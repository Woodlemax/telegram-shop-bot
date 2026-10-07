package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

var ErrDigitalArchiveNotReady = errors.New("digital archive unavailable or invalid quantity")

type DigitalArchive struct {
	ID, ProductID    int64
	FileID, FileName string
	SizeBytes        int64
}

type DigitalDelivery struct {
	ID, OrderID, ProductID, UserID, ArchiveID int64
	FileID, FileName, ProductName, LeaseToken string
	Attempts                                  int
}

type DigitalArchiveStore struct{ db *sql.DB }

func NewDigitalArchiveStore(db *DB) *DigitalArchiveStore { return &DigitalArchiveStore{db.Conn()} }

// Add stores the new version and updates existing purchases to this archive.
// Files remain on Telegram; no getFile or
// download is performed, including for archives larger than 50 MB.
func (s *DigitalArchiveStore) Add(ctx context.Context, productID int64, fileID, name string, size int64) (int64, error) {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if size <= 0 || fileID == "" || len(fileID) > 4096 || len(name) > 255 ||
		!strings.HasSuffix(strings.ToLower(name), ".zip") || strings.ContainsFunc(name, unicode.IsControl) {
		return 0, ErrDigitalArchiveNotReady
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE products SET is_digital=1, digital_content='', stock=MAX(stock,1)
        WHERE id=? AND COALESCE(sub_period_days,0)=0`, productID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, ErrNotFound
	}
	res, err = tx.ExecContext(ctx, `INSERT INTO digital_archives(product_id,file_id,file_name,size_bytes) VALUES(?,?,?,?)`, productID, fileID, name, size)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE digital_deliveries SET archive_id=?,
        state=CASE WHEN state='sending' THEN 'pending' ELSE state END,
        lease_token='',lease_until=0,available_at=0 WHERE product_id=?`, id, productID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *DigitalArchiveStore) BeginUpload(ctx context.Context, adminID, chatID, productID int64) error {
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM products WHERE id=? AND COALESCE(sub_period_days,0)=0`, productID).Scan(&id); err != nil {
		return ErrNotFound
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO digital_uploads(admin_id,chat_id,product_id,expires_at) VALUES(?,?,?,?)
        ON CONFLICT(admin_id) DO UPDATE SET chat_id=excluded.chat_id,product_id=excluded.product_id,expires_at=excluded.expires_at`, adminID, chatID, productID, time.Now().Add(15*time.Minute).Unix())
	return err
}

func (s *DigitalArchiveStore) PendingUpload(ctx context.Context, adminID, chatID int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT product_id FROM digital_uploads WHERE admin_id=? AND chat_id=? AND expires_at>?`, adminID, chatID, time.Now().Unix()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *DigitalArchiveStore) CancelUpload(ctx context.Context, adminID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM digital_uploads WHERE admin_id=?`, adminID)
	return err
}

// snapshotDigitalArchive creates the durable delivery in the order transaction.
// Replacing the product archive also updates these rows for existing purchases.
func snapshotDigitalArchive(ctx context.Context, tx *sql.Tx, orderID int64, item OrderItem) error {
	var digital bool
	err := tx.QueryRowContext(ctx, `SELECT is_digital FROM products WHERE id=?`, item.ProductID).Scan(&digital)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !digital {
		return nil
	}
	if item.Quantity != 1 {
		return ErrDigitalArchiveNotReady
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO digital_deliveries(order_id,product_id,archive_id)
        SELECT ?,?,id FROM digital_archives WHERE product_id=? ORDER BY id DESC LIMIT 1`, orderID, item.ProductID, item.ProductID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrDigitalArchiveNotReady
	}
	return nil
}

const digitalEntitlement = `o.order_state IN ('placed','completed') AND o.status IN ('paid','delivered') AND o.payment_state='settled'
    AND EXISTS(SELECT 1 FROM payment_attempts p WHERE p.order_id=o.id AND p.provider=o.payment_method
        AND p.external_id=o.payment_id AND p.status='succeeded')`

// Claim is atomic across workers. Expired leases recover interrupted sends.
func (s *DigitalArchiveStore) Claim(ctx context.Context) (*DigitalDelivery, error) {
	token := uuid.NewString()
	var id int64
	err := s.db.QueryRowContext(ctx, `UPDATE digital_deliveries SET state='sending',lease_until=unixepoch()+120,
        lease_token=?,attempts=attempts+1 WHERE id=(
        SELECT d.id FROM digital_deliveries d JOIN orders o ON o.id=d.order_id WHERE `+digitalEntitlement+`
        AND ((d.state='pending' AND d.available_at<=unixepoch()) OR (d.state='sending' AND d.lease_until<=unixepoch()))
        ORDER BY d.id LIMIT 1) RETURNING id`, token).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	d.LeaseToken = token
	return d, nil
}

func (s *DigitalArchiveStore) get(ctx context.Context, id int64) (*DigitalDelivery, error) {
	var d DigitalDelivery
	err := s.db.QueryRowContext(ctx, `SELECT d.id,d.order_id,d.product_id,o.user_id,d.archive_id,a.file_id,a.file_name,
        COALESCE(i.product_name,''),d.attempts FROM digital_deliveries d JOIN orders o ON o.id=d.order_id
        JOIN digital_archives a ON a.id=d.archive_id JOIN order_items i ON i.order_id=d.order_id AND i.product_id=d.product_id
        WHERE d.id=?`, id).Scan(&d.ID, &d.OrderID, &d.ProductID, &d.UserID, &d.ArchiveID, &d.FileID, &d.FileName, &d.ProductName, &d.Attempts)
	return &d, err
}

func (s *DigitalArchiveStore) Finish(ctx context.Context, d *DigitalDelivery, messageID int, success bool) error {
	if success {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		res, err := tx.ExecContext(ctx, `UPDATE digital_deliveries SET state='sent',message_id=?,lease_until=0 WHERE id=? AND lease_token=? AND state='sending'`, messageID, d.ID, d.LeaseToken)
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
		res, err = tx.ExecContext(ctx, `UPDATE orders SET status='delivered',order_state='completed',fulfillment_state='fulfilled',updated_at=CURRENT_TIMESTAMP
		 WHERE id=? AND status='paid' AND payment_state='settled'
		 AND NOT EXISTS(SELECT 1 FROM digital_deliveries WHERE order_id=? AND state<>'sent')
		 AND NOT EXISTS(SELECT 1 FROM order_items i WHERE i.order_id=? AND NOT EXISTS(
		 SELECT 1 FROM digital_deliveries d WHERE d.order_id=i.order_id AND d.product_id=i.product_id))`, d.OrderID, d.OrderID, d.OrderID)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 {
			if err := appendOrderEvent(ctx, tx, d.OrderID, "fulfillment.fulfilled", "paid", "delivered"); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	delay := min(3600, 30*(1<<min(d.Attempts, 7)))
	_, err := s.db.ExecContext(ctx, `UPDATE digital_deliveries SET state='pending',lease_until=0,available_at=unixepoch()+? WHERE id=? AND lease_token=? AND state='sending'`, delay, d.ID, d.LeaseToken)
	return err
}

// Owned denies forged callbacks, unpaid orders, and refunded/review payments.
func (s *DigitalArchiveStore) Owned(ctx context.Context, userID, deliveryID int64) (*DigitalDelivery, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT d.id FROM digital_deliveries d JOIN orders o ON o.id=d.order_id
        WHERE d.id=? AND o.user_id=? AND `+digitalEntitlement, deliveryID, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.get(ctx, id)
}

func (s *DigitalArchiveStore) Library(ctx context.Context, userID int64) ([]DigitalDelivery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id FROM digital_deliveries d JOIN orders o ON o.id=d.order_id
        WHERE o.user_id=? AND `+digitalEntitlement+` ORDER BY d.id DESC LIMIT 50`, userID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	items := make([]DigitalDelivery, 0, len(ids))
	for _, id := range ids {
		d, err := s.get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("digital library: %w", err)
		}
		items = append(items, *d)
	}
	return items, nil
}
