package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckoutProviderSurvivesReopenAndRejectsOtherRails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shop.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := NewSQLOrderStore(db)
	id, err := store.CreateOrder(ctx, &Order{UserID: 42, Status: OrderStatusPending, TotalStars: 100, TotalRUB: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ClaimCheckoutProvider(ctx, id, PaymentMethodStars); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store = NewSQLOrderStore(db)
	if err = store.ClaimCheckoutProvider(ctx, id, PaymentMethodYooKassa); !errors.Is(err, ErrCheckoutProviderConflict) {
		t.Fatalf("reservation lost: %v", err)
	}
	if err = store.ClaimCheckoutProvider(ctx, id, PaymentMethodStars); err != nil {
		t.Fatal(err)
	}
	o, err := store.GetOrder(ctx, id)
	if err != nil || o.CheckoutProvider != PaymentMethodStars {
		t.Fatalf("selected method %v %v", o, err)
	}
	if _, err = db.Conn().Exec(`UPDATE orders SET checkout_provider='' WHERE id=?`, id); err == nil {
		t.Fatal("reservation cleared behind active invoice")
	}
	if err = store.CancelOrder(ctx, id, 42); err != nil {
		t.Fatal(err)
	}
	if err = store.ClaimCheckoutProvider(ctx, id, PaymentMethodStars); !errors.Is(err, ErrOrderStatusConflict) {
		t.Fatalf("cancelled order payable: %v", err)
	}
}

func TestCheckoutProviderMigrationBackfillsExistingYooKassaIntent(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	store := NewSQLOrderStore(db)
	id, err := store.CreateOrder(ctx, &Order{UserID: 42, Status: OrderStatusPending, TotalRUB: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-migration schema and an already-issued pending URL.
	if _, err = db.Conn().Exec(`DROP TRIGGER orders_checkout_provider_immutable; ALTER TABLE orders DROP COLUMN checkout_provider;
 DELETE FROM schema_migrations WHERE version='032_checkout_provider.sql';
 INSERT INTO yookassa_checkout_intents(order_id,shop_id,request_key,amount_minor,description,return_url,payment_id,pay_url,created_at) VALUES(?, 'shop','key',10000,'Plane','https://t.me/test','old-payment','https://yoomoney.ru/test',?)`, id, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err = db.migrate(); err != nil {
		t.Fatal(err)
	}
	if err = store.ClaimCheckoutProvider(ctx, id, PaymentMethodStars); !errors.Is(err, ErrCheckoutProviderConflict) {
		t.Fatalf("legacy card page allowed Stars: %v", err)
	}
}
