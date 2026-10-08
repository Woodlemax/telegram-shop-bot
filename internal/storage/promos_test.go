package storage

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPromoStoreValidationDuplicateAndExpiry(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	store := NewSQLPromoStore(db)
	for _, discount := range []int{-1, 0, 101} {
		if _, err := store.CreatePromo(ctx, &PromoCode{Code: "INVALID", Discount: discount}); !errors.Is(err, ErrInvalidPromo) {
			t.Fatal("invalid discount accepted", discount)
		}
	}
	future := time.Now().In(time.FixedZone("offset", 14*3600)).Add(time.Hour)
	id, err := store.CreatePromo(ctx, &PromoCode{Code: " save10 ", Discount: 10, ExpiresAt: &future})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPromoByCode(ctx, "SAVE10"); err != nil {
		t.Fatal("valid future expiry was rejected", err)
	}
	if _, err := store.CreatePromo(ctx, &PromoCode{Code: "save10", Discount: 99}); !errors.Is(err, ErrPromoExists) {
		t.Fatal("duplicate not reported", err)
	}
	promo, err := store.GetPromoByCode(ctx, "SAVE10")
	if err != nil || promo.ID != id || promo.Discount != 10 {
		t.Fatal("duplicate changed original", err)
	}
	past := time.Now().In(time.FixedZone("offset", -12*3600)).Add(-time.Hour)
	_, err = store.CreatePromo(ctx, &PromoCode{Code: "EXPIRED", Discount: 10, ExpiresAt: &past})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPromoByCode(ctx, "EXPIRED"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired code usable", err)
	}
	if err := store.CreatePersonal(ctx, "PERSONAL", 10, 42, 0); !errors.Is(err, ErrInvalidPromo) {
		t.Fatal("personal invalid expiry accepted", err)
	}
	if err := store.CreatePersonal(ctx, "PERSONAL", 10, 42, 1); err != nil {
		t.Fatal(err)
	}
	p, err := store.GetPromoByCode(ctx, "PERSONAL")
	if err != nil || p.BoundUserID == nil || *p.BoundUserID != 42 || p.MaxUses != 1 {
		t.Fatal("personal constraints lost", err)
	}
}
func TestPromoConcurrentCreationDoesNotOverwrite(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewSQLPromoStore(db)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, discount := range []int{10, 20} {
		wg.Add(1)
		go func(d int) {
			defer wg.Done()
			_, err := store.CreatePromo(ctx, &PromoCode{Code: "SAME", Discount: d})
			results <- err
		}(discount)
	}
	wg.Wait()
	close(results)
	success, duplicates := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrPromoExists) {
			duplicates++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || duplicates != 1 {
		t.Fatal("non-unique creation", success, duplicates)
	}
}

func TestPromoLegacyTextExpiryAndList(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	store := NewSQLPromoStore(db)
	future := time.Now().In(time.FixedZone("arbitrary", 4*3600)).Add(time.Hour)
	if _, err := db.Conn().Exec(`INSERT INTO promo_codes(code,discount,max_uses,expires_at) VALUES('LEGACY',10,0,?)`, future.String()); err != nil {
		t.Fatal(err)
	}
	promo, err := store.GetPromoByCode(ctx, "legacy")
	if err != nil || promo.ExpiresAt == nil || !promo.ExpiresAt.Equal(future) {
		t.Fatal("legacy expiry not preserved", err)
	}
	list, err := store.ListPromos(ctx)
	if err != nil || len(list) != 1 || list[0].ExpiresAt == nil || !list[0].ExpiresAt.Equal(future) {
		t.Fatal("legacy expiry missing in list", err)
	}
}
