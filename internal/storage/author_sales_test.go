package storage

import (
	"context"
	"errors"
	"testing"
)

func TestAuthorSaleVisibilityCartRemovalAndCheckoutGuard(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	products := NewSQLProductStore(db)
	carts := NewSQLCartStore(db)
	cat, err := products.CreateCategory(ctx, &Category{Name: "Planes", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	rub := float64(1500)
	id, err := products.CreateProduct(ctx, &Product{CategoryID: cat, Name: "Author plane", PriceRUB: &rub, Stock: 5, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []int64{41, 42} {
		if err = NewUserStore(db.Conn()).Upsert(ctx, &User{TelegramID: user, FirstName: "Buyer"}); err != nil {
			t.Fatal(err)
		}
		if err = carts.AddItem(ctx, user, id); err != nil {
			t.Fatal(err)
		}
		if err = carts.BeginPriceInput(ctx, user, user, id); err != nil {
			t.Fatal(err)
		}
	}
	orders := NewSQLOrderStore(db)
	oldOrder, err := orders.CreateOrder(ctx, &Order{UserID: 42, Status: OrderStatusPending, TotalUSD: 15}, []OrderItem{{ProductID: id, Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := products.GetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.AuthorTelegramURL = "@plane_author"
	p.Stock = 0
	p.ComingSoon = true
	if err = products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	p, err = products.GetProduct(ctx, id)
	if err != nil || p.AuthorTelegramURL != "https://t.me/plane_author" || p.CanPurchase() || p.IsComingSoon() {
		t.Fatalf("author mode: %+v %v", p, err)
	}
	for _, user := range []int64{41, 42} {
		items, err := carts.GetItems(ctx, user)
		if err != nil || len(items) != 0 {
			t.Fatalf("cart not cleared: %v %v", items, err)
		}
		input, err := carts.PendingPriceInput(ctx, user, user)
		if err != nil || input != 0 {
			t.Fatalf("price input retained: %d %v", input, err)
		}
		if err = carts.AddItem(ctx, user, id); err == nil {
			t.Fatal("author entered cart")
		}
		if err = carts.SetPrice(ctx, user, id, 0); err == nil {
			t.Fatal("author entered open-price cart")
		}
	}
	if _, err = db.Conn().Exec(`INSERT INTO cart_items(user_id,product_id,quantity) VALUES(42,?,1)`, id); err == nil {
		t.Fatal("raw cart bypass")
	}
	if _, err = orders.CreateOrder(ctx, &Order{UserID: 42, Status: OrderStatusPending}, []OrderItem{{ProductID: id, Quantity: 1}}); !errors.Is(err, ErrProductOutOfStock) {
		t.Fatalf("stale order bypass: %v", err)
	}
	if _, err = orders.GetOrder(ctx, oldOrder); err != nil {
		t.Fatal("previous order lost", err)
	}
	var count int
	if err = db.Conn().QueryRow(`SELECT count(*) FROM orders`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rejected order left data: %d %v", count, err)
	}
	list, total, err := products.GetProductsByCategoryPaged(ctx, cat, 10, 0)
	if err != nil || total != 1 || len(list) != 1 || !list[0].IsAuthorSale() {
		t.Fatalf("zero-stock author hidden: %v %d %v", list, total, err)
	}
	search, err := products.SearchProducts(ctx, "Author")
	if err != nil || len(search) != 1 || !search[0].IsAuthorSale() {
		t.Fatalf("author search: %v %v", search, err)
	}
	groups := NewProductGroupStore(db.Conn())
	child, err := products.CreateProduct(ctx, &Product{CategoryID: cat, Name: "Author modification", PriceRUB: &rub, AuthorTelegramURL: "@mod_author", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = groups.SetParent(ctx, child, id); err != nil {
		t.Fatal(err)
	}
	roots, total, err := groups.ListRoots(ctx, cat, 10, 0)
	if err != nil || total != 1 || len(roots) != 1 || roots[0].ModificationCount != 1 {
		t.Fatalf("author groups: %v %d %v", roots, total, err)
	}
	mods, total, err := groups.ListModifications(ctx, id, 10, 0)
	if err != nil || total != 1 || len(mods) != 1 || mods[0] != child {
		t.Fatalf("author modifications: %v %d %v", mods, total, err)
	}
	p.AuthorTelegramURL = ""
	p.ComingSoon = false
	p.Stock = 1
	if err = products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err = carts.AddItem(ctx, 42, id); err != nil {
		t.Fatal("author mode could not be disabled", err)
	}
}

func TestAuthorSaleValidation(t *testing.T) {
	for _, raw := range []string{"https://evil.test/author", "https://t.me/+invite", "https://t.me/joinchat/invite", "https://t.me/author/123", "https://t.me/author?start=x", "javascript:alert(1)"} {
		if _, err := NormalizeAuthorTelegramURL(raw); err == nil {
			t.Fatalf("unsafe author accepted %q", raw)
		}
	}
	price := float64(100)
	for _, p := range []Product{
		{AuthorTelegramURL: "@author"},
		{AuthorTelegramURL: "@author", PriceRUB: &price, OpenPrice: true},
		{AuthorTelegramURL: "@author", PriceRUB: &price, SubPeriodDays: 30},
	} {
		if err := validateProduct(&p); !errors.Is(err, ErrInvalidAuthorSale) {
			t.Fatalf("invalid author settings: %v", err)
		}
	}
	p := Product{AuthorTelegramURL: "@author", PriceRUB: &price, PriceStars: 500, PriceUSD: 10}
	if err := validateProduct(&p); err != nil || p.PriceUSD != 0 || p.PriceStars != 0 {
		t.Fatalf("not RUB-only: %+v %v", p, err)
	}
}
