package webapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"shop_bot/internal/storage"
)

type orderCancelRace struct {
	OrderService
	beforeCancel func()
}

func (s orderCancelRace) CancelOrder(ctx context.Context, orderID, userID int64) error {
	s.beforeCancel()
	return s.OrderService.CancelOrder(ctx, orderID, userID)
}

func TestMiniAppOrderCancelOwnershipStateAndPaymentRace(t *testing.T) {
	f, db, digitalID, physicalID := realOpenPriceFixture(t)
	ctx := context.Background()
	store := storage.NewSQLOrderStore(db)
	create := func(owner, product int64, total float64) int64 {
		t.Helper()
		id, err := store.CreateOrder(ctx, &storage.Order{UserID: owner, Status: storage.OrderStatusPending, TotalRUB: total}, []storage.OrderItem{{ProductID: product, ProductName: "Plane", Quantity: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	post := func(id int64, auth bool) int {
		t.Helper()
		r := f.request(t, http.MethodPost, fmt.Sprintf("/api/orders/%d/cancel?user_id=43", id), `{"user_id":43}`, auth)
		if auth && r.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cancel response is cacheable")
		}
		return r.Code
	}
	own, foreign := create(42, physicalID, 100), create(43, physicalID, 100)
	if got := post(own, false); got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", got)
	}
	for _, id := range []int64{foreign, 999999} {
		if got := post(id, true); got != http.StatusNotFound {
			t.Fatalf("foreign/missing: %d", got)
		}
	}
	for _, id := range []int64{0, -1} {
		if got := post(id, true); got != http.StatusBadRequest {
			t.Fatalf("bad ID: %d", got)
		}
	}
	r := f.request(t, http.MethodPost, "/api/orders/bad/cancel", "", true)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("malformed ID: %d", r.Code)
	}
	if got := post(own, true); got != http.StatusOK {
		t.Fatalf("cancel: %d", got)
	}
	order, err := store.GetOrder(ctx, own)
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != storage.OrderStatusCancelled || order.PaymentState != storage.PaymentStateCancelled || order.OrderState != storage.OrderStateCancelled {
		t.Fatalf("not cancelled: %+v", order)
	}
	if got := post(own, true); got != http.StatusConflict {
		t.Fatalf("repeat cancel: %d", got)
	}
	r = f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", own), "", true)
	if r.Code != http.StatusOK || decodeJSON(t, r)["order"].(map[string]any)["status"] != storage.OrderStatusCancelled {
		t.Fatal("detail did not update")
	}
	foreignOrder, err := store.GetOrder(ctx, foreign)
	if err != nil || foreignOrder.Status != storage.OrderStatusPending {
		t.Fatal("foreign order changed")
	}
	for _, state := range []struct{ status, payment string }{
		{"paid", "settled"}, {"delivered", "settled"}, {"paid", "refunded"}, {"paid", "partially_refunded"}, {"pending", "needs_review"}, {"cancelled", "cancelled"},
	} {
		id := create(42, physicalID, 100)
		if _, err := db.Conn().Exec(`UPDATE orders SET status=?,payment_state=? WHERE id=?`, state.status, state.payment, id); err != nil {
			t.Fatal(err)
		}
		if got := post(id, true); got != http.StatusConflict {
			t.Fatalf("state %v: %d", state, got)
		}
		o, err := store.GetOrder(ctx, id)
		if err != nil || o.Status != state.status || o.PaymentState != state.payment {
			t.Fatalf("rejected cancellation changed order: %+v %v", o, err)
		}
	}
	free := create(42, digitalID, 0)
	if err := store.ConfirmFreeOrder(ctx, free, 42); err != nil {
		t.Fatal(err)
	}
	if got := post(free, true); got != http.StatusConflict {
		t.Fatalf("free granted: %d", got)
	}
	// A confirmed payment wins even if the UI and pre-read still saw pending.
	race := create(42, physicalID, 100)
	original := f.server.deps.Orders
	f.server.deps.Orders = orderCancelRace{OrderService: original, beforeCancel: func() {
		if _, err := db.Conn().Exec(`UPDATE orders SET status='paid',payment_state='settled' WHERE id=?`, race); err != nil {
			t.Fatal(err)
		}
	}}
	if got := post(race, true); got != http.StatusConflict {
		t.Fatalf("payment race: %d", got)
	}
	order, err = store.GetOrder(ctx, race)
	if err != nil || order.Status != storage.OrderStatusPaid || order.PaymentState != storage.PaymentStateSettled {
		t.Fatal("payment overwritten by cancellation")
	}
	var stock, receipts, cancelEvents int
	if err := db.Conn().QueryRow(`SELECT stock FROM products WHERE id=?`, physicalID).Scan(&stock); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM payment_events WHERE order_id=?`, own).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM order_events WHERE order_id=? AND event_type='order.cancelled'`, own).Scan(&cancelEvents); err != nil {
		t.Fatal(err)
	}
	if stock != 2 || receipts != 0 || cancelEvents != 1 {
		t.Fatalf("unexpected stock/receipts/cancel events: %d %d %d", stock, receipts, cancelEvents)
	}
}
