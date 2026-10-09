package bot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"shop_bot/internal/storage"
)

// cancellation_details variants from the official test-card matrix:
// https://yookassa.ru/developers/payment-acceptance/testing-and-going-live/testing#test-bank-card-data
// Fixtures reproduce provider responses. The hosted card form itself is
// covered by the manual checklist in docs/yookassa-testing.md.
func TestYooKassaTestCardDeclineThenSuccessfulRetry(t *testing.T) {
	cases := []struct{ party, reason string }{
		{"payment_network", "3d_secure_failed"},
		{"payment_network", "call_issuer"},
		{"payment_network", "card_expired"},
		{"payment_network", "fraud_suspected"},
		{"payment_network", "general_decline"},
		{"payment_network", "insufficient_funds"},
		{"payment_network", "invalid_card_number"},
		{"payment_network", "invalid_csc"},
		{"payment_network", "issuer_unavailable"},
		{"payment_network", "payment_method_limit_exceeded"},
		{"payment_network", "payment_method_restricted"},
		{"yoo_money", "country_forbidden"},
		{"yoo_money", "fraud_suspected"},
	}
	for _, tc := range cases {
		t.Run(tc.party+"/"+tc.reason, func(t *testing.T) {
			api := newYookassaWebhookAPIMock(t, http.StatusOK, "")
			e := newYooKassaWebhookEnv(t, api, nil)
			ctx := context.Background()
			const buyer = int64(7901)
			uploadDigital(e, "decline-retry-plane-zip")
			orderID := placeRUBOrder(e, buyer)
			deliveryID := e.qInt("SELECT id FROM digital_deliveries WHERE order_id=?", orderID)
			stock := e.qInt("SELECT stock FROM products WHERE id=?", e.prodReg)
			var declined map[string]any
			if err := json.Unmarshal([]byte(yookassaRefetchJSON("card_declined", "canceled", "1849.08", false, orderID)), &declined); err != nil {
				t.Fatal(err)
			}
			declined["test"] = true
			declined["cancellation_details"] = map[string]string{"party": tc.party, "reason": tc.reason}
			raw, err := json.Marshal(declined)
			if err != nil {
				t.Fatal(err)
			}
			api.setBody(string(raw))

			before := e.tg.count()
			// Even a forged succeeded envelope cannot override a declined API state.
			for _, event := range []string{"payment.canceled", "payment.succeeded"} {
				rec := postYooKassaWebhook(t, e.bot, yookassaNotificationBody(event, "card_declined"))
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", event, rec.Code, rec.Body.String())
				}
			}
			e.bot.ProcessDigitalDeliveries(ctx)
			if got := e.qStr("SELECT status FROM orders WHERE id=?", orderID); got != storage.OrderStatusPending {
				t.Fatalf("decline changed status: %s", got)
			}
			if got := e.qStr("SELECT payment_state FROM orders WHERE id=?", orderID); got != storage.PaymentStatePending {
				t.Fatalf("decline blocked retry: %s", got)
			}
			if got := e.qInt("SELECT stock FROM products WHERE id=?", e.prodReg); got != stock {
				t.Fatalf("decline changed stock: %d", got)
			}
			if got := e.qInt("SELECT COUNT(*) FROM payment_attempts"); got != 0 {
				t.Fatalf("decline recorded successful attempt: %d", got)
			}
			if got := e.qInt("SELECT COUNT(*) FROM payment_anomalies"); got != 0 {
				t.Fatalf("normal card refusal quarantined order: %d", got)
			}
			if got := e.qInt("SELECT COUNT(*) FROM payment_events"); got != 0 {
				t.Fatalf("decline moved money: %d", got)
			}
			if got := e.tg.count() - before; got != 0 {
				t.Fatalf("decline sent buyer/admin messages: %s", dumpCalls(e.tg.since(before)))
			}
			if _, err := e.bot.archives.Owned(ctx, buyer, deliveryID); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("decline unlocked archive: %v", err)
			}
			if got := api.count(); got != 1 {
				t.Fatalf("authoritative reads = %d, want 1", got)
			}

			// A different successful payment can still settle the same order.
			var succeeded map[string]any
			if err := json.Unmarshal([]byte(yookassaRefetchJSON("card_retry", "succeeded", "1849.08", true, orderID)), &succeeded); err != nil {
				t.Fatal(err)
			}
			succeeded["test"] = true
			raw, err = json.Marshal(succeeded)
			if err != nil {
				t.Fatal(err)
			}
			api.setBody(string(raw))
			body := yookassaNotificationBody("payment.succeeded", "card_retry")
			if rec := postYooKassaWebhook(t, e.bot, body); rec.Code != http.StatusOK {
				t.Fatalf("retry: %d %s", rec.Code, rec.Body.String())
			}
			before = e.tg.count()
			e.bot.ProcessDigitalDeliveries(ctx)
			docs := documentCalls(e.tg.since(before))
			if len(docs) != 1 || docs[0].Params.Get("document") != "decline-retry-plane-zip" {
				t.Fatalf("successful retry delivery: %+v", docs)
			}
			if _, err := e.bot.archives.Owned(ctx, buyer, deliveryID); err != nil {
				t.Fatalf("paid archive not accessible: %v", err)
			}
			if got := e.qStr("SELECT payment_state FROM orders WHERE id=?", orderID); got != storage.PaymentStateSettled {
				t.Fatalf("retry not settled: %s", got)
			}

			// A late refusal for the first attempt and duplicate success must not
			// revoke an entitlement, charge twice, or redeliver the archive.
			before = e.tg.count()
			for _, notification := range []string{yookassaNotificationBody("payment.canceled", "card_declined"), body} {
				if rec := postYooKassaWebhook(t, e.bot, notification); rec.Code != http.StatusOK {
					t.Fatalf("replay: %d", rec.Code)
				}
			}
			e.bot.ProcessDigitalDeliveries(ctx)
			if got := e.qInt("SELECT COUNT(*) FROM payment_attempts"); got != 1 {
				t.Fatalf("retry/replay attempts: %d", got)
			}
			if got := e.qInt("SELECT COUNT(*) FROM orders"); got != 1 {
				t.Fatalf("retry duplicated order: %d", got)
			}
			if got := e.tg.count() - before; got != 0 {
				t.Fatalf("late notifications redelivered: %s", dumpCalls(e.tg.since(before)))
			}
			if _, err := e.bot.archives.Owned(ctx, buyer, deliveryID); err != nil {
				t.Fatalf("late cancellation revoked archive: %v", err)
			}
		})
	}
}
