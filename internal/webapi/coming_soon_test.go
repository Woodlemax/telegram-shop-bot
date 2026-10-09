package webapi

import (
	"context"
	"fmt"
	"net/http"
	"shop_bot/internal/storage"
	"testing"
)

func TestComingSoonMiniAppVisibleButNotPurchasableUntilRestocked(t *testing.T) {
	for _, digital := range []bool{false, true} {
		t.Run(fmt.Sprint(digital), func(t *testing.T) {
			f, db, digitalID, physicalID := realOpenPriceFixture(t)
			id := physicalID
			if digital {
				id = digitalID
			}
			ctx := context.Background()
			products := storage.NewSQLProductStore(db)
			item, err := products.GetProduct(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			item.ComingSoon = true
			item.Stock = 0
			if err = products.UpdateProduct(ctx, item); err != nil {
				t.Fatal(err)
			}
			r := f.request(t, http.MethodGet, fmt.Sprintf("/api/products?category=%d", item.CategoryID), "", true)
			if r.Code != 200 {
				t.Fatal(r.Body.String())
			}
			found := false
			for _, raw := range decodeJSON(t, r)["products"].([]any) {
				p := raw.(map[string]any)
				if p["id"] == float64(id) {
					found = true
					if p["coming_soon"] != true {
						t.Fatal("catalog badge missing")
					}
				}
			}
			if !found {
				t.Fatal("zero-stock preview hidden")
			}
			r = f.request(t, http.MethodGet, fmt.Sprintf("/api/products/%d", id), "", true)
			if r.Code != 200 || decodeJSON(t, r)["product"].(map[string]any)["coming_soon"] != true {
				t.Fatalf("card: %d %s", r.Code, r.Body.String())
			}
			for _, body := range []string{fmt.Sprintf(`{"product_id":%d,"delta":1}`, id), fmt.Sprintf(`{"product_id":%d,"price":0}`, id), fmt.Sprintf(`{"product_id":%d,"price":100}`, id)} {
				r = f.request(t, http.MethodPost, "/api/cart", body, true)
				if r.Code < 400 {
					t.Fatalf("preview cart bypass: %s %s", body, r.Body.String())
				}
			}
			if _, err = db.Conn().Exec(`INSERT INTO cart_items(user_id,product_id,quantity) VALUES(42,?,1)`, id); err != nil {
				t.Fatal(err)
			}
			r = f.request(t, http.MethodPost, "/api/checkout", `{"method":"stars"}`, true)
			if r.Code != http.StatusConflict {
				t.Fatalf("stale cart checkout: %d %s", r.Code, r.Body.String())
			}
			var count int
			db.Conn().QueryRow(`SELECT count(*) FROM orders`).Scan(&count)
			if count != 0 || f.tg.endpoint != "" || f.yookassa.called {
				t.Fatal("preview generated payment/order")
			}
			item.Stock = 2
			if err = products.UpdateProduct(ctx, item); err != nil {
				t.Fatal(err)
			}
			r = f.request(t, http.MethodGet, fmt.Sprintf("/api/products/%d", id), "", true)
			if decodeJSON(t, r)["product"].(map[string]any)["coming_soon"] != false {
				t.Fatal("restock badge retained")
			}
			r = f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, id), true)
			if r.Code != 200 {
				t.Fatalf("restock unavailable: %d %s", r.Code, r.Body.String())
			}
		})
	}
}
