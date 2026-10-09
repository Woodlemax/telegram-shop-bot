package webapi

import (
	"context"
	"fmt"
	"net/http"
	"shop_bot/internal/storage"
	"testing"
)

func TestAuthorSaleMiniAppRUBOnlyAndCartBlocked(t *testing.T) {
	f, db, _, id := realOpenPriceFixture(t)
	ctx := context.Background()
	products := storage.NewSQLProductStore(db)
	r := f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, id), true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	p, err := products.GetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.AuthorTelegramURL = "@plane_author"
	p.Stock = 0
	if err = products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	f.server.deps.ProductGroups = storage.NewProductGroupStore(db.Conn())
	for _, path := range []string{fmt.Sprintf("/api/products/%d", id), fmt.Sprintf("/api/products?category=%d", p.CategoryID)} {
		r = f.request(t, http.MethodGet, path, "", true)
		if r.Code != 200 {
			t.Fatal(r.Body.String())
		}
		data := decodeJSON(t, r)
		var item map[string]any
		if data["product"] != nil {
			item = data["product"].(map[string]any)
		} else {
			for _, raw := range data["products"].([]any) {
				candidate := raw.(map[string]any)
				if candidate["id"] == float64(id) {
					item = candidate
				}
			}
		}
		if item == nil || item["buy_from_author"] != true || item["author_telegram_url"] != "https://t.me/plane_author" || item["price_rub"] != float64(200) || item["price_stars"] != float64(0) || item["price_usd"] != float64(0) {
			t.Fatalf("author card: %v", item)
		}
	}
	r = f.request(t, http.MethodGet, "/api/cart", "", true)
	if r.Code != 200 || len(decodeJSON(t, r)["items"].([]any)) != 0 {
		t.Fatal("old cart retained author", r.Body.String())
	}
	for _, body := range []string{fmt.Sprintf(`{"product_id":%d,"delta":1}`, id), fmt.Sprintf(`{"product_id":%d,"price":0}`, id), fmt.Sprintf(`{"product_id":%d,"price":100}`, id)} {
		r = f.request(t, http.MethodPost, "/api/cart", body, true)
		if r.Code < 400 {
			t.Fatalf("author cart bypass: %s %s", body, r.Body.String())
		}
	}
	r = f.request(t, http.MethodPost, "/api/checkout", `{"method":"stars"}`, true)
	if r.Code < 400 || f.tg.endpoint != "" || f.yookassa.called {
		t.Fatal("author generated a payment")
	}
}
