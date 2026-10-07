package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/payment"
	"shop_bot/internal/storage"
)

type captureResumeCrypto struct {
	CryptoInvoicer
	id          int64
	amount      float64
	description string
}

func (c *captureResumeCrypto) CreateInvoice(ctx context.Context, id int64, amount float64, description string) (*payment.Invoice, error) {
	c.id, c.amount, c.description = id, amount, description
	return c.CryptoInvoicer.CreateInvoice(ctx, id, amount, description)
}

type failedResumeInvoice struct{}

func (failedResumeInvoice) MakeRequest(string, tgbotapi.Params) (*tgbotapi.APIResponse, error) {
	return nil, errors.New("fake offline")
}

func TestResumeOrderPaymentUsesExistingSnapshotForEveryMethod(t *testing.T) {
	f, db, digitalID, physicalID := realOpenPriceFixture(t)
	ctx := context.Background()
	store := storage.NewSQLOrderStore(db)
	id, err := store.CreateOrder(ctx, &storage.Order{UserID: 42, Status: storage.OrderStatusPending, TotalRUB: 123.45, TotalUSD: 2.50, TotalStars: 125, TotalTonNano: 123456789, DiscountPct: 10, PromoCode: "SAVED"}, []storage.OrderItem{{ProductID: digitalID, ProductName: "Original plane", Quantity: 1, PriceUSD: 2.50}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.deps.Cart.ChangeQuantity(ctx, 42, physicalID, 1); err != nil {
		t.Fatal(err)
	}
	cartBefore, err := f.server.deps.Cart.Get(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn().Exec(`UPDATE products SET name='New name',price_rub=9999 WHERE id=?`, digitalID); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetOrder(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	crypto := &captureResumeCrypto{CryptoInvoicer: f.crypto}
	f.server.deps.Crypto = crypto
	methods := []string{"stars", "crypto", "yookassa", "stripe", "ton", "nowpayments"}
	detail := f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", id), "", true)
	got := decodeJSON(t, detail)["order"].(map[string]any)["payment_methods"].([]any)
	if len(got) != len(methods) {
		t.Fatalf("available methods: %v", got)
	}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			for i := 0; i < 2; i++ {
				r := f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/pay", id), fmt.Sprintf(`{"method":%q,"total_rub":1,"promo":"NEW","user_id":43}`, method), true)
				if r.Code != http.StatusOK {
					t.Fatalf("resume: %d %s", r.Code, r.Body.String())
				}
				result := decodeJSON(t, r)
				if result["order_id"] != float64(id) || result["invoice_link"] == "" || r.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("invoice: %v", result)
				}
			}
			switch method {
			case "stars":
				var prices []tgbotapi.LabeledPrice
				if err := json.Unmarshal([]byte(f.tg.params["prices"]), &prices); err != nil {
					t.Fatal(err)
				}
				if f.tg.params["payload"] != strconv.FormatInt(id, 10) || f.tg.params["currency"] != "XTR" || len(prices) != 1 || prices[0].Amount != 125 || f.tg.params["description"] != "Original plane × 1" {
					t.Fatalf("Stars snapshot: %v", f.tg.params)
				}
			case "crypto":
				if crypto.id != id || crypto.amount != 2.50 || crypto.description != "Original plane × 1" {
					t.Fatalf("crypto: %+v", crypto)
				}
			case "yookassa":
				if f.yookassa.gotOrderID != id || f.yookassa.gotAmount != 12345 || f.yookassa.gotDesc != "Original plane × 1" {
					t.Fatalf("RUB snapshot: %+v", f.yookassa)
				}
			case "stripe":
				if f.stripe.gotOrderID != id || f.stripe.gotAmount != 250 {
					t.Fatalf("USD snapshot: %+v", f.stripe)
				}
			case "ton":
				if f.ton.gotOrderID != id || f.ton.gotNano != 123456789 {
					t.Fatalf("TON snapshot: %+v", f.ton)
				}
			case "nowpayments":
				if f.nowpayments.gotOrderID != id || f.nowpayments.gotAmount != 250 {
					t.Fatalf("NOWPayments snapshot: %+v", f.nowpayments)
				}
			}
		})
	}
	after, err := store.GetOrder(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("resuming payment changed committed order")
	}
	cartAfter, err := f.server.deps.Cart.Get(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cartBefore, cartAfter) {
		t.Fatal("resuming payment changed cart")
	}
	var count, stock, receipts int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn().QueryRow(`SELECT stock FROM products WHERE id=?`, digitalID).Scan(&stock); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM payment_events`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if count != 1 || stock != 1 || receipts != 0 {
		t.Fatalf("unexpected order/stock/payment mutation: %d %d %d", count, stock, receipts)
	}
}

func TestResumeOrderPaymentRejectsForeignTerminalAndUnavailableMethods(t *testing.T) {
	f, db, digitalID, _ := realOpenPriceFixture(t)
	ctx := context.Background()
	store := storage.NewSQLOrderStore(db)
	create := func(owner int64) int64 {
		t.Helper()
		id, err := store.CreateOrder(ctx, &storage.Order{UserID: owner, Status: "pending", TotalUSD: 1, TotalRUB: 85, TotalStars: 50}, []storage.OrderItem{{ProductID: digitalID, ProductName: "Plane", Quantity: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	id, foreign := create(42), create(43)
	post := func(orderID int64, method string, auth bool) int {
		return f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/pay", orderID), fmt.Sprintf(`{"method":%q}`, method), auth).Code
	}
	if got := post(id, "stars", false); got != http.StatusUnauthorized {
		t.Fatalf("no auth: %d", got)
	}
	for _, other := range []int64{foreign, 999999} {
		if got := post(other, "stars", true); got != http.StatusNotFound {
			t.Fatalf("foreign/missing: %d", got)
		}
	}
	for _, other := range []int64{0, -1} {
		if got := post(other, "stars", true); got != http.StatusBadRequest {
			t.Fatalf("bad ID: %d", got)
		}
	}
	for _, method := range []string{"unknown", "free", "balance", "ton"} {
		if got := post(id, method, true); got != http.StatusBadRequest {
			t.Fatalf("unavailable %s: %d", method, got)
		}
	}
	f.yookassa.configured = false
	if got := post(id, "yookassa", true); got != http.StatusBadRequest {
		t.Fatalf("disabled provider: %d", got)
	}
	for _, state := range []struct{ status, payment string }{{"paid", "settled"}, {"delivered", "settled"}, {"cancelled", "cancelled"}, {"pending", "needs_review"}, {"paid", "refunded"}, {"paid", "partially_refunded"}} {
		if _, err := db.Conn().Exec(`UPDATE orders SET status=?,payment_state=? WHERE id=?`, state.status, state.payment, id); err != nil {
			t.Fatal(err)
		}
		if got := post(id, "stars", true); got != http.StatusConflict {
			t.Fatalf("terminal state %v: %d", state, got)
		}
	}
	if f.tg.endpoint != "" || f.yookassa.called || f.ton.called {
		t.Fatal("rejected resume contacted provider")
	}
	r := f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/pay", foreign), "broken", true)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", r.Code)
	}
}

func TestResumeOrderPaymentFreeAndSubscriptionAndRetry(t *testing.T) {
	f, db, digitalID, _ := realOpenPriceFixture(t)
	ctx := context.Background()
	store := storage.NewSQLOrderStore(db)
	free, err := store.CreateOrder(ctx, &storage.Order{UserID: 42, Status: "pending"}, []storage.OrderItem{{ProductID: digitalID, ProductName: "Free plane", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	post := func(id int64, method string) int {
		return f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/pay", id), fmt.Sprintf(`{"method":%q}`, method), true).Code
	}
	if got := post(free, "stars"); got != http.StatusBadRequest {
		t.Fatalf("zero invoice: %d", got)
	}
	if got := post(free, "free"); got != http.StatusOK {
		t.Fatalf("free grant: %d", got)
	}
	if got := post(free, "free"); got != http.StatusConflict {
		t.Fatalf("duplicate free grant: %d", got)
	}
	if files, err := storage.NewDigitalArchiveStore(db).ForOrder(ctx, 42, free); err != nil || len(files) != 1 {
		t.Fatalf("free archive access: %v %v", files, err)
	}
	sub, err := store.CreateOrder(ctx, &storage.Order{UserID: 42, Status: "pending", TotalUSD: 1, TotalRUB: 85, TotalStars: 50, SubscriptionProductID: digitalID, SubscriptionPeriodDays: 30}, []storage.OrderItem{{ProductID: digitalID, ProductName: "Saved subscription", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got := post(sub, "crypto"); got != http.StatusBadRequest {
		t.Fatalf("subscription crypto: %d", got)
	}
	f.server.deps.Tg = failedResumeInvoice{}
	if got := post(sub, "stars"); got != http.StatusBadGateway {
		t.Fatalf("provider failure: %d", got)
	}
	f.server.deps.Tg = f.tg
	if got := post(sub, "stars"); got != http.StatusOK {
		t.Fatalf("retry: %d", got)
	}
	if f.tg.params["payload"] != strconv.FormatInt(sub, 10) || f.tg.params["subscription_period"] != strconv.Itoa(subscriptionPeriodSeconds) {
		t.Fatalf("subscription snapshot: %v", f.tg.params)
	}
	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("retry created another order: %d", count)
	}
}
