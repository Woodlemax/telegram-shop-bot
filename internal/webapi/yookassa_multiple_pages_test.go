package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"shop_bot/internal/payment"
	"shop_bot/internal/storage"
)

// Reproduces the buyer opening an unpaid order in two separate app sessions.
// Each session gets a separate adapter, like the bot and the Mini App.
func TestYooKassaTwoPaymentPagesReuseOneInvoice(t *testing.T) {
	f, db, productID, _ := realOpenPriceFixture(t)
	id, err := storage.NewSQLOrderStore(db).CreateOrder(context.Background(), &storage.Order{
		UserID: 42, Status: storage.OrderStatusPending, TotalRUB: 100.01, TotalStars: 50,
	}, []storage.OrderItem{{ProductID: productID, ProductName: "Plane", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	var posts, reads atomic.Int32
	var paid atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/v3/payments" {
			posts.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "one_card_payment", "confirmation": map[string]string{"confirmation_url": "https://yoomoney.ru/test/single"}})
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v3/payments/one_card_payment" {
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
		}
		reads.Add(1)
		state := "pending"
		if paid.Load() {
			state = "succeeded"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "one_card_payment", "test": true, "status": state, "paid": paid.Load(),
			"amount":   map[string]string{"value": "100.01", "currency": "RUB"},
			"metadata": map[string]string{"order_id": fmt.Sprint(id)},
		})
	}))
	defer api.Close()
	newSession := func() {
		adapter := payment.NewYooKassaCheckout("test-shop", "fake-test-key", "https://t.me/test_shop_bot", storage.NewSQLYooKassaCheckoutStore(db))
		adapter.SetBaseURL(api.URL + "/v3")
		f.server.deps.YooKassa = adapter
	}
	for i := 0; i < 2; i++ {
		newSession()
		result := f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/pay", id), `{"method":"yookassa"}`, true)
		if result.Code != 200 {
			t.Fatalf("session %d: %d %s", i, result.Code, result.Body.String())
		}
		if got := decodeJSON(t, result)["invoice_link"]; got != "https://yoomoney.ru/test/single" {
			t.Fatalf("different payment page: %v", got)
		}
	}
	if posts.Load() != 1 || reads.Load() != 1 {
		t.Fatalf("two sessions created two payments: posts=%d reads=%d", posts.Load(), reads.Load())
	}
	// The provider has received money but no webhook/poller has settled the
	// local order yet. Opening another page must still not create a charge.
	paid.Store(true)
	result := f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/pay", id), `{"method":"yookassa"}`, true)
	if result.Code != http.StatusConflict || posts.Load() != 1 {
		t.Fatalf("paid provider payment reopened: HTTP=%d creations=%d", result.Code, posts.Load())
	}
}
