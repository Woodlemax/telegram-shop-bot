package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"shop_bot/internal/service"
	"shop_bot/internal/shop"
	"shop_bot/internal/storage"
	"testing"
)

func TestStarsOnlyCheckoutResumeAndRubleDisplay(t *testing.T) {
	f, db, digitalID, physicalID := realOpenPriceFixture(t)
	ctx := context.Background()
	products := storage.NewSQLProductStore(db)
	cart := storage.NewSQLCartStore(db)
	store := storage.NewSQLOrderStore(db)
	ex := service.NewExchangeService(50, 100, 0)
	f.server.deps.Cart = shop.NewCartService(cart, products, ex)
	f.server.deps.Catalog = shop.NewCatalogService(products, ex)
	f.server.deps.Orders = shop.NewOrderService(store, cart, products, shop.PaymentDeps{}, slog.Default(), ex)
	f.server.deps.StarsOnlyPayments = true
	f.server.deps.USDToRUBRate = 100
	f.server.deps.YooKassaAvailable = true
	f.server.deps.StripeAvailable = true
	f.server.deps.TONAvailable = true
	f.server.deps.NowpaymentsAvailable = true
	r := f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":100}`, digitalID), true)
	body := decodeJSON(t, r)
	if body["total_rub"] != float64(100) || body["total_usd"] != float64(1) || body["total_stars"] != float64(50) || body["stars_only"] != true {
		t.Fatalf("100 RUB / 50 Stars: %v", body)
	}
	for _, key := range []string{"yookassa_enabled", "stripe_enabled", "ton_enabled", "nowpayments_enabled"} {
		if body[key] != false {
			t.Fatalf("external button remains: %s", key)
		}
	}
	for _, method := range []string{"crypto", "yookassa", "stripe", "ton", "nowpayments", "balance"} {
		r = f.request(t, http.MethodPost, "/api/checkout", fmt.Sprintf(`{"method":%q}`, method), true)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("external checkout %s: %d", method, r.Code)
		}
	}
	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected checkout created order: %d %v", count, err)
	}
	r = f.request(t, http.MethodPost, "/api/checkout", `{"method":"stars"}`, true)
	if r.Code != http.StatusOK {
		t.Fatalf("Stars: %d %s", r.Code, r.Body.String())
	}
	id := int64(decodeJSON(t, r)["order_id"].(float64))
	var prices []struct {
		Amount int `json:"amount"`
	}
	if err := json.Unmarshal([]byte(f.tg.params["prices"]), &prices); err != nil || len(prices) != 1 || prices[0].Amount != 50 {
		t.Fatalf("wrong Stars charge: %v %v", prices, err)
	}
	for _, method := range []string{"crypto", "yookassa", "stripe", "ton", "nowpayments", "balance"} {
		r = f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/pay", id), fmt.Sprintf(`{"method":%q}`, method), true)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("external resume %s: %d", method, r.Code)
		}
	}
	r = f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", id), "", true)
	methods := decodeJSON(t, r)["order"].(map[string]any)["payment_methods"].([]any)
	if len(methods) != 1 || methods[0] != "stars" {
		t.Fatalf("resume choices: %v", methods)
	}
	// Legacy USD display converts to rubles without modifying catalog/order data.
	if _, err := db.Conn().Exec(`UPDATE products SET price_rub=NULL,price_usd=10 WHERE id=?`, physicalID); err != nil {
		t.Fatal(err)
	}
	r = f.request(t, http.MethodGet, fmt.Sprintf("/api/products/%d", physicalID), "", true)
	p := decodeJSON(t, r)["product"].(map[string]any)
	if p["price_rub"] != float64(1000) || p["price_stars"] != float64(500) {
		t.Fatalf("legacy product display: %v", p)
	}
	legacy, err := store.CreateOrder(ctx, &storage.Order{UserID: 42, Status: "pending", TotalUSD: 10, TotalStars: 500}, []storage.OrderItem{{ProductID: physicalID, ProductName: "Legacy plane", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	r = f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", legacy), "", true)
	old := decodeJSON(t, r)["order"].(map[string]any)
	if old["display_total_rub"] != float64(1000) || old["total_rub"] != float64(0) || old["total_stars"] != float64(500) {
		t.Fatalf("legacy snapshot altered: %v", old)
	}
	f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":0}`, digitalID), true)
	r = f.request(t, http.MethodPost, "/api/checkout", `{"method":"free"}`, true)
	if r.Code != http.StatusOK || decodeJSON(t, r)["free"] != true {
		t.Fatalf("free checkout disabled: %d %s", r.Code, r.Body.String())
	}
	if f.yookassa.called || f.stripe.called || f.ton.called || f.nowpayments.called {
		t.Fatal("disabled provider contacted")
	}
}
