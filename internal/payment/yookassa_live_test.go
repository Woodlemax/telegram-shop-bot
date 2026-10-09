package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"shop_bot/internal/storage"
)

// Opt-in real-provider check. Creates an unpaid TEST payment for an isolated
// local order; never visits confirmation or creates an order in the shop.
func TestYooKassaCheckoutLiveTestMerchantReusesPayment(t *testing.T) {
	if os.Getenv("RUN_YOOKASSA_LIVE_TESTS") != "1" {
		t.Skip("explicit live test opt-in required")
	}
	shopID, key, returnURL := os.Getenv("YOOKASSA_TEST_SHOP_ID"), os.Getenv("YOOKASSA_TEST_SECRET_KEY"), os.Getenv("YOOKASSA_TEST_RETURN_URL")
	if shopID == "" || key == "" || returnURL == "" {
		t.Fatal("test merchant settings missing")
	}
	db, err := storage.New(filepath.Join(t.TempDir(), "isolated-live-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	y := NewYooKassaCheckout(shopID, key, returnURL, storage.NewSQLYooKassaCheckoutStore(db))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, y.baseURL+"/me", nil)
	if err != nil {
		t.Fatal("merchant request failed")
	}
	req.Header.Set("Authorization", y.basicAuth())
	raw, status, err := y.doJSON(req)
	if err != nil || status != 200 {
		t.Fatal("merchant verification failed")
	}
	var merchant struct {
		AccountID string `json:"account_id"`
		Test      bool   `json:"test"`
	}
	if json.Unmarshal(raw, &merchant) != nil || !merchant.Test || merchant.AccountID != shopID {
		t.Fatal("refusing to create payment outside verified TEST merchant")
	}
	// Use a high isolated ID so even an accidentally completed payment cannot
	// be associated with a real production order by the polling worker.
	if _, err = db.Conn().Exec("INSERT INTO sqlite_sequence(name,seq) VALUES('orders',?)", time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	id, err := storage.NewSQLOrderStore(db).CreateOrder(ctx, &storage.Order{UserID: 42, Status: storage.OrderStatusPending, TotalRUB: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := y.CreatePayment(ctx, id, 100, "Unpaid integration check: isolated order")
	if err != nil {
		t.Fatal("test payment creation failed")
	}
	secondSession := NewYooKassaCheckout(shopID, key, returnURL, storage.NewSQLYooKassaCheckoutStore(db))
	second, err := secondSession.CreatePayment(ctx, id, 100, "Second session with same isolated order")
	if err != nil {
		t.Fatal("second session did not reopen saved payment")
	}
	if *first != *second {
		t.Fatal("two sessions received different provider payments")
	}
	object, err := y.getPaymentObject(ctx, first.InvoiceID)
	if err != nil {
		t.Fatal("payment read-back failed")
	}
	if object.Status != "pending" || object.Paid || object.Metadata["order_id"] != strconv.FormatInt(id, 10) {
		t.Fatal("test payment state/order mismatch")
	}
	// GetPayment's minimal snapshot intentionally omits test. Validate the raw
	// object as well before reporting this live integration check successful.
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, y.baseURL+"/payments/"+first.InvoiceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", y.basicAuth())
	raw, status, err = y.doJSON(req)
	var mode struct {
		Test bool `json:"test"`
	}
	if err != nil || status != 200 || json.Unmarshal(raw, &mode) != nil || !mode.Test {
		t.Fatal("provider payment is not confirmed TEST mode")
	}
	t.Log("Verified two sessions share one pending TEST payment; no payment completed and no production order created")
}
