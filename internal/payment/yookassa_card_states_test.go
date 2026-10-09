package payment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The merchant uses redirect confirmation, so card data never passes through
// our API. These fixtures model the payment objects returned after the hosted
// page's 3-D Secure or bank refusal, not a simulation of card processing.
func TestYooKassaHostedCardPaymentStates(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		paid, settle bool
	}{
		{"awaiting_card_or_3ds", "pending", false, false},
		{"authorized_not_captured", "waiting_for_capture", true, false},
		{"bank_declined", "canceled", false, false},
		{"succeeded_without_paid_flag", "succeeded", false, false},
		{"captured_test_payment", "succeeded", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requireYooKassaBasicAuth(t, r)
				if r.Method != http.MethodGet || r.URL.Path != "/v3/payments/card_test" {
					t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "card_test", "test": true, "status": tc.status, "paid": tc.paid,
					"amount":         map[string]string{"value": "100.01", "currency": "RUB"},
					"metadata":       map[string]string{"order_id": "42"},
					"created_at":     "2026-10-09T10:00:00Z",
					"captured_at":    "2026-10-09T10:01:00Z",
					"payment_method": map[string]any{"type": "bank_card", "card": map[string]string{"card_type": "MasterCard", "last4": "4477"}},
				})
			}))
			defer srv.Close()
			p, err := newYookassaTestClient(srv).GetPayment(context.Background(), "card_test")
			if err != nil {
				t.Fatal(err)
			}
			if p.Status != tc.status || p.Paid != tc.paid {
				t.Fatalf("wrong state: %+v", p)
			}
			receipt, err := p.PaymentReceipt()
			if !tc.settle {
				if !errors.Is(err, ErrInvalidYooKassaReceipt) {
					t.Fatalf("unsettled state produced receipt: %+v, %v", receipt, err)
				}
				return
			}
			if err != nil || receipt.OrderID != 42 || receipt.AmountMinor != 10001 || receipt.Currency != "RUB" {
				t.Fatalf("wrong captured receipt: %+v, %v", receipt, err)
			}
		})
	}
}
