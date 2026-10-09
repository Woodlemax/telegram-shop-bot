package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *SQLCartStore) SetPrice(ctx context.Context, userID, productID int64, amount int) error {
	if amount < 0 || amount > 1000000 {
		return ErrInvalidMoney
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO cart_items(user_id,product_id,quantity,custom_price)
 SELECT ?,id,1,? FROM products WHERE id=? AND deleted_at IS NULL AND author_telegram_url='' AND open_price=1 AND is_active=1 AND sub_period_days=0 AND NOT(coming_soon=1 AND stock<=0) AND (infinite_stock=1 OR stock>0)
 ON CONFLICT(user_id,product_id) DO UPDATE SET custom_price=excluded.custom_price, quantity=CASE WHEN EXISTS(SELECT 1 FROM products WHERE id=excluded.product_id AND single_in_cart=1) THEN 1 ELSE cart_items.quantity END`, userID, amount, productID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLCartStore) BeginPriceInput(ctx context.Context, userID, chatID, productID int64) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO open_price_inputs(user_id,chat_id,product_id,expires_at)
 SELECT ?,?,id,? FROM products WHERE id=? AND deleted_at IS NULL AND author_telegram_url=''
 ON CONFLICT(user_id) DO UPDATE SET chat_id=excluded.chat_id,product_id=excluded.product_id,expires_at=excluded.expires_at`, userID, chatID, time.Now().Add(15*time.Minute).Unix(), productID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *SQLCartStore) PendingPriceInput(ctx context.Context, userID, chatID int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT product_id FROM open_price_inputs WHERE user_id=? AND chat_id=? AND expires_at>?`, userID, chatID, time.Now().Unix()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
func (s *SQLCartStore) CancelPriceInput(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM open_price_inputs WHERE user_id=?`, userID)
	return err
}

// Free grants are recorded separately from real money receipts. The transaction
// checks committed order totals and uses a compare-and-swap to make replay safe.
func (s *SQLOrderStore) ConfirmFreeOrder(ctx context.Context, id, userID int64) error {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		err := s.confirmFreeOrderOnce(ctx, id, userID)
		if err == nil || !isDatabaseLocked(err) {
			return err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 20 * time.Millisecond):
		}
	}
	return lastErr
}
func (s *SQLOrderStore) confirmFreeOrderOnce(ctx context.Context, id, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var totalUSD, totalRUB float64
	var totalStars int
	var ton, owner, sub int64
	var status, paymentState string
	err = tx.QueryRowContext(ctx, `SELECT user_id,status,payment_state,total_usd,total_stars,total_rub,total_ton_nano,COALESCE(subscription_product_id,0) FROM orders WHERE id=?`, id).
		Scan(&owner, &status, &paymentState, &totalUSD, &totalStars, &totalRUB, &ton, &sub)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrNotFound
	}
	if status != OrderStatusPending || paymentState != PaymentStatePending {
		return ErrOrderStatusConflict
	}
	if totalUSD != 0 || totalRUB != 0 || totalStars != 0 || ton != 0 || sub != 0 {
		return ErrInvalidMoney
	}
	var itemCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_items WHERE order_id=?`, id).Scan(&itemCount); err != nil {
		return err
	}
	if itemCount == 0 {
		return ErrEmptyCart
	}
	res, err := tx.ExecContext(ctx, `UPDATE orders SET status='paid',payment_state='settled',payment_method='free',payment_id=?,updated_at=CURRENT_TIMESTAMP
 WHERE id=? AND user_id=? AND status='pending' AND payment_state='pending'`, fmt.Sprintf("free:%d", id), id, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrOrderStatusConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.product_id,i.quantity FROM order_items i WHERE i.order_id=?`, id)
	if err != nil {
		return err
	}
	type stockItem struct {
		id  int64
		qty int
	}
	var items []stockItem
	for rows.Next() {
		var item stockItem
		if err := rows.Scan(&item.id, &item.qty); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		res, err := tx.ExecContext(ctx, `UPDATE products SET stock=CASE WHEN infinite_stock=1 THEN stock ELSE stock-? END WHERE id=? AND is_active=1 AND (infinite_stock=1 OR stock>=?)`, item.qty, item.id, item.qty)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrProductOutOfStock
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO free_order_grants(order_id) VALUES(?)`, id); err != nil {
		return err
	}
	// A zero-total promo still consumes its one-time usage, just like paid checkout.
	promoRes, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO promo_usages(promo_id,user_id,order_id)
 SELECT p.id,o.user_id,o.id FROM orders o JOIN promo_codes p ON p.code=o.promo_code WHERE o.id=?`, id)
	if err != nil {
		return err
	}
	if changed, err := promoRes.RowsAffected(); err != nil {
		return err
	} else if changed > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE promo_codes SET used_count=used_count+1 WHERE code=(SELECT promo_code FROM orders WHERE id=?)`, id); err != nil {
			return err
		}
	}
	if err := appendOrderEvent(ctx, tx, id, "order.free_granted", "pending", "paid"); err != nil {
		return err
	}
	return tx.Commit()
}
