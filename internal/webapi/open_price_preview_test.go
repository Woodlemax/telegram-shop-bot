package webapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"shop_bot/internal/service"
	"shop_bot/internal/shop"
	"shop_bot/internal/storage"
	"testing"
)

func TestOpenPricePreviewRatesAndSavedCart(t *testing.T) {
	f, db, id, fixed := realOpenPriceFixture(t)
	ctx := context.Background()
	products := storage.NewSQLProductStore(db)
	cartStore := storage.NewSQLCartStore(db)
	ex, err := service.NewPersistentExchangeService(ctx, storage.NewSQLRUBRateStore(db.Conn()), 50, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.server.deps.Exchange = ex
	f.server.deps.Catalog = shop.NewCatalogService(products, ex)
	f.server.deps.Cart = shop.NewCartService(cartStore, products, ex)
	f.server.deps.Orders = shop.NewOrderService(storage.NewSQLOrderStore(db), cartStore, products, shop.PaymentDeps{}, slog.Default(), ex)
	detail := decodeJSON(t, f.request(t, http.MethodGet, fmt.Sprintf("/api/products/%d", id), "", true))
	quote := detail["open_price_rates"].(map[string]any)
	if quote["rub_per_usd"] != float64(100) || quote["stars_per_usd"] != float64(50) {
		t.Fatal(quote)
	}
	if _, ok := decodeJSON(t, f.request(t, http.MethodGet, fmt.Sprintf("/api/products/%d", fixed), "", true))["open_price_rates"]; ok {
		t.Fatal("fixed-price quote exposed")
	}
	cart := decodeJSON(t, f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":100}`, id), true))
	item := cart["items"].([]any)[0].(map[string]any)
	if item["price_rub"] != float64(100) || item["price_stars"] != float64(50) {
		t.Fatal(item)
	}
	if err := ex.UpdateRUBRate(ctx, 200); err != nil {
		t.Fatal(err)
	}
	cart = decodeJSON(t, f.request(t, http.MethodGet, "/api/cart", "", true))
	quote = cart["open_price_rates"].(map[string]any)
	item = cart["items"].([]any)[0].(map[string]any)
	if quote["rub_per_usd"] != float64(200) || item["price_stars"] != float64(25) || item["price_rub"] != float64(100) {
		t.Fatal("active rate mismatch", cart)
	}
	cart = decodeJSON(t, f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":0}`, id), true))
	if cart["total_stars"] != float64(0) || cart["free_checkout"] != true {
		t.Fatal("zero preview broke free cart", cart)
	}
	var amount, orders int
	if err := db.Conn().QueryRow(`SELECT price_rub FROM products WHERE id=?`, id).Scan(&amount); err != nil || amount != 0 {
		t.Fatal("cart choice changed base price", err)
	}
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&orders); err != nil || orders != 0 {
		t.Fatal("preview created order", err)
	}
}
