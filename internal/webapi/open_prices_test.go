package webapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"shop_bot/internal/service"
	"shop_bot/internal/shop"
	"shop_bot/internal/storage"
	"sync"
	"testing"
)

func realOpenPriceFixture(t *testing.T) (*fixture, *storage.DB, int64, int64) {
	t.Helper()
	f := newFixture(t)
	ctx := context.Background()
	db, err := storage.New(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	users := storage.NewUserStore(db.Conn())
	if err := users.Upsert(ctx, &storage.User{TelegramID: 42, FirstName: "Ann"}); err != nil {
		t.Fatal(err)
	}
	products := storage.NewSQLProductStore(db)
	category, err := products.CreateCategory(ctx, &storage.Category{Name: "Planes", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	zero := float64(0)
	id, err := products.CreateProduct(ctx, &storage.Product{CategoryID: category, Name: "Open plane", PriceRUB: &zero, OpenPrice: true, IsDigital: true, IsActive: true, Stock: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.NewDigitalArchiveStore(db).Add(ctx, id, "api-plane-zip", "plane.zip", 80_000_000); err != nil {
		t.Fatal(err)
	}
	fixedPrice := float64(200)
	fixedID, err := products.CreateProduct(ctx, &storage.Product{CategoryID: category, Name: "Fixed item", PriceRUB: &fixedPrice, IsActive: true, Stock: 2})
	if err != nil {
		t.Fatal(err)
	}
	cartStore := storage.NewSQLCartStore(db)
	exchange := service.NewExchangeService(50, 85.7116, 5)
	f.server.deps.Cart = shop.NewCartService(cartStore, products, exchange)
	f.server.deps.Orders = shop.NewOrderService(storage.NewSQLOrderStore(db), cartStore, products, shop.PaymentDeps{}, slog.Default(), exchange)
	f.server.deps.Catalog = shop.NewCatalogService(products, exchange)
	f.server.deps.Users = users
	return f, db, id, fixedID
}

func TestFreeOrderConcurrentGrantAndStock(t *testing.T) {
	_, db, _, fixedID := realOpenPriceFixture(t)
	ctx := context.Background()
	if err := storage.NewUserStore(db.Conn()).Upsert(ctx, &storage.User{TelegramID: 43, FirstName: "Second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn().Exec("UPDATE products SET price_rub=0,price_usd=0,stock=1 WHERE id=?", fixedID); err != nil {
		t.Fatal(err)
	}
	orders := storage.NewSQLOrderStore(db)
	create := func(user int64) int64 {
		id, err := orders.CreateOrder(ctx, &storage.Order{UserID: user, Status: storage.OrderStatusPending}, []storage.OrderItem{{ProductID: fixedID, ProductName: "Free physical item", Quantity: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, second := create(42), create(43)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs <- orders.ConfirmFreeOrder(ctx, first, 42) }()
	go func() { defer wg.Done(); errs <- orders.ConfirmFreeOrder(ctx, second, 43) }()
	wg.Wait()
	close(errs)
	succeeded, stockDenied := 0, 0
	for err := range errs {
		if err == nil {
			succeeded++
		} else if errors.Is(err, storage.ErrProductOutOfStock) {
			stockDenied++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || stockDenied != 1 {
		t.Fatalf("last item claimed: success=%d denied=%d", succeeded, stockDenied)
	}
	var stock, grants int
	if err := db.Conn().QueryRow("SELECT stock FROM products WHERE id=?", fixedID).Scan(&stock); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM free_order_grants").Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if stock != 0 || grants != 1 {
		t.Fatalf("stock=%d grants=%d", stock, grants)
	}
	var grantedID, owner int64
	if err := db.Conn().QueryRow("SELECT o.id,o.user_id FROM orders o JOIN free_order_grants g ON g.order_id=o.id").Scan(&grantedID, &owner); err != nil {
		t.Fatal(err)
	}
	if err := orders.ConfirmFreeOrder(ctx, grantedID, owner); !errors.Is(err, storage.ErrOrderStatusConflict) {
		t.Fatalf("duplicate free grant: %v", err)
	}
}

func TestOpenPriceMiniAppFreeCheckout(t *testing.T) {
	f, db, id, _ := realOpenPriceFixture(t)
	response := f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, id), true)
	cart := decodeJSON(t, response)
	if response.Code != http.StatusOK || cart["free_checkout"] != true || cart["total_rub"] != float64(0) {
		t.Fatalf("cart: %d %s", response.Code, response.Body.String())
	}
	response = f.request(t, http.MethodPost, "/api/checkout", `{"method":"free"}`, true)
	body := decodeJSON(t, response)
	if response.Code != http.StatusOK || body["free"] != true {
		t.Fatalf("free checkout: %d %s", response.Code, response.Body.String())
	}
	orderID := int64(body["order_id"].(float64))
	var grants, receipts int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM free_order_grants WHERE order_id=?", orderID).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM payment_attempts WHERE order_id=?", orderID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if grants != 1 || receipts != 0 || f.tg.endpoint != "" {
		t.Fatal("free checkout sent an invoice or fabricated a payment")
	}
	archives := storage.NewDigitalArchiveStore(db)
	claim, err := archives.Claim(context.Background())
	if err != nil || claim == nil || claim.FileID != "api-plane-zip" {
		t.Fatalf("free archive queued: %+v %v", claim, err)
	}
	if _, err := archives.Owned(context.Background(), 43, claim.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("foreign free download: %v", err)
	}
	if err := f.server.deps.Orders.(*shop.OrderService).ConfirmFreeOrder(context.Background(), orderID, 42); !errors.Is(err, storage.ErrOrderStatusConflict) {
		t.Fatalf("free replay: %v", err)
	}
}

func TestOpenPriceMiniAppWholeRublesAndMixedCart(t *testing.T) {
	f, db, id, fixedID := realOpenPriceFixture(t)
	for _, price := range []string{"-1", "1.5", "1000001", "\"100\""} {
		response := f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":%s}`, id, price), true)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid price %s: %d", price, response.Code)
		}
	}
	response := f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":125}`, id), true)
	cart := decodeJSON(t, response)
	if response.Code != http.StatusOK || cart["total_rub"] != float64(125) || cart["free_checkout"] != false {
		t.Fatalf("chosen price: %d %s", response.Code, response.Body.String())
	}
	response = f.request(t, http.MethodPost, "/api/checkout", `{"method":"free"}`, true)
	if response.Code != http.StatusBadRequest {
		t.Fatal("positive price skipped payment")
	}
	var count int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM orders").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("rejected free checkout created an order")
	}
	response = f.request(t, http.MethodPost, "/api/checkout", `{"method":"stars"}`, true)
	body := decodeJSON(t, response)
	if response.Code != http.StatusOK || body["invoice_link"] == nil {
		t.Fatalf("paid checkout: %d %s", response.Code, response.Body.String())
	}
	order, err := f.server.deps.Orders.GetOrder(context.Background(), int64(body["order_id"].(float64)))
	if err != nil {
		t.Fatal(err)
	}
	if order.TotalRUB != 125 || order.TotalStars != 72 || order.TotalUSD != 1.46 {
		t.Fatalf("frozen price: %+v", order)
	}
	response = f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":0}`, fixedID), true)
	if response.Code != http.StatusBadRequest {
		t.Fatal("fixed-price product changed to free")
	}
	f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":0}`, id), true)
	response = f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, fixedID), true)
	cart = decodeJSON(t, response)
	if cart["free_checkout"] != false || cart["total_rub"] != float64(200) {
		t.Fatal("mixed cart is free")
	}
	response = f.request(t, http.MethodPost, "/api/checkout", `{"method":"free"}`, true)
	if response.Code != http.StatusBadRequest {
		t.Fatal("mixed cart skipped payment")
	}
}
