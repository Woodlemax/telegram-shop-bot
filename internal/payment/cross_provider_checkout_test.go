package payment

import (
	"context"
	"errors"
	"shop_bot/internal/storage"
	"strconv"
	"sync"
	"testing"
)

func TestYooKassaBlocksStarsInvoiceAndAlreadyOpenedStarsPreCheckout(t *testing.T) {
	db, id, api, client := newCheckoutFixture(t)
	if _, err := db.Conn().Exec(`UPDATE orders SET total_stars=100 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	requireCheckout(t, client(), id)
	stars, capture := newTestStarsPayment(t, storage.NewSQLOrderStore(db))
	if err := stars.SendInvoice(42, id, 100, nil, 0); !errors.Is(err, storage.ErrCheckoutProviderConflict) {
		t.Fatalf("second rail invoice: %v", err)
	}
	if err := stars.HandlePreCheckout(context.Background(), preCheckoutQuery(strconv.FormatInt(id, 10), 42, 100)); err != nil {
		t.Fatal(err)
	}
	assertPreCheckoutAnswer(t, capture, false, PreCheckoutKeyPaymentMethodConflict)
	if api.count() != 1 {
		t.Fatalf("card creations: %d", api.count())
	}
}

func TestStarsApprovalBlocksYooKassaBeforeItsAPI(t *testing.T) {
	db, id, api, client := newCheckoutFixture(t)
	if _, err := db.Conn().Exec(`UPDATE orders SET total_stars=100 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	stars, capture := newTestStarsPayment(t, storage.NewSQLOrderStore(db))
	if err := stars.HandlePreCheckout(context.Background(), preCheckoutQuery(strconv.FormatInt(id, 10), 42, 100)); err != nil {
		t.Fatal(err)
	}
	assertPreCheckoutAnswer(t, capture, true, "")
	if _, err := client().CreatePayment(context.Background(), id, 10001, "Plane"); !errors.Is(err, storage.ErrCheckoutProviderConflict) {
		t.Fatalf("card after Stars approval: %v", err)
	}
	if api.count() != 0 {
		t.Fatal("second rail contacted YooKassa")
	}
}

func TestConcurrentYooKassaCreationAndStarsPreCheckoutOnlyOneRailWins(t *testing.T) {
	for i := 0; i < 12; i++ {
		db, id, api, client := newCheckoutFixture(t)
		if _, err := db.Conn().Exec(`UPDATE orders SET total_stars=100 WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		stars, _ := newTestStarsPayment(t, storage.NewSQLOrderStore(db))
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var cardErr error
		var starsKey string
		go func() {
			defer wg.Done()
			<-start
			_, cardErr = client().CreatePayment(context.Background(), id, 10001, "Plane")
		}()
		go func() {
			defer wg.Done()
			<-start
			starsKey = stars.validatePreCheckout(context.Background(), preCheckoutQuery(strconv.FormatInt(id, 10), 42, 100))
		}()
		close(start)
		wg.Wait()
		switch {
		case cardErr == nil:
			if starsKey != PreCheckoutKeyPaymentMethodConflict || api.count() != 1 {
				t.Fatalf("both rails accepted: stars=%q cards=%d", starsKey, api.count())
			}
		case errors.Is(cardErr, storage.ErrCheckoutProviderConflict):
			if starsKey != "" || api.count() != 0 {
				t.Fatalf("neither rail accepted: stars=%q cards=%d", starsKey, api.count())
			}
		default:
			t.Fatalf("unexpected card error: %v", cardErr)
		}
	}
}

func TestLostYooKassaCreationResponseKeepsStarsBlocked(t *testing.T) {
	db, id, api, client := newCheckoutFixture(t)
	api.failCreateOnce = true
	if _, err := client().CreatePayment(context.Background(), id, 10001, "Plane"); err == nil {
		t.Fatal("expected lost API response")
	}
	if err := storage.NewSQLOrderStore(db).ClaimCheckoutProvider(context.Background(), id, storage.PaymentMethodStars); !errors.Is(err, storage.ErrCheckoutProviderConflict) {
		t.Fatalf("uncertain card payment allowed Stars: %v", err)
	}
	requireCheckout(t, client(), id)
	if api.count() != 1 {
		t.Fatal("lost response created extra charge")
	}
}
