package service

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"shop_bot/internal/storage"
	"sync"
	"testing"
)

func TestPersistentRUBRateRestartValidationAndConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shop.db")
	db, err := storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := NewPersistentExchangeService(ctx, storage.NewSQLRUBRateStore(db.Conn()), 50, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ex.GetUSDToRUBRate() != 100 {
		t.Fatal("env fallback lost")
	}
	snap := ex.Snapshot()
	if err := ex.UpdateRUBRate(ctx, 92.5); err != nil {
		t.Fatal(err)
	}
	if snap.GetUSDToRUBRate() != 100 || ex.GetUSDToRUBRate() != 92.5 {
		t.Fatal("snapshot changed")
	}
	for _, v := range []float64{0, -1, 0.5, 1000001, math.NaN(), math.Inf(1)} {
		if err := ex.UpdateRUBRate(ctx, v); !errors.Is(err, ErrInvalidRUBRate) {
			t.Fatalf("invalid rate %v: %v", v, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := ex.UpdateRUBRate(cancelled, 200); err == nil || ex.GetUSDToRUBRate() != 92.5 {
		t.Fatal("failed save changed active rate")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(rate float64) {
			defer wg.Done()
			if err := ex.UpdateRUBRate(ctx, rate); err != nil {
				t.Error(err)
			}
			_ = ex.ConvertRUBToUSD(100)
		}(100 + float64(i))
	}
	wg.Wait()
	saved, err := storage.NewSQLRUBRateStore(db.Conn()).GetRUBRate(ctx)
	if err != nil || saved == nil || *saved != ex.GetUSDToRUBRate() {
		t.Fatal("database and active rate diverged")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restarted, err := NewPersistentExchangeService(ctx, storage.NewSQLRUBRateStore(db.Conn()), 50, 999, 0)
	if err != nil || restarted.GetUSDToRUBRate() != *saved {
		t.Fatalf("restart reset override: %v", err)
	}
}
