package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalidPromo  = errors.New("storage: invalid promo")
	ErrPromoExists   = errors.New("storage: promo already exists")
	promoCodePattern = regexp.MustCompile(`^[A-Z0-9_-]{1,32}$`)
)

// ValidatePromo protects both administrator input and internal writers.
func ValidatePromo(p *PromoCode) error {
	if p == nil || !promoCodePattern.MatchString(p.Code) || p.Discount < 1 || p.Discount > 100 || p.MaxUses < 0 || (p.CategoryID != nil && *p.CategoryID <= 0) || (p.BoundUserID != nil && *p.BoundUserID <= 0) {
		return ErrInvalidPromo
	}
	return nil
}

// SQLite may return either a time.Time or a legacy text date. Normalize both
// using their actual offset instead of comparing local dates lexically.
type promoTime struct{ sql.NullTime }

func (p *promoTime) Scan(value any) error {
	p.NullTime = sql.NullTime{}
	if value == nil {
		return nil
	}
	if stamp, ok := value.(time.Time); ok {
		p.Time, p.Valid = stamp.UTC(), true
		return nil
	}
	var text string
	switch v := value.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return fmt.Errorf("promo store: invalid expiry type")
	}
	layouts := []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02"}
	for _, layout := range layouts {
		if stamp, err := time.Parse(layout, text); err == nil {
			p.Time, p.Valid = stamp.UTC(), true
			return nil
		}
	}
	// Older modernc writers used time.Time.String(): date, time, numeric offset,
	// and an arbitrary zone name. The numeric offset is authoritative.
	fields := strings.Fields(text)
	if len(fields) >= 3 {
		if stamp, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", strings.Join(fields[:3], " ")); err == nil {
			p.Time, p.Valid = stamp.UTC(), true
			return nil
		}
	}
	return fmt.Errorf("promo store: invalid expiry date")
}

// SQLPromoStore implements PromoStore using a *sql.DB connection.
type SQLPromoStore struct {
	db *sql.DB
}

// NewSQLPromoStore creates a new SQLPromoStore from the given DB.
func NewSQLPromoStore(d *DB) *SQLPromoStore {
	return &SQLPromoStore{db: d.Conn()}
}

// GetPromoByCode returns an active, valid promo code. Returns ErrNotFound if
// no matching code exists or it has expired / been exhausted.
func (s *SQLPromoStore) GetPromoByCode(ctx context.Context, code string) (*PromoCode, error) {
	var p PromoCode
	var expiresAt promoTime
	var categoryID sql.NullInt64
	var boundUserID sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, code, discount, max_uses, used_count, expires_at, is_active, created_at, category_id, bound_user_id
		 FROM promo_codes
		 WHERE code = ? AND is_active = 1
		   AND discount BETWEEN 1 AND 100
		   AND (max_uses = 0 OR used_count < max_uses)`,
		strings.ToUpper(strings.TrimSpace(code)),
	).Scan(&p.ID, &p.Code, &p.Discount, &p.MaxUses, &p.UsedCount,
		&expiresAt, &p.IsActive, &p.CreatedAt, &categoryID, &boundUserID)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("promo store: get promo: %w", err)
	}
	if expiresAt.Valid {
		t := expiresAt.Time
		p.ExpiresAt = &t
		if !time.Now().Before(t) {
			return nil, ErrNotFound
		}
	}
	if categoryID.Valid {
		p.CategoryID = &categoryID.Int64
	}
	if boundUserID.Valid {
		p.BoundUserID = &boundUserID.Int64
	}
	return &p, nil
}

// UsePromo records that a user has used the promo code for an order and
// increments the usage counter.
func (s *SQLPromoStore) UsePromo(ctx context.Context, promoID, userID, orderID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("promo store: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO promo_usages (promo_id, user_id, order_id) VALUES (?, ?, ?)`,
		promoID, userID, orderID,
	); err != nil {
		return fmt.Errorf("promo store: insert promo usage: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE promo_codes SET used_count = used_count + 1 WHERE id = ?`, promoID,
	); err != nil {
		return fmt.Errorf("promo store: increment used_count: %w", err)
	}

	return tx.Commit()
}

// HasUserUsedPromo returns true if the user has already used the given promo.
func (s *SQLPromoStore) HasUserUsedPromo(ctx context.Context, promoID, userID int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM promo_usages WHERE promo_id = ? AND user_id = ?`,
		promoID, userID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("promo store: check user usage: %w", err)
	}
	return count > 0, nil
}

// CreatePromo inserts a new promo code and returns its ID.
func (s *SQLPromoStore) CreatePromo(ctx context.Context, p *PromoCode) (int64, error) {
	if p == nil {
		return 0, ErrInvalidPromo
	}
	copyPromo := *p
	copyPromo.Code = strings.ToUpper(strings.TrimSpace(copyPromo.Code))
	p = &copyPromo
	if err := ValidatePromo(p); err != nil {
		return 0, err
	}
	var expires any
	if p.ExpiresAt != nil {
		expires = p.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO promo_codes (code, discount, max_uses, expires_at, category_id, bound_user_id) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(code) DO NOTHING`,
		p.Code, p.Discount, p.MaxUses, expires, p.CategoryID, p.BoundUserID,
	)
	if err != nil {
		return 0, fmt.Errorf("promo store: create promo: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return 0, err
	} else if n == 0 {
		return 0, ErrPromoExists
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("promo store: last insert id: %w", err)
	}
	return id, nil
}

// CreatePersonal issues a single-use promo code bound to one Telegram user,
// valid for validDays days from now. Bound promos are invisible to other users
// at validation time (see PromoCode.BoundUserID).
func (s *SQLPromoStore) CreatePersonal(ctx context.Context, code string, discountPct int, boundUserID int64, validDays int) error {
	if validDays < 1 || validDays > 36500 {
		return ErrInvalidPromo
	}
	expiresAt := time.Now().UTC().AddDate(0, 0, validDays)
	_, err := s.CreatePromo(ctx, &PromoCode{Code: code, Discount: discountPct, MaxUses: 1, ExpiresAt: &expiresAt, BoundUserID: &boundUserID})
	return err
}

// ListPromos returns all active promo codes ordered by creation date.
func (s *SQLPromoStore) ListPromos(ctx context.Context) ([]PromoCode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, code, discount, max_uses, used_count, expires_at, is_active, created_at, category_id, bound_user_id
		 FROM promo_codes WHERE is_active = 1 ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("promo store: list promos: %w", err)
	}
	defer rows.Close()

	var promos []PromoCode
	for rows.Next() {
		var p PromoCode
		var expiresAt promoTime
		var categoryID sql.NullInt64
		var boundUserID sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Code, &p.Discount, &p.MaxUses, &p.UsedCount,
			&expiresAt, &p.IsActive, &p.CreatedAt, &categoryID, &boundUserID); err != nil {
			return nil, fmt.Errorf("promo store: scan promo: %w", err)
		}
		if expiresAt.Valid {
			t := expiresAt.Time
			p.ExpiresAt = &t
			if !time.Now().Before(t) {
				return nil, ErrNotFound
			}
		}
		if categoryID.Valid {
			p.CategoryID = &categoryID.Int64
		}
		if boundUserID.Valid {
			p.BoundUserID = &boundUserID.Int64
		}
		promos = append(promos, p)
	}
	return promos, rows.Err()
}

// DeactivatePromo marks a promo code as inactive.
func (s *SQLPromoStore) DeactivatePromo(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE promo_codes SET is_active = 0 WHERE id = ? AND is_active=1`, id)
	if err != nil {
		return fmt.Errorf("promo store: deactivate promo: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}
