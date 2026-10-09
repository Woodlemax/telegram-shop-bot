package storage

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTaxReceiptPersistenceBindingAndDeliveryLease(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "receipts.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	orders := NewSQLOrderStore(db)
	seed := func(user int64, paymentID string) int64 {
		id, err := orders.CreateOrder(ctx, &Order{UserID: user, Status: OrderStatusPending, TotalRUB: 100}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = orders.UpdateOrderStatus(ctx, id, OrderStatusPending, OrderStatusPaid, PaymentMethodYooKassa, paymentID); err != nil {
			t.Fatal(err)
		}
		return id
	}
	id := seed(777, "payment-one")
	other := seed(888, "payment-two")
	s := NewOrderTaxReceiptStore(db.Conn())
	link := "https://lknpd.nalog.ru/api/v1/receipt/123/example/print"
	r, err := s.Attach(ctx, id, 9001, link)
	if err != nil || r.UserID != 777 || r.PaymentID != "payment-one" {
		t.Fatalf("binding: %+v %v", r, err)
	}
	if _, err = s.Attach(ctx, id, 9001, link+"?different=1"); !errors.Is(err, ErrReceiptConflict) {
		t.Fatalf("replacement: %v", err)
	}
	if _, err = s.Attach(ctx, other, 9001, link); err == nil {
		t.Fatal("receipt reused for another order")
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.ClaimDelivery(ctx, id, "token")
			if err != nil {
				t.Error(err)
			}
			if ok {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("duplicate send claims: %d", winners.Load())
	}
	if err = s.FinishDelivery(ctx, id, "wrong-token", 0, false); !errors.Is(err, ErrOrderStatusConflict) {
		t.Fatalf("unowned completion: %v", err)
	}
	if err = s.FinishDelivery(ctx, id, "token", 0, false); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	s = NewOrderTaxReceiptStore(db.Conn())
	orders = NewSQLOrderStore(db)
	r, err = s.Get(ctx, id)
	if err != nil || r.DeliveryStatus != "failed" {
		t.Fatalf("restart lost receipt: %+v %v", r, err)
	}
	ok, err := s.ClaimDelivery(ctx, id, "retry")
	if err != nil || !ok {
		t.Fatalf("retry claim: %v %v", ok, err)
	}
	if err = s.FinishDelivery(ctx, id, "retry", 42, true); err != nil {
		t.Fatal(err)
	}
	ok, err = s.ClaimDelivery(ctx, id, "duplicate")
	if err != nil || ok {
		t.Fatal("sent receipt claimed again")
	}
	o, err := orders.GetOrder(ctx, id)
	if err != nil || o.TaxReceipt == nil || o.TaxReceipt.MessageID != 42 {
		t.Fatalf("order receipt: %+v %v", o, err)
	}
	list, _, err := orders.GetUserOrdersPaged(ctx, 777, 10, 0)
	if err != nil || len(list) != 1 || list[0].TaxReceipt == nil {
		t.Fatalf("history receipt: %+v %v", list, err)
	}
	// An expired process lease can be recovered after a restart.
	if _, err = db.Conn().Exec(`UPDATE order_tax_receipts SET delivery_status='sending',delivery_token='old',delivery_lease_until=1 WHERE order_id=?`, id); err != nil {
		t.Fatal(err)
	}
	ok, err = s.ClaimDelivery(ctx, id, "recovered")
	if err != nil || !ok {
		t.Fatal("expired lease not recovered")
	}
}
