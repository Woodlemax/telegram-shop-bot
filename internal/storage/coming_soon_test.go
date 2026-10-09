package storage

import (
	"context"
	"errors"
	"testing"
)

func TestComingSoonVisibilityPersistenceAndUnavailableCheckout(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	products := NewSQLProductStore(db)
	cat, err := products.CreateCategory(ctx, &Category{Name: "Planes", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string, soon, active bool) int64 {
		t.Helper()
		id, err := products.CreateProduct(ctx, &Product{CategoryID: cat, Name: name, PriceUSD: 1, Stock: 0, IsActive: active, ComingSoon: soon})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	id := create("Soon plane", true, true)
	create("Hidden zero plane", false, true)
	create("Inactive plane", true, false)
	list, total, err := products.GetProductsByCategoryPaged(ctx, cat, 10, 0)
	if err != nil || total != 1 || len(list) != 1 || list[0].ID != id || !list[0].ComingSoon {
		t.Fatalf("catalog: %v %d %v", list, total, err)
	}
	found, err := products.SearchProducts(ctx, "plane")
	if err != nil || len(found) != 1 || found[0].ID != id {
		t.Fatalf("search: %v %v", found, err)
	}
	orderStore := NewSQLOrderStore(db)
	if _, err := orderStore.CreateOrder(ctx, &Order{UserID: 42, Status: OrderStatusPending}, []OrderItem{{ProductID: id, Quantity: 1}}); !errors.Is(err, ErrProductOutOfStock) {
		t.Fatalf("preview ordered: %v", err)
	}
	var orders int
	db.Conn().QueryRow(`SELECT count(*) FROM orders`).Scan(&orders)
	if orders != 0 {
		t.Fatal("blocked checkout left order")
	}
	item, err := products.GetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	item.Stock = 2
	if err = products.UpdateProduct(ctx, item); err != nil {
		t.Fatal(err)
	}
	item, err = products.GetProduct(ctx, id)
	if err != nil || !item.ComingSoon || item.IsComingSoon() || !item.CanPurchase() {
		t.Fatalf("restock: %v %v", item, err)
	}
}

func TestComingSoonOverridesInfiniteStockUntilRestocked(t *testing.T) {
	for _, infinite := range []bool{false, true} {
		item := Product{IsActive: true, ComingSoon: true, Stock: 0, InfiniteStock: infinite}
		if item.CanPurchase() || !item.IsComingSoon() {
			t.Fatal("coming soon product can be bought")
		}
		item.Stock = 1
		if !item.CanPurchase() || item.IsComingSoon() {
			t.Fatal("restocked product stays blocked")
		}
		item.Stock = 0
		item.ComingSoon = false
		if item.CanPurchase() != infinite {
			t.Fatal("normal stock semantics changed")
		}
	}
}
