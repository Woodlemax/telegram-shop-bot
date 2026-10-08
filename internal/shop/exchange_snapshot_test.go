package shop

import (
	"context"
	"shop_bot/internal/service"
	"shop_bot/internal/storage"
	"testing"
)

type rateChangingProducts struct {
	*mockProductStore
	exchange *service.ExchangeService
	reads    int
}

func (p *rateChangingProducts) GetProduct(ctx context.Context, id int64) (*storage.Product, error) {
	p.reads++
	if p.reads == 2 {
		if err := p.exchange.UpdateRUBRate(ctx, 200); err != nil {
			return nil, err
		}
	}
	return p.mockProductStore.GetProduct(ctx, id)
}
func TestCartUsesOneRateSnapshotDuringAdminChange(t *testing.T) {
	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ex, err := service.NewPersistentExchangeService(context.Background(), storage.NewSQLRUBRateStore(db.Conn()), 50, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	rub := float64(100)
	products := &rateChangingProducts{mockProductStore: &mockProductStore{byID: map[int64]*storage.Product{1: {ID: 1, PriceRUB: &rub}, 2: {ID: 2, PriceRUB: &rub}}}, exchange: ex}
	cart := NewCartService(&mockCartStore{items: []storage.CartItem{{ProductID: 1, Quantity: 1}, {ProductID: 2, Quantity: 1}}}, products, ex)
	before, err := cart.Get(context.Background(), 42)
	if err != nil || before.TotalUSD != 2 || before.TotalStars != 100 || before.TotalRUB != 200 {
		t.Fatalf("mixed rate inside cart: %+v %v", before, err)
	}
	after, err := cart.Get(context.Background(), 42)
	if err != nil || after.TotalUSD != 1 || after.TotalStars != 50 || after.TotalRUB != 200 {
		t.Fatalf("new rate not applied: %+v %v", after, err)
	}
}
