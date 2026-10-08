package webapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"shop_bot/internal/storage"
)

func TestOrderDownloadEntitlementLatestArchiveAndQueue(t *testing.T) {
	f, db, digitalID, physicalID := realOpenPriceFixture(t)
	ctx := context.Background()
	archives := storage.NewDigitalArchiveStore(db)
	f.server.deps.Archives = archives
	orders := storage.NewSQLOrderStore(db)
	create := func(owner int64) int64 {
		t.Helper()
		id, err := orders.CreateOrder(ctx, &storage.Order{UserID: owner, Status: storage.OrderStatusPending}, []storage.OrderItem{{ProductID: digitalID, ProductName: "Plane", Quantity: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	id, foreign := create(42), create(43)
	post := func(orderID, productID int64, auth bool) int {
		t.Helper()
		r := f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/download", orderID), fmt.Sprintf(`{"product_id":%d}`, productID), auth)
		return r.Code
	}
	detail := func(want bool, filename string) {
		t.Helper()
		r := f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", id), "", true)
		if r.Code != http.StatusOK {
			t.Fatalf("detail: %d %s", r.Code, r.Body.String())
		}
		item := decodeJSON(t, r)["order"].(map[string]any)["items"].([]any)[0].(map[string]any)
		if item["download_available"] != want {
			t.Fatalf("availability: %v", item)
		}
		if want && item["archive_name"] != filename {
			t.Fatalf("filename: %v", item)
		}
		for _, private := range []string{"api-plane-zip", "zip-v2-private", "file_id", "archive_id", "lease_token"} {
			if strings.Contains(r.Body.String(), private) {
				t.Fatalf("private archive data: %s", private)
			}
		}
	}
	if got := post(id, digitalID, false); got != http.StatusUnauthorized {
		t.Fatalf("no auth: %d", got)
	}
	detail(false, "")
	if got := post(id, digitalID, true); got != http.StatusNotFound {
		t.Fatalf("unpaid access: %d", got)
	}
	if err := orders.ConfirmFreeOrder(ctx, id, 42); err != nil {
		t.Fatal(err)
	}
	if err := orders.ConfirmFreeOrder(ctx, foreign, 43); err != nil {
		t.Fatal(err)
	}
	detail(true, "plane.zip")
	if got := post(foreign, digitalID, true); got != http.StatusNotFound {
		t.Fatalf("foreign access: %d", got)
	}
	if got := post(id, physicalID, true); got != http.StatusNotFound {
		t.Fatalf("wrong product: %d", got)
	}
	claim, err := archives.Claim(ctx)
	if err != nil || claim == nil || claim.OrderID != id {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	if err := archives.Finish(ctx, claim, 10, true); err != nil {
		t.Fatal(err)
	}
	if _, err := archives.Add(ctx, digitalID, "zip-v2-private", "v2.zip", 80_000_000); err != nil {
		t.Fatal(err)
	}
	detail(true, "v2.zip")
	for i := 0; i < 2; i++ {
		if got := post(id, digitalID, true); got != http.StatusOK {
			t.Fatalf("queue: %d", got)
		}
	}
	claim, err = archives.Claim(ctx)
	if err != nil || claim == nil || claim.OrderID != id || claim.FileID != "zip-v2-private" {
		t.Fatalf("updated claim: %+v %v", claim, err)
	}
	// Another click during sending keeps the lease and produces no extra send.
	if got := post(id, digitalID, true); got != http.StatusOK {
		t.Fatalf("in-flight request: %d", got)
	}
	if err := archives.Finish(ctx, claim, 11, true); err != nil {
		t.Fatal(err)
	}
	var count, stock int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM digital_deliveries WHERE order_id=?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn().QueryRow(`SELECT stock FROM products WHERE id=?`, digitalID).Scan(&stock); err != nil {
		t.Fatal(err)
	}
	if count != 1 || stock != 1 {
		t.Fatalf("duplicate row or stock changed: %d %d", count, stock)
	}
	for _, state := range []string{"refunded", "partially_refunded", "needs_review", "cancelled"} {
		if _, err := db.Conn().Exec(`UPDATE orders SET payment_state=? WHERE id=?`, state, id); err != nil {
			t.Fatal(err)
		}
		detail(false, "")
		if got := post(id, digitalID, true); got != http.StatusNotFound {
			t.Fatalf("revoked %s: %d", state, got)
		}
	}
	if _, err := db.Conn().Exec(`UPDATE orders SET payment_state='settled',status='cancelled',order_state='cancelled' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	detail(false, "")
	if got := post(id, digitalID, true); got != http.StatusNotFound {
		t.Fatalf("cancelled order: %d", got)
	}
	if got := post(id, 0, true); got != http.StatusBadRequest {
		t.Fatalf("bad product: %d", got)
	}
}
