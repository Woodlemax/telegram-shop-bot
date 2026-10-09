package webapi

import (
	"context"
	"fmt"
	"net/http"
	"shop_bot/internal/storage"
	"testing"
)

func TestOrderPaymentSelectsOneRailAndHidesTheOther(t *testing.T) {
	for _, first := range []string{"stars", "yookassa"} {
		t.Run(first, func(t *testing.T) {
			f, db, productID, _ := realOpenPriceFixture(t)
			id, err := storage.NewSQLOrderStore(db).CreateOrder(context.Background(), &storage.Order{UserID: 42, Status: storage.OrderStatusPending, TotalStars: 100, TotalRUB: 100}, []storage.OrderItem{{ProductID: productID, Quantity: 1}})
			if err != nil {
				t.Fatal(err)
			}
			pay := fmt.Sprintf("/api/orders/%d/pay", id)
			r := f.request(t, http.MethodPost, pay, fmt.Sprintf(`{"method":%q}`, first), true)
			if r.Code != http.StatusOK {
				t.Fatalf("first method: %d %s", r.Code, r.Body.String())
			}
			other := "stars"
			if first == "stars" {
				other = "yookassa"
			}
			f.tg.endpoint = ""
			f.yookassa.called = false
			r = f.request(t, http.MethodPost, pay, fmt.Sprintf(`{"method":%q}`, other), true)
			if r.Code != http.StatusConflict || decodeJSON(t, r)["error"] != "payment_method_locked" {
				t.Fatalf("second method: %d %s", r.Code, r.Body.String())
			}
			if f.tg.endpoint != "" || f.yookassa.called {
				t.Fatal("rejected payment contacted provider")
			}
			r = f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", id), "", true)
			order := decodeJSON(t, r)["order"].(map[string]any)
			methods := order["payment_methods"].([]any)
			if len(methods) != 1 || methods[0] != first || order["checkout_provider"] != first {
				t.Fatalf("options after choice: %v", order)
			}
			r = f.request(t, http.MethodPost, pay, fmt.Sprintf(`{"method":%q}`, first), true)
			if r.Code != http.StatusOK {
				t.Fatalf("same rail retry blocked: %d %s", r.Code, r.Body.String())
			}
		})
	}
}
