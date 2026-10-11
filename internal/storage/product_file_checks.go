package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const MaxVerificationFileBytes int64 = 128 << 20

var ErrFileVerificationRequired = errors.New("storage: current reference file verification required")
var ErrFileVerificationMismatch = errors.New("storage: verification file does not match the reference")
var ErrInvalidFileRequirement = errors.New("storage: configure instructions and a reference before enabling verification")

// Reference hashes never leave the server, including administrative JSON responses.
type ProductFileRequirement struct {
	ProductID       int64  `json:"product_id"`
	Enabled         bool   `json:"enabled"`
	Description     string `json:"description"`
	ReferenceSHA256 string `json:"-"`
	ReferenceName   string `json:"reference_name"`
	ReferenceBytes  int64  `json:"reference_bytes"`
	Version         int64  `json:"version"`
	Revision        int64  `json:"revision"`
}

type ProductFileStatus struct {
	Required      bool   `json:"required"`
	Verified      bool   `json:"verified"`
	Description   string `json:"description,omitempty"`
	ReferenceName string `json:"reference_name,omitempty"`
	Version       int64  `json:"version,omitempty"`
	MaxBytes      int64  `json:"max_bytes,omitempty"`
}

type ProductFileCheckStore struct{ db *sql.DB }

func NewProductFileCheckStore(db *sql.DB) *ProductFileCheckStore {
	return &ProductFileCheckStore{db: db}
}

type fileCheckQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const productFileAccessSQL = `NOT EXISTS (
 SELECT 1 FROM product_file_requirements r WHERE r.product_id=? AND r.enabled=1
 AND NOT EXISTS (SELECT 1 FROM product_file_verifications v
 WHERE v.product_id=r.product_id AND v.user_id=? AND v.version=r.version))`

func checkProductFileAccess(ctx context.Context, q fileCheckQuery, userID, productID int64) error {
	var allowed bool
	if err := q.QueryRowContext(ctx, `SELECT `+productFileAccessSQL, productID, userID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrFileVerificationRequired
	}
	return nil
}

func (s *ProductFileCheckStore) Requirement(ctx context.Context, id int64) (*ProductFileRequirement, error) {
	r := &ProductFileRequirement{ProductID: id}
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(r.enabled,0),COALESCE(r.description,''),COALESCE(r.reference_sha256,''),COALESCE(r.reference_name,''),COALESCE(r.reference_bytes,0),COALESCE(r.version,0),COALESCE(r.revision,0)
 FROM products p LEFT JOIN product_file_requirements r ON r.product_id=p.id WHERE p.id=? AND p.deleted_at IS NULL`, id).Scan(&r.Enabled, &r.Description, &r.ReferenceSHA256, &r.ReferenceName, &r.ReferenceBytes, &r.Version, &r.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *ProductFileCheckStore) Status(ctx context.Context, userID, id int64) (*ProductFileStatus, error) {
	r, err := s.Requirement(ctx, id)
	if err != nil {
		return nil, err
	}
	status := &ProductFileStatus{Required: r.Enabled, Verified: true}
	if !r.Enabled {
		return status, nil
	}
	status.Description, status.ReferenceName, status.Version, status.MaxBytes = r.Description, r.ReferenceName, r.Version, MaxVerificationFileBytes
	err = checkProductFileAccess(ctx, s.db, userID, id)
	if errors.Is(err, ErrFileVerificationRequired) {
		status.Verified = false
		return status, nil
	}
	return status, err
}

func (s *ProductFileCheckStore) Save(ctx context.Context, id int64, enabled bool, description string, revision int64) (*ProductFileRequirement, error) {
	description = strings.TrimSpace(description)
	if len(description) > 8000 {
		return nil, ErrInvalidFileRequirement
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var hash, author string
	var sub int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(r.reference_sha256,''),p.author_telegram_url,p.sub_period_days FROM products p LEFT JOIN product_file_requirements r ON r.product_id=p.id WHERE p.id=? AND p.deleted_at IS NULL`, id).Scan(&hash, &author, &sub)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if enabled && (hash == "" || description == "" || author != "" || sub > 0) {
		return nil, ErrInvalidFileRequirement
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO product_file_requirements(product_id,enabled,description,revision) SELECT ?,?,?,1 WHERE ?=0
 ON CONFLICT(product_id) DO UPDATE SET enabled=excluded.enabled,description=excluded.description,revision=product_file_requirements.revision+1,updated_at=CURRENT_TIMESTAMP WHERE product_file_requirements.revision=?`, id, enabled, description, revision, revision)
	// Existing rows with nonzero revisions need an INSERT candidate as well.
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		res, err = tx.ExecContext(ctx, `UPDATE product_file_requirements SET enabled=?,description=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP WHERE product_id=? AND revision=?`, enabled, description, id, revision)
		if err != nil {
			return nil, err
		}
		n, _ = res.RowsAffected()
	}
	if n != 1 {
		return nil, ErrAdminEditConflict
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.Requirement(ctx, id)
}

func (s *ProductFileCheckStore) ReplaceReference(ctx context.Context, id int64, name, hash string, size, revision int64) (*ProductFileRequirement, error) {
	if len(hash) != 64 || size <= 0 || size > MaxVerificationFileBytes || name == "" || len(name) > 255 || strings.ContainsAny(name, "\r\n\x00") {
		return nil, ErrInvalidFileRequirement
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var found int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM products WHERE id=? AND deleted_at IS NULL`, id).Scan(&found); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO product_file_requirements(product_id,reference_sha256,reference_name,reference_bytes,version,revision) SELECT ?,?,?,?,1,1 WHERE ?=0
 ON CONFLICT(product_id) DO UPDATE SET reference_sha256=excluded.reference_sha256,reference_name=excluded.reference_name,reference_bytes=excluded.reference_bytes,version=product_file_requirements.version+CASE WHEN product_file_requirements.reference_sha256<>excluded.reference_sha256 THEN 1 ELSE 0 END,revision=product_file_requirements.revision+1,updated_at=CURRENT_TIMESTAMP WHERE product_file_requirements.revision=?`, id, hash, name, size, revision, revision)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		res, err = tx.ExecContext(ctx, `UPDATE product_file_requirements SET version=version+CASE WHEN reference_sha256<>? THEN 1 ELSE 0 END,reference_sha256=?,reference_name=?,reference_bytes=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP WHERE product_id=? AND revision=?`, hash, hash, name, size, id, revision)
		if err != nil {
			return nil, err
		}
		n, _ = res.RowsAffected()
	}
	if n != 1 {
		return nil, ErrAdminEditConflict
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.Requirement(ctx, id)
}

// The conditional write prevents an upload for an old reference granting new access.
func (s *ProductFileCheckStore) Verify(ctx context.Context, userID, id, version int64, hash string, size int64) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO product_file_verifications(user_id,product_id,version)
 SELECT ?,r.product_id,r.version FROM product_file_requirements r JOIN products p ON p.id=r.product_id
 WHERE r.product_id=? AND r.enabled=1 AND r.version=? AND r.reference_sha256=? AND r.reference_bytes=? AND p.deleted_at IS NULL
 ON CONFLICT(user_id,product_id) DO UPDATE SET version=excluded.version,verified_at=CURRENT_TIMESTAMP`, userID, id, version, hash, size)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrFileVerificationMismatch
	}
	return nil
}

const orderFileAccessSQL = `NOT EXISTS (SELECT 1 FROM order_items i JOIN orders o ON o.id=i.order_id
 JOIN product_file_requirements r ON r.product_id=i.product_id
 WHERE o.id=? AND o.status='pending' AND o.payment_state='pending' AND r.enabled=1
 AND NOT EXISTS(SELECT 1 FROM product_file_verifications v WHERE v.user_id=o.user_id AND v.product_id=r.product_id AND v.version=r.version))`

func (s *SQLOrderStore) CheckOrderFileAccess(ctx context.Context, id int64) error {
	var allowed bool
	if err := s.db.QueryRowContext(ctx, `SELECT `+orderFileAccessSQL, id).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrFileVerificationRequired
	}
	return nil
}

func (s *SQLCartStore) ProductFileStatus(ctx context.Context, userID, id int64) (*ProductFileStatus, error) {
	return NewProductFileCheckStore(s.db).Status(ctx, userID, id)
}
