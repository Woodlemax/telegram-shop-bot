package webapi

import (
	"context"
	"fmt"
	"net/http"
	"shop_bot/internal/storage"
	"testing"
)

func TestDeletedProductAPIAndExistingOrderAccess(t *testing.T) {
	f, db, open, fixed := realOpenPriceFixture(t)
	ctx := context.Background()
	f.server.deps.Archives = storage.NewDigitalArchiveStore(db)
	f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":0}`, open), true)
	free := f.request(t, http.MethodPost, "/api/checkout", `{"method":"free"}`, true)
	if free.Code != 200 {
		t.Fatal(free.Body.String())
	}
	freeID := int64(decodeJSON(t, free)["order_id"].(float64))
	f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, fixed), true)
	pending := f.request(t, http.MethodPost, "/api/checkout", `{"method":"stars"}`, true)
	if pending.Code != 200 {
		t.Fatal(pending.Body.String())
	}
	pendingID := int64(decodeJSON(t, pending)["order_id"].(float64))
	f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":100}`, open), true)
	f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, fixed), true)
	ps := storage.NewSQLProductStore(db)
	for _, id := range []int64{open, fixed} {
		if err := ps.DeleteProduct(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	cart := f.request(t, http.MethodGet, "/api/cart", "", true)
	if cart.Code != 200 || len(decodeJSON(t, cart)["items"].([]any)) != 0 {
		t.Fatal("deleted cart not empty", cart.Body.String())
	}
	for _, id := range []int64{open, fixed} {
		if r := f.request(t, http.MethodGet, fmt.Sprintf("/api/products/%d", id), "", true); r.Code != 404 {
			t.Fatal("deleted card accessible", r.Body.String())
		}
		if r := f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, id), true); r.Code != 409 {
			t.Fatal("deleted item re-added", r.Body.String())
		}
	}
	download := f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/download", freeID), fmt.Sprintf(`{"product_id":%d}`, open), true)
	if download.Code != 200 {
		t.Fatal("old download lost", download.Body.String())
	}
	resumed := f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/pay", pendingID), `{"method":"stars"}`, true)
	if resumed.Code != 200 {
		t.Fatal("saved payment lost", resumed.Body.String())
	}
	var orders int
	db.Conn().QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&orders)
	if orders != 2 {
		t.Fatal("deletion/resume modified history")
	}
}
