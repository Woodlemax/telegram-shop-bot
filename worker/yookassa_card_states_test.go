package worker

import (
	"context"
	"testing"
	"time"

	"shop_bot/internal/payment"
)

func TestYooKassaPollingIgnoresIncompleteCardPayments(t *testing.T) {
	for _, tc := range []struct {
		status string
		paid   bool
	}{
		{"pending", false}, {"waiting_for_capture", true}, {"canceled", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			p := yooPayment("card_incomplete", 42, "100.01")
			p.Status, p.Paid = tc.status, tc.paid
			lister := &stubYooKassaLister{pages: []yookassaListPage{{items: []payment.Payment{p}}}}
			confirmer := &recordingConfirmer{}
			worker := NewYooKassaPollingWorker(lister, confirmer, nil, time.Minute)
			worker.PollOnce(context.Background())
			if len(confirmer.confirmed) != 0 || len(confirmer.receipts) != 0 || len(confirmer.anomalies) != 0 {
				t.Fatalf("incomplete payment touched ledger: %+v", confirmer)
			}
			if len(lister.calls) != 1 || lister.calls[0].status != "succeeded" {
				t.Fatalf("wrong provider filter: %+v", lister.calls)
			}
		})
	}
}
