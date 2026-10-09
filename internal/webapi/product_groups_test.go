package webapi

import (
	"context"
	"fmt"
	"net/http"
	"shop_bot/internal/storage"
	"testing"
)

func TestGroupedCatalogLazyModificationsPagingAndAuth(t *testing.T) {
	f, db, main, child := realOpenPriceFixture(t)
	ctx := context.Background()
	groups := storage.NewProductGroupStore(db.Conn())
	f.server.deps.ProductGroups = groups
	products := storage.NewSQLProductStore(db)
	p, _ := products.GetProduct(ctx, main)
	cat := p.CategoryID
	if err := groups.SetParent(ctx, child, main); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 22; i++ {
		id, err := products.CreateProduct(ctx, &storage.Product{Name: fmt.Sprintf("Mod %d", i), CategoryID: cat, PriceUSD: 1, Stock: 1, IsActive: true})
		if err != nil {
			t.Fatal(err)
		}
		if err = groups.SetParent(ctx, id, main); err != nil {
			t.Fatal(err)
		}
	}
	soon, err := products.CreateProduct(ctx, &storage.Product{Name: "Soon mod", CategoryID: cat, PriceUSD: 1, Stock: 0, IsActive: true, ComingSoon: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = groups.SetParent(ctx, soon, main); err != nil {
		t.Fatal(err)
	}
	r := f.request(t, http.MethodGet, fmt.Sprintf("/api/products?category=%d", cat), "", true)
	body := decodeJSON(t, r)
	list := body["products"].([]any)
	if r.Code != 200 || body["total"] != float64(1) || len(list) != 1 {
		t.Fatalf("group catalog: %s", r.Body.String())
	}
	if list[0].(map[string]any)["id"] != float64(main) || list[0].(map[string]any)["modification_count"] != float64(24) {
		t.Fatalf("group metadata: %v", list)
	}
	url := fmt.Sprintf("/api/products/%d/modifications", main)
	if r = f.request(t, http.MethodGet, url, "", false); r.Code != 401 {
		t.Fatal("mods need auth")
	}
	r = f.request(t, http.MethodGet, url, "", true)
	body = decodeJSON(t, r)
	list = body["products"].([]any)
	if r.Code != 200 || body["total"] != float64(24) || len(list) != 20 {
		t.Fatalf("mods: %s", r.Body.String())
	}
	if list[0].(map[string]any)["price_rub"] != float64(200) {
		t.Fatal("mod price lost")
	}
	r = f.request(t, http.MethodGet, url+"?page=2", "", true)
	list = decodeJSON(t, r)["products"].([]any)
	if len(list) != 4 || list[3].(map[string]any)["coming_soon"] != true {
		t.Fatalf("mod availability: %v", list)
	}
	for _, page := range []string{"0", "-1", "oops", "1000001"} {
		if r = f.request(t, http.MethodGet, url+"?page="+page, "", true); r.Code != 400 {
			t.Fatal("invalid page accepted")
		}
	}
	// Modifications retain their normal product card and cart identity.
	r = f.request(t, http.MethodGet, fmt.Sprintf("/api/products/%d", child), "", true)
	if r.Code != 200 {
		t.Fatal("child card unavailable")
	}
	r = f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, child), true)
	if r.Code != 200 {
		t.Fatalf("mod cart: %s", r.Body.String())
	}
	// Pagination counts models, regardless of how many mods the first one has.
	for i := 0; i < 11; i++ {
		_, err = products.CreateProduct(ctx, &storage.Product{Name: fmt.Sprintf("Main %d", i), CategoryID: cat, PriceUSD: 1, Stock: 1, IsActive: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	r = f.request(t, http.MethodGet, fmt.Sprintf("/api/products?category=%d&page=2", cat), "", true)
	body = decodeJSON(t, r)
	if body["total"] != float64(12) || len(body["products"].([]any)) != 2 {
		t.Fatalf("main page count: %s", r.Body.String())
	}
	p.IsActive = false
	if err = products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	r = f.request(t, http.MethodGet, url, "", true)
	if r.Code != 404 {
		t.Fatal("hidden main exposes modifications endpoint")
	}
	r = f.request(t, http.MethodGet, fmt.Sprintf("/api/products?category=%d", cat), "", true)
	if decodeJSON(t, r)["total"] != float64(35) {
		t.Fatal("mods disappeared with hidden main")
	}
}
