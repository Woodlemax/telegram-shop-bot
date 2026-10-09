package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Checkout intents are mutable provider requests, separate from the immutable
// ledger of money actually received. The request body/key survive crashes.
type YooKassaCheckoutIntent struct {
	OrderID     int64
	ShopID      string
	RequestKey  string
	AmountMinor int64
	Description string
	ReturnURL   string
	PaymentID   string
	PayURL      string
	CreatedAt   time.Time
}
type SQLYooKassaCheckoutStore struct{ db *DB }

func NewSQLYooKassaCheckoutStore(db *DB) *SQLYooKassaCheckoutStore {
	return &SQLYooKassaCheckoutStore{db: db}
}

func (s *SQLYooKassaCheckoutStore) GetOrCreate(ctx context.Context, proposed YooKassaCheckoutIntent) (*YooKassaCheckoutIntent, error) {
	tx, err := s.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = claimCheckoutProvider(ctx, tx, proposed.OrderID, PaymentMethodYooKassa); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO yookassa_checkout_intents
 (order_id,shop_id,request_key,amount_minor,description,return_url,created_at)
 SELECT id,?,?,?,?,?,? FROM orders WHERE id=? AND status='pending' AND payment_state='pending'
 AND CAST(ROUND(total_rub*100) AS INTEGER)=?
 ON CONFLICT(order_id,shop_id) DO NOTHING`,
		proposed.ShopID, proposed.RequestKey, proposed.AmountMinor, proposed.Description, proposed.ReturnURL, proposed.CreatedAt.Unix(), proposed.OrderID, proposed.AmountMinor)
	if err != nil {
		return nil, err
	}
	var result YooKassaCheckoutIntent
	var created int64
	err = tx.QueryRowContext(ctx, `SELECT i.order_id,i.shop_id,i.request_key,i.amount_minor,i.description,i.return_url,i.payment_id,i.pay_url,i.created_at
 FROM yookassa_checkout_intents i JOIN orders o ON o.id=i.order_id
 WHERE i.order_id=? AND i.shop_id=? AND o.status='pending' AND o.payment_state='pending'
 AND i.amount_minor=? AND CAST(ROUND(o.total_rub*100) AS INTEGER)=i.amount_minor`,
		proposed.OrderID, proposed.ShopID, proposed.AmountMinor).Scan(&result.OrderID, &result.ShopID, &result.RequestKey, &result.AmountMinor, &result.Description, &result.ReturnURL, &result.PaymentID, &result.PayURL, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrOrderStatusConflict
	}
	if err != nil {
		return nil, err
	}
	result.CreatedAt = time.Unix(created, 0).UTC()
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &result, nil
}
func (s *SQLYooKassaCheckoutStore) Save(ctx context.Context, intent *YooKassaCheckoutIntent, paymentID, payURL string) error {
	result, err := s.db.conn.ExecContext(ctx, `UPDATE yookassa_checkout_intents SET payment_id=?,pay_url=?
 WHERE order_id=? AND shop_id=? AND request_key=? AND (payment_id='' OR payment_id=?)
 AND EXISTS(SELECT 1 FROM orders WHERE id=? AND status='pending' AND payment_state='pending' AND checkout_provider='yookassa')`,
		paymentID, payURL, intent.OrderID, intent.ShopID, intent.RequestKey, paymentID, intent.OrderID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrOrderStatusConflict
	}
	return nil
}

// Rotate is a CAS: only a caller that verified this exact payment canceled
// can replace its request. Concurrent callers subsequently see the winner.
func (s *SQLYooKassaCheckoutStore) Rotate(ctx context.Context, intent *YooKassaCheckoutIntent, key string, now time.Time) (bool, error) {
	result, err := s.db.conn.ExecContext(ctx, `UPDATE yookassa_checkout_intents SET request_key=?,payment_id='',pay_url='',created_at=?
 WHERE order_id=? AND shop_id=? AND request_key=? AND payment_id=?
 AND EXISTS(SELECT 1 FROM orders WHERE id=? AND status='pending' AND payment_state='pending' AND checkout_provider='yookassa')`,
		key, now.Unix(), intent.OrderID, intent.ShopID, intent.RequestKey, intent.PaymentID, intent.OrderID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}
