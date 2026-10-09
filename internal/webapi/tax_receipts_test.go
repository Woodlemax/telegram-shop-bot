package webapi

import (
	"context"
	"fmt"
	"net/http"
	"shop_bot/internal/storage"
	"strings"
	"testing"
)

func TestOrderTaxReceiptVisibleOnlyToOwnerEvenWhenTelegramDeliveryFailed(t *testing.T) {
	f, db, _, productID := realOpenPriceFixture(t)
	ctx := context.Background()
	store := storage.NewSQLOrderStore(db)
	seed := func(userID int64, paymentID, link string) int64 {
		id, err := store.CreateOrder(ctx, &storage.Order{UserID: userID, Status: storage.OrderStatusPending, TotalRUB: 100}, []storage.OrderItem{{ProductID: productID, ProductName: "Plane", Quantity: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.UpdateOrderStatus(ctx, id, storage.OrderStatusPending, storage.OrderStatusPaid, storage.PaymentMethodYooKassa, paymentID); err != nil {
			t.Fatal(err)
		}
		receipts := storage.NewOrderTaxReceiptStore(db.Conn())
		if _, err = receipts.Attach(ctx, id, 9001, link); err != nil {
			t.Fatal(err)
		}
		ok, err := receipts.ClaimDelivery(ctx, id, "fail")
		if err != nil || !ok {
			t.Fatal(err)
		}
		if err = receipts.FinishDelivery(ctx, id, "fail", 0, false); err != nil {
			t.Fatal(err)
		}
		return id
	}
	link := "https://lknpd.nalog.ru/api/v1/receipt/123/own/print"
	own := seed(42, "own-payment", link)
	foreign := seed(43, "foreign-payment", "https://lknpd.nalog.ru/api/v1/receipt/123/foreign/print")
	for _, path := range []string{fmt.Sprintf("/api/orders/%d", own), "/api/orders"} {
		response := f.request(t, http.MethodGet, path, "", true)
		if response.Code != 200 || !strings.Contains(response.Body.String(), link) || strings.Contains(response.Body.String(), "foreign/print") || strings.Contains(response.Body.String(), "own-payment") {
			t.Fatalf("receipt response: %d %s", response.Code, response.Body.String())
		}
	}
	response := f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", foreign), "", true)
	if response.Code != 404 || strings.Contains(response.Body.String(), "foreign/print") {
		t.Fatal("foreign receipt exposed")
	}
	response = f.request(t, http.MethodGet, fmt.Sprintf("/api/orders/%d", own), "", false)
	if response.Code != 401 {
		t.Fatal("unauthenticated receipt exposed")
	}
}
