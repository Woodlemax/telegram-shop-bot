package webapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"shop_bot/internal/storage"
	"strings"
	"testing"
)

func TestFileCheckAPIBuyerUploadAndPurchaseGuards(t *testing.T) {
	f, db, id, _ := realOpenPriceFixture(t)
	ctx := context.Background()
	checks := storage.NewProductFileCheckStore(db.Conn())
	f.server.deps.FileChecks = checks
	digest := func(data string) string { sum := sha256.Sum256([]byte(data)); return hex.EncodeToString(sum[:]) }
	ref, err := checks.ReplaceReference(ctx, id, "CAN_1.STL", digest("original"), 8, 0)
	if err != nil {
		t.Fatal(err)
	}
	ref, err = checks.Save(ctx, id, true, "Attach CAN_1.STL", ref.Revision)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/products/%d/verify-file", id)
	for _, test := range []struct {
		body   string
		auth   bool
		status int
	}{{"original", false, 401}, {"", true, 400}, {"changed!", true, 422}, {`{"sha256":"` + digest("original") + `"}`, true, 422}} {
		r := f.request(t, http.MethodPost, path, test.body, test.auth)
		if r.Code != test.status {
			t.Fatalf("upload status %d: %s", r.Code, r.Body.String())
		}
	}
	for _, body := range []string{fmt.Sprintf(`{"product_id":%d,"delta":1}`, id), fmt.Sprintf(`{"product_id":%d,"price":0}`, id)} {
		r := f.request(t, http.MethodPost, "/api/cart", body, true)
		if r.Code != 403 {
			t.Fatal("cart bypass", r.Code, r.Body.String())
		}
	}
	r := f.request(t, http.MethodGet, fmt.Sprintf("/api/products/%d", id), "", true)
	check := decodeJSON(t, r)["file_check"].(map[string]any)
	if check["verified"] != false || check["description"] != "Attach CAN_1.STL" || strings.Contains(r.Body.String(), digest("original")) {
		t.Fatal("bad public status", r.Body.String())
	}
	r = f.request(t, http.MethodPost, path, "original", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r = f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, id), true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	pending, err := storage.NewSQLOrderStore(db).CreateOrder(ctx, &storage.Order{UserID: 42, Status: storage.OrderStatusPending}, []storage.OrderItem{{ProductID: id, ProductName: "Protected add-on", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := checks.ReplaceReference(ctx, id, "CAN_1.STL", digest("new-file"), 8, ref.Revision)
	if err != nil {
		t.Fatal(err)
	}
	r = f.request(t, http.MethodGet, "/api/cart", "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	items := decodeJSON(t, r)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["file_check"].(map[string]any)["verified"] != false {
		t.Fatal("cart lost locked item", r.Body.String())
	}
	r = f.request(t, http.MethodPost, "/api/checkout", `{"method":"free"}`, true)
	if r.Code != 403 {
		t.Fatal("checkout bypass", r.Code, r.Body.String())
	}
	r = f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/pay", pending), `{"method":"free"}`, true)
	if r.Code != 403 {
		t.Fatal("resumed payment bypass", r.Code, r.Body.String())
	}
	r = f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", pending), "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	order := decodeJSON(t, r)["order"].(map[string]any)
	if _, ok := order["payment_methods"]; ok {
		t.Fatal("payment methods exposed while locked", order)
	}
	r = f.request(t, http.MethodPost, path, "original", true)
	if r.Code != 422 {
		t.Fatal("old file accepted", r.Body.String())
	}
	r = f.request(t, http.MethodPost, path, "new-file", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if updated.Version <= ref.Version {
		t.Fatal("version did not advance")
	}
	r = f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/pay", pending), `{"method":"free"}`, true)
	if r.Code != 200 {
		t.Fatal("reverified order blocked", r.Body.String())
	}
	if f.tg.endpoint != "" || f.yookassa.called {
		t.Fatal("verification invoked payment providers")
	}
}
