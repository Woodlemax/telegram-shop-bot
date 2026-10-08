package webapi

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"shop_bot/internal/storage"
)

func (f *fakeOrders) GetUserOrdersPaged(ctx context.Context, userID int64, limit, offset int) ([]storage.Order, int, error) {
	orders, err := f.GetUserOrders(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].CreatedAt.Equal(orders[j].CreatedAt) {
			return orders[i].ID > orders[j].ID
		}
		return orders[i].CreatedAt.After(orders[j].CreatedAt)
	})
	total := len(orders)
	if offset >= total {
		return []storage.Order{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return orders[offset:end], total, nil
}

func (f *fakeOrders) CancelOrder(ctx context.Context, orderID, userID int64) error {
	o, err := f.GetOrder(ctx, orderID)
	if err != nil || o.UserID != userID || o.Status != storage.OrderStatusPending || o.PaymentState != storage.PaymentStatePending {
		return storage.ErrNotFound
	}
	o.Status, o.OrderState, o.PaymentState = storage.OrderStatusCancelled, storage.OrderStateCancelled, storage.PaymentStateCancelled
	return nil
}

func TestOrderHistoryOwnershipPaginationAndSnapshots(t *testing.T) {
	f, db, _, productID := realOpenPriceFixture(t)
	ctx := context.Background()
	store := storage.NewSQLOrderStore(db)
	var ids []int64
	for i := 0; i < 13; i++ {
		id, err := store.CreateOrder(ctx, &storage.Order{UserID: 42, Status: storage.OrderStatusPending, TotalRUB: 123.45, TotalUSD: 1.44, TotalStars: 72, PaymentMethod: "yookassa", PaymentID: "private-payment", PromoCode: "private-promo"}, []storage.OrderItem{{ProductID: productID, ProductName: "Original plane <name>", Quantity: 2, PriceUSD: 0.72}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if _, err := db.Conn().Exec(`UPDATE orders SET created_at='2026-10-07 10:00:00' WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
	}
	foreign, err := store.CreateOrder(ctx, &storage.Order{UserID: 43, Status: storage.OrderStatusPending}, []storage.OrderItem{{ProductID: productID, ProductName: "Foreign product", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn().Exec(`UPDATE products SET name='Renamed plane',price_rub=999 WHERE id=?`, productID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/orders", fmt.Sprintf("/api/orders/%d", ids[0])} {
		if r := f.request(t, http.MethodGet, path, "", false); r.Code != http.StatusUnauthorized {
			t.Fatalf("no auth: %s %d", path, r.Code)
		}
	}
	r := f.request(t, http.MethodGet, "/api/orders?page=1&user_id=43", "", true)
	if r.Code != http.StatusOK {
		t.Fatalf("history: %d %s", r.Code, r.Body.String())
	}
	if r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private history is cacheable")
	}
	body := decodeJSON(t, r)
	orders := body["orders"].([]any)
	if body["total"] != float64(13) || body["per_page"] != float64(10) || len(orders) != 10 {
		t.Fatalf("page: %v", body)
	}
	for i, raw := range orders {
		o := raw.(map[string]any)
		if o["id"] != float64(ids[12-i]) {
			t.Fatalf("tie ordering: %v", o)
		}
		if o["total_rub"] != 123.45 {
			t.Fatalf("recalculated amount: %v", o)
		}
		item := o["items"].([]any)[0].(map[string]any)
		if item["name"] != "Original plane <name>" || item["quantity"] != float64(2) {
			t.Fatalf("wrong snapshot: %v", item)
		}
	}
	for _, private := range []string{"private-payment", "private-promo", "Foreign product", "user_id", "payment_id", "file_id"} {
		if strings.Contains(r.Body.String(), private) {
			t.Fatalf("private data in history: %s", private)
		}
	}
	r = f.request(t, http.MethodGet, "/api/orders?page=2", "", true)
	if len(decodeJSON(t, r)["orders"].([]any)) != 3 {
		t.Fatalf("second page: %s", r.Body.String())
	}
	r = f.request(t, http.MethodGet, "/api/orders?page=3", "", true)
	if len(decodeJSON(t, r)["orders"].([]any)) != 0 {
		t.Fatalf("empty page: %s", r.Body.String())
	}
	for _, page := range []string{"0", "-1", "oops", "1000001", "9223372036854775807"} {
		if r := f.request(t, http.MethodGet, "/api/orders?page="+page, "", true); r.Code != http.StatusBadRequest {
			t.Fatalf("bad page %s: %d", page, r.Code)
		}
	}
	r = f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", ids[0]), "", true)
	if r.Code != http.StatusOK || decodeJSON(t, r)["order"].(map[string]any)["total_rub"] != 123.45 {
		t.Fatalf("detail: %d %s", r.Code, r.Body.String())
	}
	for _, id := range []int64{foreign, 999999} {
		if r := f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", id), "", true); r.Code != http.StatusNotFound {
			t.Fatalf("foreign/missing detail: %d", r.Code)
		}
	}
}

func TestOrderHistoryIncludesFreeRefundedAndPendingOrders(t *testing.T) {
	f, db, digitalID, _ := realOpenPriceFixture(t)
	ctx := context.Background()
	store := storage.NewSQLOrderStore(db)
	free, err := store.CreateOrder(ctx, &storage.Order{UserID: 42, Status: storage.OrderStatusPending}, []storage.OrderItem{{ProductID: digitalID, ProductName: "Free plane", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ConfirmFreeOrder(ctx, free, 42); err != nil {
		t.Fatal(err)
	}
	r := f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", free), "", true)
	o := decodeJSON(t, r)["order"].(map[string]any)
	if o["payment_method"] != "free" || o["payment_state"] != "settled" || o["total_rub"] != float64(0) {
		t.Fatalf("free history: %v", o)
	}
	for _, state := range []string{"refunded", "partially_refunded", "needs_review"} {
		if _, err := db.Conn().Exec(`UPDATE orders SET payment_state=? WHERE id=?`, state, free); err != nil {
			t.Fatal(err)
		}
		r = f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", free), "", true)
		if decodeJSON(t, r)["order"].(map[string]any)["payment_state"] != state {
			t.Fatal("payment state lost")
		}
	}
	for _, bad := range []string{"0", "-1", "abc"} {
		if r := f.request(t, http.MethodGet, "/api/orders/"+bad, "", true); r.Code != http.StatusBadRequest {
			t.Fatalf("bad id: %s %d", bad, r.Code)
		}
	}
	f.server.deps.Orders = &fakeOrders{}
	r = f.request(t, http.MethodGet, "/api/orders", "", true)
	if r.Code != http.StatusOK || strings.Contains(r.Body.String(), `"orders":null`) || len(decodeJSON(t, r)["orders"].([]any)) != 0 {
		t.Fatalf("empty history: %s", r.Body.String())
	}
}
