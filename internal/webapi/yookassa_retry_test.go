package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"shop_bot/internal/payment"
	"shop_bot/internal/storage"
)

func TestYooKassaMiniAppResumeAfterProviderError(t *testing.T) {
	f, db, productID, _ := realOpenPriceFixture(t)
	ctx := context.Background()
	store := storage.NewSQLOrderStore(db)
	id, err := store.CreateOrder(ctx, &storage.Order{
		UserID: 42, Status: storage.OrderStatusPending, TotalRUB: 100.01, TotalUSD: 1, TotalStars: 50,
	}, []storage.OrderItem{{ProductID: productID, ProductName: "Plane", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.GetOrder(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/payments" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Amount       struct{ Value, Currency string }
			Metadata     map[string]string
			Confirmation struct {
				Type      string
				ReturnURL string `json:"return_url"`
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Amount.Value != "100.01" || body.Amount.Currency != "RUB" || body.Metadata["order_id"] != fmt.Sprint(id) {
			t.Errorf("retry did not use saved RUB snapshot: %+v", body)
		}
		if body.Confirmation.Type != "redirect" || body.Confirmation.ReturnURL != "https://t.me/test_shop_bot" {
			t.Errorf("wrong redirect confirmation: %+v", body.Confirmation)
		}
		keys = append(keys, r.Header.Get("Idempotence-Key"))
		w.Header().Set("Content-Type", "application/json")
		if len(keys) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"type":"error","code":"internal_server_error","description":"temporary test failure"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"retry_card_payment","test":true,"status":"pending","paid":false,"confirmation":{"type":"redirect","confirmation_url":"https://yoomoney.ru/test/retry"}}`))
	}))
	defer api.Close()
	provider := payment.NewYooKassaPayment("test-shop", "fake-test-key", "https://t.me/test_shop_bot")
	provider.SetBaseURL(api.URL + "/v3")
	f.server.deps.YooKassa = provider

	target := fmt.Sprintf("/api/orders/%d/pay", id)
	first := f.request(t, http.MethodPost, target, `{"method":"yookassa"}`, true)
	if first.Code != http.StatusBadGateway {
		t.Fatalf("provider failure: %d %s", first.Code, first.Body.String())
	}
	second := f.request(t, http.MethodPost, target, `{"method":"yookassa","total_rub":1}`, true)
	if second.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", second.Code, second.Body.String())
	}
	response := decodeJSON(t, second)
	if response["order_id"] != float64(id) || response["invoice_link"] != "https://yoomoney.ru/test/retry" {
		t.Fatalf("wrong retry response: %v", response)
	}
	if len(keys) != 2 || keys[0] == "" || keys[1] == "" || keys[0] == keys[1] {
		t.Fatalf("retry reused/missed idempotence key: %v", keys)
	}
	after, err := store.GetOrder(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("provider failure/retry changed order or marked it paid")
	}
	var orders, events int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM orders").Scan(&orders); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM payment_events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if orders != 1 || events != 0 {
		t.Fatalf("retry created order/payment: orders=%d events=%d", orders, events)
	}
}
