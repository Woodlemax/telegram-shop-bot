package storage

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"
)

var ErrInvalidReceiptURL = errors.New("invalid My Tax receipt URL")
var ErrReceiptConflict = errors.New("receipt already attached")

type OrderTaxReceipt struct {
	OrderID        int64
	UserID         int64
	PaymentID      string
	URL            string
	AttachedBy     int64
	AttachedAt     time.Time
	DeliveryStatus string
	SentAt         sql.NullTime
	MessageID      int
}

type OrderTaxReceiptStore struct{ db *sql.DB }

func NewOrderTaxReceiptStore(db *sql.DB) *OrderTaxReceiptStore { return &OrderTaxReceiptStore{db: db} }

// Only official receipt links are accepted; the server never fetches this URL.
func ValidTaxReceiptURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 2048 && u.Scheme == "https" &&
		strings.EqualFold(u.Hostname(), "lknpd.nalog.ru") && u.User == nil &&
		(u.Port() == "" || u.Port() == "443") && strings.HasPrefix(u.Path, "/api/v1/receipt/") &&
		!strings.ContainsAny(raw, "\r\n\t ")
}

func (s *OrderTaxReceiptStore) Get(ctx context.Context, orderID int64) (*OrderTaxReceipt, error) {
	var r OrderTaxReceipt
	err := s.db.QueryRowContext(ctx, `SELECT order_id,user_id,payment_id,url,attached_by,attached_at,
 delivery_status,sent_at,message_id FROM order_tax_receipts WHERE order_id=?`, orderID).
		Scan(&r.OrderID, &r.UserID, &r.PaymentID, &r.URL, &r.AttachedBy, &r.AttachedAt, &r.DeliveryStatus, &r.SentAt, &r.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Capture the buyer and successful payment together. Repeating the same command
// is idempotent; a receipt already bound to another payment cannot be reused.
func (s *OrderTaxReceiptStore) Attach(ctx context.Context, orderID, adminID int64, receiptURL string) (*OrderTaxReceipt, error) {
	if !ValidTaxReceiptURL(receiptURL) {
		return nil, ErrInvalidReceiptURL
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO order_tax_receipts(order_id,user_id,payment_id,url,attached_by)
 SELECT id,user_id,payment_id,?,? FROM orders WHERE id=? AND status IN ('paid','delivered')
 AND payment_state IN ('settled','partially_refunded') AND payment_method='yookassa' AND COALESCE(payment_id,'')<>''
 ON CONFLICT DO NOTHING`, receiptURL, adminID, orderID)
	if err != nil {
		return nil, err
	}
	r, err := s.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrOrderStatusConflict
	}
	if r.URL != receiptURL {
		return nil, ErrReceiptConflict
	}
	return r, nil
}

// A persisted lease prevents simultaneous admin commands from sending twice.
// After a failed request or restart the same command can retry delivery.
func (s *OrderTaxReceiptStore) ClaimDelivery(ctx context.Context, orderID int64, token string) (bool, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `UPDATE order_tax_receipts SET delivery_status='sending',delivery_token=?,delivery_lease_until=?
 WHERE order_id=? AND delivery_status<>'sent' AND (delivery_status<>'sending' OR delivery_lease_until<?)`, token, now+120, orderID, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
func (s *OrderTaxReceiptStore) FinishDelivery(ctx context.Context, orderID int64, token string, messageID int, sent bool) error {
	status := "failed"
	var sentAt any
	if sent {
		status = "sent"
		sentAt = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx, `UPDATE order_tax_receipts SET delivery_status=?,sent_at=?,message_id=?,delivery_token='',delivery_lease_until=0
 WHERE order_id=? AND delivery_status='sending' AND delivery_token=?`, status, sentAt, messageID, orderID, token)
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
	return nil
}
