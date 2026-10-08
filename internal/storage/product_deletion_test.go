package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestProductDeletionPreservesOrdersAndClearsAllCarts(t *testing.T) {
	ps, db := newTestProductStore(t)
	defer db.Close()
	ctx := context.Background()
	for _, id := range []int64{42, 43, 99} {
		if err := NewUserStore(db.Conn()).Upsert(ctx, &User{TelegramID: id, FirstName: "Buyer"}); err != nil {
			t.Fatal(err)
		}
	}
	cat := seedCategory(t, db, "Planes", "")
	product, err := ps.CreateProduct(ctx, &Product{CategoryID: cat, Name: "Plane", IsActive: true, IsDigital: true, OpenPrice: true, Stock: 1})
	if err != nil {
		t.Fatal(err)
	}
	other, err := ps.CreateProduct(ctx, &Product{CategoryID: cat, Name: "Other", IsActive: true, Stock: 5, PriceUSD: 1})
	if err != nil {
		t.Fatal(err)
	}
	archives := NewDigitalArchiveStore(db)
	if _, err := archives.Add(ctx, product, "zip-file", "plane.zip", 80_000_000); err != nil {
		t.Fatal(err)
	}
	os := NewSQLOrderStore(db)
	create := func(amount float64, stars int) int64 {
		id, err := os.CreateOrder(ctx, &Order{UserID: 42, Status: OrderStatusPending, TotalRUB: amount, TotalStars: stars, TotalUSD: amount / 100}, []OrderItem{{ProductID: product, ProductName: "Original plane", Quantity: 1, PriceUSD: amount / 100}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	free, paid, pending := create(0, 0), create(100, 50), create(200, 100)
	if err := os.ConfirmFreeOrder(ctx, free, 42); err != nil {
		t.Fatal(err)
	}
	if err := os.UpdateOrderStatus(ctx, paid, OrderStatusPending, OrderStatusPaid, PaymentMethodStars, "capture-before"); err != nil {
		t.Fatal(err)
	}
	carts := NewSQLCartStore(db)
	for _, user := range []int64{42, 43, 99} {
		if err := carts.SetPrice(ctx, user, product, 100); err != nil {
			t.Fatal(err)
		}
	}
	if err := carts.AddItem(ctx, 42, other); err != nil {
		t.Fatal(err)
	}
	if err := carts.BeginPriceInput(ctx, 42, 42, product); err != nil {
		t.Fatal(err)
	}
	if err := archives.BeginUpload(ctx, 99, 99, product); err != nil {
		t.Fatal(err)
	}
	wish := NewWishlistStore(db.Conn())
	wishlistUser, err := NewUserStore(db.Conn()).GetByTelegramID(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := wish.Add(ctx, wishlistUser.ID, product, 1, 1); err != nil {
		t.Fatal(err)
	}
	promos := NewSQLPromoStore(db)
	if _, err := promos.CreatePromo(ctx, &PromoCode{Code: "PLANE", Discount: 10, ProductIDs: []int64{product}}); err != nil {
		t.Fatal(err)
	}
	original, err := ps.GetProduct(ctx, product)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.DeleteProduct(ctx, product); err != nil {
		t.Fatal("orders blocked deletion", err)
	}
	for _, table := range []string{"cart_items", "wishlist", "open_price_inputs", "digital_uploads"} {
		var n int
		if err := db.Conn().QueryRow("SELECT COUNT(*) FROM "+table+" WHERE product_id=?", product).Scan(&n); err != nil || n != 0 {
			t.Fatal("user state survived deletion", table, n, err)
		}
	}
	for _, id := range []int64{free, paid, pending} {
		o, err := os.GetOrder(ctx, id)
		if err != nil || len(o.Items) != 1 || o.Items[0].ProductName != "Original plane" {
			t.Fatal("history changed", id, o, err)
		}
	}
	if err := os.UpdateOrderStatus(ctx, pending, OrderStatusPending, OrderStatusPaid, PaymentMethodStars, "capture-after"); err != nil {
		t.Fatal("existing invoice cannot settle", err)
	}
	for _, id := range []int64{free, paid, pending} {
		files, err := archives.ForOrder(ctx, 42, id)
		if err != nil || len(files) != 1 || files[0].FileID != "zip-file" {
			t.Fatal("old file entitlement lost", id, files, err)
		}
	}
	if err := archives.RequestOrderDownload(ctx, 42, paid, product); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.GetProduct(ctx, product); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted product visible", err)
	}
	if err := ps.UpdateProduct(ctx, original); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted product resurrected", err)
	}
	if err := carts.AddItem(ctx, 43, product); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale add resurrected deleted cart item", err)
	}
	if err := carts.SetPrice(ctx, 43, product, 200); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale price resurrected cart item", err)
	}
	if err := carts.BeginPriceInput(ctx, 43, 43, product); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted input re-created", err)
	}
	if err := archives.BeginUpload(ctx, 99, 99, product); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted upload re-created", err)
	}
	if _, err := archives.Add(ctx, product, "new-file", "new.zip", 100); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted archive modified", err)
	}
	if _, err := os.CreateOrder(ctx, &Order{UserID: 42, Status: OrderStatusPending}, []OrderItem{{ProductID: product, ProductName: "Stale", Quantity: 1}}); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale checkout created order", err)
	}
	items, err := carts.GetItems(ctx, 42)
	if err != nil || len(items) != 1 || items[0].ProductID != other {
		t.Fatal("unrelated cart item removed", items, err)
	}
	products, total, err := ps.ListProductsAdmin(ctx, 20, 0)
	if err != nil || total != 1 || len(products) != 1 || products[0].ID != other {
		t.Fatal("deleted inventory listed", products, total, err)
	}
	for _, query := range []string{"Plane", ""} {
		products, err := ps.SearchProducts(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range products {
			if p.ID == product {
				t.Fatal("deleted item found in search")
			}
		}
	}
	for _, paged := range []bool{false, true} {
		var listed []Product
		var err error
		if paged {
			listed, _, err = ps.GetProductsByCategoryPaged(ctx, cat, 20, 0)
		} else {
			listed, err = ps.GetProductsByCategory(ctx, cat)
		}
		if err != nil || len(listed) != 1 || listed[0].ID != other {
			t.Fatal("deleted catalog item listed", listed, err)
		}
	}
	legacyCatalog := NewCatalogStore(db.Conn())
	if p, err := legacyCatalog.GetProductByID(ctx, product); err != nil || p != nil {
		t.Fatal("alternate catalog exposes deleted product", p, err)
	}
	if listed, err := legacyCatalog.GetProductsByCategory(ctx, cat, 20, 0); err != nil || len(listed) != 1 || listed[0].ID != other {
		t.Fatal("alternate catalog lists deleted product", listed, err)
	}
	if listed, err := legacyCatalog.SearchProducts(ctx, "Plane", 20, 0); err != nil || len(listed) != 0 {
		t.Fatal("alternate search lists deleted product", listed, err)
	}
	p, err := promos.GetPromoByCode(ctx, "PLANE")
	if err != nil || len(p.ProductIDs) != 1 || p.ProductIDs[0] != product {
		t.Fatal("deleted product broadened promo", p, err)
	}
	if err := ps.DeleteProduct(ctx, product); err != nil {
		t.Fatal("repeat deletion", err)
	}
	var n int
	db.Conn().QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&n)
	if n != 3 {
		t.Fatal("failed stale checkout left order")
	}
	rows, err := db.Conn().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign keys broken")
	}
}
func TestProductDeletionRollbackAndMigration(t *testing.T) {
	db := migrationDBBefore(t, "030_product_deletion.sql")
	if _, err := db.Conn().Exec(`INSERT INTO categories(id,name) VALUES(1,'Cat');INSERT INTO products(id,category_id,name,price_usd,stock,is_active) VALUES(1,1,'Legacy',1,5,1);INSERT INTO cart_items(user_id,product_id) VALUES(42,1)`); err != nil {
		t.Fatal(err)
	}
	applyMigrationsFrom(t, db, "030_product_deletion.sql")
	ps := NewSQLProductStore(db)
	ctx := context.Background()
	if _, err := ps.GetProduct(ctx, 1); err != nil {
		t.Fatal("migration hid existing product", err)
	}
	if _, err := db.Conn().Exec(`CREATE TRIGGER fail_cart_delete BEFORE DELETE ON cart_items BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	if err := ps.DeleteProduct(ctx, 1); err == nil {
		t.Fatal("failed cleanup claimed success")
	}
	if _, err := ps.GetProduct(ctx, 1); err != nil {
		t.Fatal("failed cleanup partially marked deleted", err)
	}
	var n int
	db.Conn().QueryRow(`SELECT COUNT(*) FROM cart_items WHERE product_id=1`).Scan(&n)
	if n != 1 {
		t.Fatal("failed cleanup lost cart")
	}
	if _, err := db.Conn().Exec(`DROP TRIGGER fail_cart_delete`); err != nil {
		t.Fatal(err)
	}
	if err := ps.DeleteProduct(ctx, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := NewSQLCartStore(db).AddItem(ctx, 42, 1); !errors.Is(err, ErrNotFound) {
			t.Fatal(fmt.Sprint("re-add deleted item", err))
		}
	}
}
