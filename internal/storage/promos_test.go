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

func TestPromoProductScopeUpgradePersistenceAndExpiredList(t *testing.T) {
	db := migrationDBBefore(t, "029_promo_product_ids.sql")
	if _, err := db.Conn().Exec(`INSERT INTO categories(id,name) VALUES(2,'Planes'); INSERT INTO promo_codes(code,discount,max_uses,category_id) VALUES('ALL10',10,0,NULL),('CATEGORY10',10,0,2)`); err != nil {
		t.Fatal(err)
	}
	applyMigrationsFrom(t, db, "029_promo_product_ids.sql")
	ctx := context.Background()
	store := NewSQLPromoStore(db)
	for _, code := range []string{"ALL10", "CATEGORY10"} {
		p, err := store.GetPromoByCode(ctx, code)
		if err != nil || len(p.ProductIDs) != 0 {
			t.Fatal("legacy scope changed", p, err)
		}
		if code == "CATEGORY10" && (p.CategoryID == nil || *p.CategoryID != 2) {
			t.Fatal("category scope lost")
		}
	}
	id, err := store.CreatePromo(ctx, &PromoCode{Code: "PRODUCT10", Discount: 10, ProductIDs: []int64{2, 5}})
	if err != nil {
		t.Fatal(err)
	}
	// No product rows exist: a removed product must not turn the code global.
	for _, s := range []*SQLPromoStore{store, NewSQLPromoStore(db)} {
		p, err := s.GetPromoByCode(ctx, "PRODUCT10")
		if err != nil || len(p.ProductIDs) != 2 || p.ProductIDs[0] != 2 || p.ProductIDs[1] != 5 {
			t.Fatal("scope not persisted", p, err)
		}
		if PromoMatchesProduct(p, &Product{ID: 3}) || !PromoMatchesProduct(p, &Product{ID: 5}) {
			t.Fatal("removed product became global")
		}
	}
	past := time.Now().Add(-time.Hour)
	expired, err := store.CreatePromo(ctx, &PromoCode{Code: "EXPIRED", Discount: 10, ExpiresAt: &past})
	if err != nil {
		t.Fatal(err)
	}
	list, err := store.ListPromos(ctx)
	if err != nil || len(list) != 4 {
		t.Fatal("expired promo broke admin listing", len(list), err)
	}
	found := false
	for _, p := range list {
		if p.ID == id {
			found = len(p.ProductIDs) == 2 && p.ProductIDs[1] == 5
		}
	}
	if !found {
		t.Fatal("list omitted products")
	}
	if err := store.DeactivatePromo(ctx, expired); err != nil {
		t.Fatal("expired promo cannot be removed", err)
	}
	category := int64(1)
	for _, p := range []*PromoCode{
		{Code: "BAD", Discount: 10, ProductIDs: []int64{0}},
		{Code: "BAD", Discount: 10, ProductIDs: []int64{-1}},
		{Code: "BAD", Discount: 10, ProductIDs: []int64{2, 2}},
		{Code: "BAD", Discount: 10, ProductIDs: make([]int64, 101)},
		{Code: "BAD", Discount: 10, ProductIDs: []int64{2}, CategoryID: &category},
	} {
		if _, err := store.CreatePromo(ctx, p); !errors.Is(err, ErrInvalidPromo) {
			t.Fatal("invalid scope accepted", p, err)
		}
	}
	if _, err := db.Conn().Exec(`UPDATE promo_codes SET product_ids='["bad"]' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPromoByCode(ctx, "PRODUCT10"); err == nil {
		t.Fatal("corrupt scope became global")
	}
	if _, err := db.Conn().Exec(`UPDATE promo_codes SET product_ids='null' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPromoByCode(ctx, "PRODUCT10"); err == nil {
		t.Fatal("null scope became global")
	}

}
