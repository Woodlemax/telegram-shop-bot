package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"shop_bot/internal/storage"
)

type checkoutAPIMock struct {
	mu                       sync.Mutex
	srv                      *httptest.Server
	requests                 map[string]string
	payments                 map[string]map[string]any
	creates                  int
	failCreateOnce, failRead bool
}

func newCheckoutAPIMock(t *testing.T) *checkoutAPIMock {
	t.Helper()
	m := &checkoutAPIMock{requests: map[string]string{}, payments: map[string]map[string]any{}}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireYooKassaBasicAuth(t, r)
		m.mu.Lock()
		defer m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			if m.failRead {
				w.WriteHeader(503)
				return
			}
			p := m.payments[strings.TrimPrefix(r.URL.Path, "/v3/payments/")]
			if p == nil {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(p)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v3/payments" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		key := r.Header.Get("Idempotence-Key")
		if key == "" {
			t.Error("missing creation key")
		}
		raw, _ := json.Marshal(body)
		if previous, ok := m.requests[key]; ok {
			var request map[string]any
			_ = json.Unmarshal([]byte(previous), &request)
			savedBody, _ := json.Marshal(request["body"])
			if string(raw) != string(savedBody) {
				t.Error("same key used with different creation body")
			}
			_ = json.NewEncoder(w).Encode(m.payments[request["id"].(string)])
			return
		}
		m.creates++
		id := fmt.Sprintf("card_%d", m.creates)
		p := map[string]any{
			"id": id, "test": true, "paid": false, "status": "pending",
			"amount": body["amount"], "metadata": body["metadata"],
			"confirmation": map[string]string{"type": "redirect", "confirmation_url": "https://yoomoney.ru/test/" + id},
			"created_at":   "2026-10-09T10:00:00Z",
		}
		m.payments[id] = p
		record, _ := json.Marshal(map[string]any{"id": id, "body": body})
		m.requests[key] = string(record)
		// Model a payment created remotely while its success response is lost.
		if m.failCreateOnce {
			m.failCreateOnce = false
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"code":"internal_server_error"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(p)
	}))
	t.Cleanup(m.srv.Close)
	return m
}
func (m *checkoutAPIMock) status(id, status string, paid bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.payments[id]["status"] = status
	m.payments[id]["paid"] = paid
}
func (m *checkoutAPIMock) count() int { m.mu.Lock(); defer m.mu.Unlock(); return m.creates }

func newCheckoutFixture(t *testing.T) (*storage.DB, int64, *checkoutAPIMock, func() *YooKassaCheckout) {
	t.Helper()
	db, err := storage.New(filepath.Join(t.TempDir(), "checkout.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	id, err := storage.NewSQLOrderStore(db).CreateOrder(context.Background(), &storage.Order{UserID: 42, Status: storage.OrderStatusPending, TotalRUB: 100.01}, nil)
	if err != nil {
		t.Fatal(err)
	}
	api := newCheckoutAPIMock(t)
	client := func() *YooKassaCheckout {
		y := NewYooKassaCheckout(yookassaTestShopID, yookassaTestSecretKey, yookassaTestReturnURL, storage.NewSQLYooKassaCheckoutStore(db))
		y.SetBaseURL(api.srv.URL + "/v3")
		return y
	}
	return db, id, api, client
}
func requireCheckout(t *testing.T, y *YooKassaCheckout, id int64) *Invoice {
	t.Helper()
	invoice, err := y.CreatePayment(context.Background(), id, 10001, "Plane")
	if err != nil {
		t.Fatal(err)
	}
	return invoice
}

func TestYooKassaCheckoutConcurrentBotAndMiniAppShareOnePayment(t *testing.T) {
	_, id, api, client := newCheckoutFixture(t)
	callers := []*YooKassaCheckout{client(), client()}
	var wg sync.WaitGroup
	invoices := make(chan *Invoice, 24)
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			inv, err := callers[index%2].CreatePayment(context.Background(), id, 10001, fmt.Sprintf("caller %d", index))
			invoices <- inv
			errs <- err
		}(i)
	}
	wg.Wait()
	close(invoices)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for inv := range invoices {
		if inv == nil || inv.InvoiceID != "card_1" || inv.PayURL != "https://yoomoney.ru/test/card_1" {
			t.Fatalf("multiple active payments: %+v", inv)
		}
	}
	if api.count() != 1 {
		t.Fatalf("created %d provider payments", api.count())
	}
}
func TestYooKassaCheckoutSurvivesDatabaseReopen(t *testing.T) {
	db, id, api, client := newCheckoutFixture(t)
	first := requireCheckout(t, client(), id)
	var dbPath string
	if err := db.Conn().QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&dbPath); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	y := NewYooKassaCheckout(yookassaTestShopID, yookassaTestSecretKey, "https://t.me/changed_return", storage.NewSQLYooKassaCheckoutStore(reopened))
	y.SetBaseURL(api.srv.URL + "/v3")
	second := requireCheckout(t, y, id)
	if *first != *second || api.count() != 1 {
		t.Fatalf("restart duplicated payment: %+v %+v count=%d", first, second, api.count())
	}
}
func TestYooKassaCheckoutOnlyCanceledPaymentCanBeReplaced(t *testing.T) {
	_, id, api, client := newCheckoutFixture(t)
	first := requireCheckout(t, client(), id)
	api.status(first.InvoiceID, "canceled", false)
	second := requireCheckout(t, client(), id)
	if first.InvoiceID == second.InvoiceID || api.count() != 2 {
		t.Fatal("confirmed cancellation did not get a fresh payment")
	}
	third := requireCheckout(t, client(), id)
	if *second != *third || api.count() != 2 {
		t.Fatal("reopening replacement created another charge")
	}
}
func TestYooKassaCheckoutConcurrentCanceledRotationCreatesOneReplacement(t *testing.T) {
	_, id, api, client := newCheckoutFixture(t)
	first := requireCheckout(t, client(), id)
	api.status(first.InvoiceID, "canceled", false)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inv, err := client().CreatePayment(context.Background(), id, 10001, "Retry")
			if err == nil && inv.InvoiceID != "card_2" {
				err = fmt.Errorf("wrong replacement %+v", inv)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if api.count() != 2 {
		t.Fatalf("created %d payments, want initial+one replacement", api.count())
	}
}
func TestYooKassaCheckoutLostCreationResponseReusesFrozenRequest(t *testing.T) {
	db, id, api, client := newCheckoutFixture(t)
	api.failCreateOnce = true
	if _, err := client().CreatePayment(context.Background(), id, 10001, "Bot description"); err == nil {
		t.Fatal("expected lost-response error")
	}
	y := client()
	y.returnURL = "https://t.me/another_return"
	inv, err := y.CreatePayment(context.Background(), id, 10001, "Mini App description")
	if err != nil {
		t.Fatal(err)
	}
	if inv.InvoiceID != "card_1" || api.count() != 1 {
		t.Fatal("uncertain request charged twice")
	}
	var saved string
	if err := db.Conn().QueryRow("SELECT description FROM yookassa_checkout_intents WHERE order_id=?", id).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if saved != "Bot description" {
		t.Fatalf("creation body changed: %s", saved)
	}
}
func TestYooKassaCheckoutBlocksSubmittedPaymentsAndReadFailures(t *testing.T) {
	for _, status := range []string{"succeeded", "waiting_for_capture", "read_error", "unknown"} {
		t.Run(status, func(t *testing.T) {
			_, id, api, client := newCheckoutFixture(t)
			inv := requireCheckout(t, client(), id)
			switch status {
			case "read_error":
				api.mu.Lock()
				api.failRead = true
				api.mu.Unlock()
			case "unknown":
				api.status(inv.InvoiceID, "unknown", false)
			default:
				api.status(inv.InvoiceID, status, true)
			}
			if _, err := client().CreatePayment(context.Background(), id, 10001, "Reopen"); err == nil {
				t.Fatal("submitted or uncertain payment was reopened")
			}
			if api.count() != 1 {
				t.Fatal("another payment created before settlement")
			}
		})
	}
}
func TestYooKassaCheckoutOldUncertainCreationFailsClosed(t *testing.T) {
	db, id, api, client := newCheckoutFixture(t)
	api.failCreateOnce = true
	if _, err := client().CreatePayment(context.Background(), id, 10001, "Plane"); err == nil {
		t.Fatal("expected lost response")
	}
	if _, err := db.Conn().Exec("UPDATE yookassa_checkout_intents SET created_at=?", time.Now().Add(-25*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := client().CreatePayment(context.Background(), id, 10001, "Plane"); !errors.Is(err, ErrYooKassaAwaitingConfirmation) {
		t.Fatalf("old unknown request retried: %v", err)
	}
	if api.count() != 1 {
		t.Fatal("expired idempotence key used to create second charge")
	}
}
func TestYooKassaCheckoutRejectsStaleOrderOrAmount(t *testing.T) {
	for _, state := range []string{"paid", "cancelled", "needs_review", "wrong_amount"} {
		t.Run(state, func(t *testing.T) {
			db, id, api, client := newCheckoutFixture(t)
			requireCheckout(t, client(), id)
			amount := int64(10001)
			switch state {
			case "wrong_amount":
				amount = 1
			case "needs_review":
				if _, err := db.Conn().Exec("UPDATE orders SET payment_state='needs_review' WHERE id=?", id); err != nil {
					t.Fatal(err)
				}
			default:
				if _, err := db.Conn().Exec("UPDATE orders SET status=? WHERE id=?", state, id); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := client().CreatePayment(context.Background(), id, amount, "Stale screen"); !errors.Is(err, storage.ErrOrderStatusConflict) {
				t.Fatalf("stale order accepted: %v", err)
			}
			if api.count() != 1 {
				t.Fatal("stale order created charge")
			}
		})
	}
}
