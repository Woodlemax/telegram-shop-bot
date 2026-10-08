package webapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"shop_bot/internal/storage"
)

func TestProductQuantitySettingsCheckout(t *testing.T) {
	for _, digital := range []bool{false, true} {
		for _, infinite := range []bool{false, true} {
			for _, single := range []bool{false, true} {
				for _, paid := range []bool{false, true} {
					t.Run(fmt.Sprintf("digital=%t/infinite=%t/single=%t/paid=%t", digital, infinite, single, paid), func(t *testing.T) {
						f, db, digitalID, physicalID := realOpenPriceFixture(t)
						ctx := context.Background()
						id := physicalID
						if digital {
							id = digitalID
						}
						products := storage.NewSQLProductStore(db)
						p, err := products.GetProduct(ctx, id)
						if err != nil {
							t.Fatal(err)
						}
						p.InfiniteStock, p.SingleInCart, p.OpenPrice = infinite, single, false
						p.Stock = 3
						if infinite {
							p.Stock = 0
						}
						price := float64(0)
						if paid {
							price = 100
						}
						p.PriceRUB = &price
						if err := products.UpdateProduct(ctx, p); err != nil {
							t.Fatal(err)
						}
						card := f.request(t, http.MethodGet, fmt.Sprintf("/api/products/%d", id), "", true)
						product := decodeJSON(t, card)["product"].(map[string]any)
						if product["infinite_stock"] != infinite || product["single_in_cart"] != single {
							t.Fatalf("flags: %v", product)
						}
						for i := 0; i < 2; i++ {
							r := f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, id), true)
							if r.Code != http.StatusOK {
								t.Fatalf("add: %d %s", r.Code, r.Body.String())
							}
						}
						cart := f.request(t, http.MethodGet, "/api/cart", "", true)
						body := decodeJSON(t, cart)
						item := body["items"].([]any)[0].(map[string]any)
						wantQty := 2
						if single {
							wantQty = 1
						}
						if item["quantity"] != float64(wantQty) || item["single_in_cart"] != single {
							t.Fatalf("item: %v", item)
						}
						if body["total_rub"] != price*float64(wantQty) {
							t.Fatalf("total: %v", body["total_rub"])
						}
						method := "free"
						if paid {
							method = "stars"
						}
						response := f.request(t, http.MethodPost, "/api/checkout", fmt.Sprintf(`{"method":%q}`, method), true)
						if response.Code != http.StatusOK {
							t.Fatalf("checkout: %d %s", response.Code, response.Body.String())
						}
						orderID := int64(decodeJSON(t, response)["order_id"].(float64))
						orders := storage.NewSQLOrderStore(db)
						if paid {
							if err := orders.UpdateOrderStatus(ctx, orderID, storage.OrderStatusPending, storage.OrderStatusPaid, "stars", fmt.Sprintf("quantity-%d", orderID)); err != nil {
								t.Fatal(err)
							}
						}
						stored, err := products.GetProduct(ctx, id)
						if err != nil {
							t.Fatal(err)
						}
						wantStock := 3 - wantQty
						if infinite {
							wantStock = 0
						}
						if stored.Stock != wantStock {
							t.Fatalf("stock=%d want=%d", stored.Stock, wantStock)
						}
						if digital {
							claim, err := storage.NewDigitalArchiveStore(db).Claim(ctx)
							if err != nil || claim == nil {
								t.Fatalf("delivery: %+v %v", claim, err)
							}
						}
					})
				}
			}
		}
	}
}

func TestSingleInCartEnabledForExistingCart(t *testing.T) {
	f, db, _, id := realOpenPriceFixture(t)
	ctx := context.Background()
	products := storage.NewSQLProductStore(db)
	p, err := products.GetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.InfiniteStock = true
	p.Stock = 0
	if err := products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	r := f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":5}`, id), true)
	if r.Code != http.StatusOK {
		t.Fatalf("add: %d %s", r.Code, r.Body.String())
	}
	p.SingleInCart = true
	if err := products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	cs := storage.NewSQLCartStore(db)
	assertQuantity := func(want int) {
		t.Helper()
		items, err := cs.GetItems(ctx, 42)
		if err != nil || len(items) != 1 || items[0].Quantity != want {
			t.Fatalf("items=%+v err=%v want=%d", items, err, want)
		}
	}
	assertQuantity(1)
	if err := cs.AddItem(ctx, 42, id); err != nil {
		t.Fatal(err)
	}
	if err := cs.UpdateQuantity(ctx, 42, id, 5); err != nil {
		t.Fatal(err)
	}
	assertQuantity(1)
	r = f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":2}`, id), true)
	if r.Code != http.StatusConflict || decodeJSON(t, r)["error"] != "product_single_in_cart" {
		t.Fatalf("limit: %d %s", r.Code, r.Body.String())
	}
	_, err = storage.NewSQLOrderStore(db).CreateOrder(ctx, &storage.Order{UserID: 42, Status: storage.OrderStatusPending}, []storage.OrderItem{{ProductID: id, Quantity: 2}})
	if !errors.Is(err, storage.ErrSingleItemLimit) {
		t.Fatalf("bypassed limit: %v", err)
	}
	p.SingleInCart = false
	if err := products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	r = f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":2}`, id), true)
	if r.Code != http.StatusOK {
		t.Fatalf("allow multiple: %d %s", r.Code, r.Body.String())
	}
	assertQuantity(3)
	p.InfiniteStock = false
	if err := products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	r = f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, id), true)
	if r.Code != http.StatusConflict {
		t.Fatalf("finite stock bypass: %d", r.Code)
	}
}

func TestArchiveReplacementPreservesQuantitySettings(t *testing.T) {
	_, db, id, _ := realOpenPriceFixture(t)
	ctx := context.Background()
	products := storage.NewSQLProductStore(db)
	p, err := products.GetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.InfiniteStock, p.SingleInCart, p.Stock = false, false, 0
	if err := products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.NewDigitalArchiveStore(db).Add(ctx, id, "updated-plane", "updated.zip", 80_000_000); err != nil {
		t.Fatal(err)
	}
	p, err = products.GetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if p.InfiniteStock || p.SingleInCart || p.Stock != 0 {
		t.Fatalf("replacement reset settings: %+v", p)
	}
}
