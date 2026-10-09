package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CheckoutProviderClaimer atomically selects the one payment rail that may
// issue an invoice for this order. Reservations survive failures and restarts.
type CheckoutProviderClaimer interface {
	ClaimCheckoutProvider(context.Context, int64, string) error
}

type checkoutProviderDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func claimCheckoutProvider(ctx context.Context, db checkoutProviderDB, id int64, provider string) error {
	switch provider {
	case PaymentMethodStars, PaymentMethodYooKassa, PaymentMethodCrypto, PaymentMethodStripe, PaymentMethodTON, PaymentMethodNowpayments, PaymentMethodBalance:
	default:
		return ErrPaymentReceiptMismatch
	}
	result, err := db.ExecContext(ctx, `UPDATE orders SET checkout_provider=?
 WHERE id=? AND status='pending' AND payment_state='pending'
 AND checkout_provider IN ('',?)`, provider, id, provider)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	var status, state, chosen string
	err = db.QueryRowContext(ctx, `SELECT status,payment_state,checkout_provider FROM orders WHERE id=?`, id).Scan(&status, &state, &chosen)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != OrderStatusPending || state != PaymentStatePending {
		return ErrOrderStatusConflict
	}
	return ErrCheckoutProviderConflict
}

func (s *SQLOrderStore) ClaimCheckoutProvider(ctx context.Context, id int64, provider string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = claimCheckoutProvider(ctx, s.db, id, provider)
		if !isDatabaseLocked(err) {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}
