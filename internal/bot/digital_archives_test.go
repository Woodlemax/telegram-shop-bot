package bot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/config"
	"shop_bot/internal/storage"
)

func digitalDocument(e *e2eEnv, userID int64, name, fileID string) []tgCall {
	return e.do(tgbotapi.Update{Message: &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: userID, Type: "private"}, From: &tgbotapi.User{ID: userID, LanguageCode: "ru"},
		Document: &tgbotapi.Document{FileID: fileID, FileName: name, FileSize: 80_000_000},
	}})
}

func uploadDigital(e *e2eEnv, fileID string) {
	e.t.Helper()
	e.cmd(e2eAdminID, fmt.Sprintf("/setarchive %d", e.prodReg), "ru")
	calls := digitalDocument(e, e2eAdminID, "plane.zip", fileID)
	if !strings.Contains(tgText(calls), "ZIP прикреплён") {
		e.t.Fatalf("archive upload: %s", tgText(calls))
	}
}

func documentCalls(calls []tgCall) []tgCall {
	var docs []tgCall
	for _, c := range calls {
		if c.Method == "sendDocument" {
			docs = append(docs, c)
		}
	}
	return docs
}

func TestDigitalArchivePaymentAndReplacement(t *testing.T) {
	e := newE2EEnv(t)
	ctx := context.Background()
	buyer := int64(781)
	uploadDigital(e, "zip-80mb-v1")
	// The upload uses only document metadata, never getFile/download/upload.
	if got := e.qInt("SELECT size_bytes FROM digital_archives LIMIT 1"); got != 80_000_000 {
		t.Fatalf("size=%d", got)
	}
	e.cmd(buyer, "/start", "ru")
	orderID := e.placeOrder(buyer, e.prodReg, "")
	deliveryID := e.qInt("SELECT id FROM digital_deliveries WHERE order_id=?", orderID)
	e.bot.ProcessDigitalDeliveries(ctx)
	if len(documentCalls(e.tg.since(0))) != 0 {
		t.Fatal("unpaid archive sent")
	}
	calls := e.cb(buyer, fmt.Sprintf("digital:download:%d", deliveryID), "ru")
	if len(documentCalls(calls)) != 0 {
		t.Fatal("unpaid download allowed")
	}
	e.payWithStars(buyer, orderID, "archive-charge")
	before := e.tg.count()
	e.bot.ProcessDigitalDeliveries(ctx)
	docs := documentCalls(e.tg.since(before))
	if len(docs) != 1 || docs[0].Params.Get("document") != "zip-80mb-v1" || docs[0].Params.Get("chat_id") != strconv.FormatInt(buyer, 10) {
		t.Fatalf("delivery=%+v", docs)
	}
	if e.qStr("SELECT status FROM orders WHERE id=?", orderID) != "delivered" {
		t.Fatal("delivery not recorded")
	}
	if got := e.qInt("SELECT stock FROM products WHERE id=?", e.prodReg); got != 5 {
		t.Fatalf("digital stock decremented: %d", got)
	}
	// Exact payment replay must not create a second delivery.
	e.successfulPayment(buyer, strconv.FormatInt(orderID, 10), 500, "archive-charge")
	before = e.tg.count()
	e.bot.ProcessDigitalDeliveries(ctx)
	if len(documentCalls(e.tg.since(before))) != 0 {
		t.Fatal("duplicate automatic delivery")
	}
	// Replacement changes the entitlement of an already-delivered purchase.
	uploadDigital(e, "zip-80mb-v2")
	before = e.tg.count()
	e.bot.ProcessDigitalDeliveries(ctx)
	if len(documentCalls(e.tg.since(before))) != 0 {
		t.Fatal("replacement broadcast without a download request")
	}
	calls = e.cb(buyer, fmt.Sprintf("digital:download:%d", deliveryID), "ru")
	docs = documentCalls(calls)
	if len(docs) != 1 || docs[0].Params.Get("document") != "zip-80mb-v2" {
		t.Fatalf("updated download=%+v", docs)
	}
	calls = e.cb(buyer+1, fmt.Sprintf("digital:download:%d", deliveryID), "ru")
	if len(documentCalls(calls)) != 0 {
		t.Fatal("foreign purchase allowed")
	}
	if !strings.Contains(tgText(e.cmd(buyer, "/files", "ru")), "Мои файлы") {
		t.Fatal("library missing")
	}
	_, err := e.db.Conn().Exec("UPDATE orders SET payment_state='refunded' WHERE id=?", orderID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.bot.archives.Owned(ctx, buyer, deliveryID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("refunded entitlement: %v", err)
	}
}

func TestDigitalArchiveAdminAndZIPValidation(t *testing.T) {
	e := newE2EEnv(t)
	e.cmd(781, fmt.Sprintf("/setarchive %d", e.prodReg), "ru")
	digitalDocument(e, 781, "plane.zip", "foreign-file")
	if e.qInt("SELECT count(*) FROM digital_archives") != 0 {
		t.Fatal("non-admin uploaded archive")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/setarchive %d", e.prodReg), "ru")
	digitalDocument(e, e2eAdminID, "plane.exe", "not-a-zip")
	if e.qInt("SELECT count(*) FROM digital_archives") != 0 {
		t.Fatal("non-ZIP accepted")
	}
	e.cmd(e2eAdminID, "/cancel", "ru")
	digitalDocument(e, e2eAdminID, "plane.zip", "canceled-file")
	if e.qInt("SELECT count(*) FROM digital_archives") != 0 {
		t.Fatal("canceled upload accepted")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/setarchive %d", e.prodReg), "ru")
	if _, err := e.db.Conn().Exec("UPDATE digital_uploads SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	digitalDocument(e, e2eAdminID, "plane.zip", "expired-file")
	if e.qInt("SELECT count(*) FROM digital_archives") != 0 {
		t.Fatal("expired upload accepted")
	}
}

type digitalFailClient struct{}

func (digitalFailClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("simulated offline")
}

func TestDigitalArchiveRetryAndLeaseRecovery(t *testing.T) {
	e := newE2EEnv(t)
	ctx := context.Background()
	buyer := int64(782)
	uploadDigital(e, "retry-zip")
	e.cmd(buyer, "/start", "ru")
	id := e.placeOrder(buyer, e.prodReg, "")
	e.payWithStars(buyer, id, "retry-charge")
	original := e.bot.api.Client
	e.bot.api.Client = digitalFailClient{}
	e.bot.ProcessDigitalDeliveries(ctx)
	e.bot.api.Client = original
	if e.qStr("SELECT state FROM digital_deliveries WHERE order_id=?", id) != "pending" {
		t.Fatal("failed send not queued")
	}
	if e.qStr("SELECT status FROM orders WHERE id=?", id) != "paid" {
		t.Fatal("failed send marked delivered")
	}
	if _, err := e.db.Conn().Exec("UPDATE digital_deliveries SET available_at=0"); err != nil {
		t.Fatal(err)
	}
	claim, err := e.bot.archives.Claim(ctx)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	another, err := e.bot.archives.Claim(ctx)
	if err != nil || another != nil {
		t.Fatalf("double claim: %v %+v", err, another)
	}
	if _, err := e.db.Conn().Exec("UPDATE digital_deliveries SET lease_until=0"); err != nil {
		t.Fatal(err)
	}
	before := e.tg.count()
	e.bot.ProcessDigitalDeliveries(ctx)
	if len(documentCalls(e.tg.since(before))) != 1 {
		t.Fatal("abandoned lease not recovered")
	}
	if err := e.bot.archives.Finish(ctx, claim, 999, true); !errors.Is(err, storage.ErrOrderStatusConflict) {
		t.Fatalf("stale claim overwrote result: %v", err)
	}
}

func TestDigitalArchivePaymentRailsAndQuantity(t *testing.T) {
	e := newE2EEnv(t)
	ctx := context.Background()
	uploadDigital(e, "all-rails-zip")
	orders := storage.NewSQLOrderStore(e.db)
	for i, rail := range []struct {
		provider, currency string
		amount             int64
		scale              int
		payer              bool
	}{
		{storage.PaymentMethodStars, "XTR", 500, 0, true},
		{storage.PaymentMethodCrypto, "USDT", 1000, 2, true},
		{storage.PaymentMethodYooKassa, "RUB", 92500, 2, false},
		{storage.PaymentMethodStripe, "USD", 1000, 2, false},
		{storage.PaymentMethodTON, "TON", 2_000_000_000, 9, false},
		{storage.PaymentMethodNowpayments, "USD", 1000, 2, false},
		{storage.PaymentMethodBalance, "USD", 1000, 2, true},
	} {
		t.Run(rail.provider, func(t *testing.T) {
			buyer := int64(800 + i)
			e.cmd(buyer, "/start", "ru")
			id := e.placeOrder(buyer, e.prodReg, "")
			if _, err := e.db.Conn().Exec("UPDATE orders SET total_rub=925,total_ton_nano=2000000000 WHERE id=?", id); err != nil {
				t.Fatal(err)
			}
			fact := storage.PaymentFact{Provider: rail.provider, ExternalID: "digital-" + rail.provider,
				AmountMinor: rail.amount, Currency: rail.currency, Scale: rail.scale}
			if rail.payer {
				fact.PayerID = buyer
			}
			wrong := fact
			wrong.AmountMinor--
			if err := orders.UpdateOrderStatusWithPaymentFact(ctx, id, storage.OrderStatusPending, storage.OrderStatusPaid, wrong); !errors.Is(err, storage.ErrPaymentReceiptMismatch) {
				t.Fatalf("underpayment accepted: %v", err)
			}
			before := e.tg.count()
			e.bot.ProcessDigitalDeliveries(ctx)
			if len(documentCalls(e.tg.since(before))) != 0 {
				t.Fatal("archive sent before confirmed payment")
			}
			if err := orders.UpdateOrderStatusWithPaymentFact(ctx, id, storage.OrderStatusPending, storage.OrderStatusPaid, fact); err != nil {
				t.Fatal(err)
			}
			deliveryID := e.qInt("SELECT id FROM digital_deliveries WHERE order_id=?", id)
			// A different payment identifier must not unlock a file through an unrelated ledger entry.
			if _, err := e.db.Conn().Exec("UPDATE orders SET payment_id='unverified' WHERE id=?", id); err != nil {
				t.Fatal(err)
			}
			if _, err := e.bot.archives.Owned(ctx, buyer, deliveryID); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("unverified payment access: %v", err)
			}
			if _, err := e.db.Conn().Exec("UPDATE orders SET payment_id=? WHERE id=?", fact.ExternalID, id); err != nil {
				t.Fatal(err)
			}
			before = e.tg.count()
			e.bot.ProcessDigitalDeliveries(ctx)
			docs := documentCalls(e.tg.since(before))
			if len(docs) != 1 || docs[0].Params.Get("document") != "all-rails-zip" || docs[0].Params.Get("chat_id") != strconv.FormatInt(buyer, 10) {
				t.Fatalf("delivery: %+v", docs)
			}
			if e.qStr("SELECT status FROM orders WHERE id=?", id) != "delivered" {
				t.Fatal("delivery not recorded")
			}
			if _, err := e.bot.archives.Owned(ctx, buyer, deliveryID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.db.Conn().Exec("UPDATE orders SET payment_state='refunded' WHERE id=?", id); err != nil {
				t.Fatal(err)
			}
			if _, err := e.bot.archives.Owned(ctx, buyer, deliveryID); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("refunded access: %v", err)
			}
		})
	}
	if got := e.qInt("SELECT stock FROM products WHERE id=?", e.prodReg); got != 5 {
		t.Fatalf("digital stock decremented: %d", got)
	}
	_, err := orders.CreateOrder(ctx, &storage.Order{UserID: 800, TotalUSD: 20, TotalStars: 1000, Status: storage.OrderStatusPending}, []storage.OrderItem{{ProductID: e.prodReg, ProductName: "Plane", Quantity: 2, PriceUSD: 10}})
	if !errors.Is(err, storage.ErrSingleItemLimit) {
		t.Fatalf("digital quantity accepted: %v", err)
	}
}

func TestDigitalArchiveBalanceCheckout(t *testing.T) {
	e := newE2EEnv(t)
	ctx := context.Background()
	const buyer = int64(9110)
	uploadDigital(e, "balance-zip")
	e.cmd(buyer, "/start", "en")
	e.cmd(e2eAdminID, fmt.Sprintf("/setbalance %d 25.00 test credit", buyer), "en")
	e.cb(buyer, fmt.Sprintf("cart:add:%d", e.prodReg), "en")
	e.cb(buyer, "cart:checkout", "en")
	calls := e.cb(buyer, "order:confirm", "en")
	id := e.qInt("SELECT MAX(id) FROM orders WHERE user_id=?", buyer)
	screen := requireRender(t, calls, fmt.Sprintf("pay:stars:%d", id))
	if !strings.Contains(screen.markup(), fmt.Sprintf("pay:balance:%d", id)) {
		t.Fatal("balance button missing")
	}
	e.cb(buyer, fmt.Sprintf("pay:balance:%d", id), "en")
	if e.qStr("SELECT payment_method FROM orders WHERE id=?", id) != storage.PaymentMethodBalance {
		t.Fatal("balance payment not settled")
	}
	before := e.tg.count()
	e.bot.ProcessDigitalDeliveries(ctx)
	if len(documentCalls(e.tg.since(before))) != 1 {
		t.Fatal("balance purchase did not deliver archive")
	}
	e.cb(buyer, fmt.Sprintf("pay:balance:%d", id), "en")
	if got := e.qStr("SELECT printf('%.2f',balance_usd) FROM users WHERE telegram_id=?", buyer); got != "15.00" {
		t.Fatalf("balance charged twice: %s", got)
	}
}

func TestDigitalArchiveYooKassaCheckout(t *testing.T) {
	api := newYookassaE2EAPIMock(t)
	e := newE2EEnvWithConfig(t, func(c *config.Config) { enableYooKassa(c) })
	e.bot.yookassa.SetBaseURL(api.srv.URL + "/v3")
	const buyer = int64(5010)
	uploadDigital(e, "card-zip-v1")
	e.cmd(buyer, "/start", "en")
	e.cb(buyer, fmt.Sprintf("cart:add:%d", e.prodReg), "en")
	e.cb(buyer, "cart:checkout", "en")
	calls := e.cb(buyer, "order:confirm", "en")
	id := e.qInt("SELECT MAX(id) FROM orders WHERE user_id=?", buyer)
	screen := requireRender(t, calls, fmt.Sprintf("pay:stars:%d", id))
	if !strings.Contains(screen.markup(), fmt.Sprintf("pay:yookassa:%d", id)) {
		t.Fatal("card button missing")
	}
	calls = e.cb(buyer, fmt.Sprintf("pay:yookassa:%d", id), "en")
	requireRender(t, calls, yookassaE2EConfirmationURL)
	before := e.tg.count()
	e.bot.ProcessDigitalDeliveries(context.Background())
	if len(documentCalls(e.tg.since(before))) != 0 {
		t.Fatal("payment link unlocked archive before payment")
	}
	api.setRefetch(yookassaRefetchJSON("pay_e2e", "succeeded", "925.00", true, id))
	body := yookassaNotificationBody("payment.succeeded", "pay_e2e")
	post := func() {
		rec := httptest.NewRecorder()
		e.bot.YooKassaWebhookHandler()(rec, httptest.NewRequest(http.MethodPost, "/yookassa-webhook", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("payment webhook: %d %s", rec.Code, rec.Body.String())
		}
	}
	post()
	before = e.tg.count()
	e.bot.ProcessDigitalDeliveries(context.Background())
	docs := documentCalls(e.tg.since(before))
	if len(docs) != 1 || docs[0].Params.Get("document") != "card-zip-v1" {
		t.Fatalf("card delivery: %+v", docs)
	}
	post()
	before = e.tg.count()
	e.bot.ProcessDigitalDeliveries(context.Background())
	if len(documentCalls(e.tg.since(before))) != 0 {
		t.Fatal("duplicate payment notification resent ZIP")
	}
	uploadDigital(e, "card-zip-v2")
	deliveryID := e.qInt("SELECT id FROM digital_deliveries WHERE order_id=?", id)
	docs = documentCalls(e.cb(buyer, fmt.Sprintf("digital:download:%d", deliveryID), "en"))
	if len(docs) != 1 || docs[0].Params.Get("document") != "card-zip-v2" {
		t.Fatalf("updated card purchase: %+v", docs)
	}
}
