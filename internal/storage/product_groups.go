package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrInvalidProductGroup = errors.New("invalid product group")

type ProductGroupStore struct{ db *sql.DB }

func NewProductGroupStore(db *sql.DB) *ProductGroupStore { return &ProductGroupStore{db: db} }

type ProductGroupRow struct {
	ID                int64
	ModificationCount int
}

func catalogVisible(alias string) string {
	return alias + `.is_active=1 AND ` + alias + `.deleted_at IS NULL AND (` + alias + `.stock>0 OR ` + alias + `.infinite_stock=1 OR ` + alias + `.coming_soon=1)`
}

func (s *ProductGroupStore) SetParent(ctx context.Context, productID, parentID int64) error {
	if productID <= 0 || parentID < 0 || productID == parentID {
		return ErrInvalidProductGroup
	}
	if parentID == 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM product_modifications WHERE product_id=?`, productID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO product_modifications(product_id,parent_id) VALUES(?,?) ON CONFLICT(product_id) DO UPDATE SET parent_id=excluded.parent_id`, productID, parentID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProductGroup, err)
	}
	return nil
}
func (s *ProductGroupStore) Parent(ctx context.Context, productID int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT parent_id FROM product_modifications WHERE product_id=?`, productID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// Count/page roots, never modifications, so a group cannot split across pages.
// If the main model becomes unavailable or changes category, its visible
// modifications become ordinary cards instead of disappearing from the shop.
func (s *ProductGroupStore) ListRoots(ctx context.Context, categoryID int64, limit, offset int) ([]ProductGroupRow, int, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, 0, ErrInvalidProductGroup
	}
	where := `p.category_id=? AND ` + catalogVisible("p") + ` AND NOT EXISTS(
 SELECT 1 FROM product_modifications m JOIN products main ON main.id=m.parent_id
 WHERE m.product_id=p.id AND main.category_id=p.category_id AND ` + catalogVisible("main") + `)`
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM products p WHERE `+where, categoryID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,(SELECT COUNT(*) FROM product_modifications m JOIN products child ON child.id=m.product_id
 WHERE m.parent_id=p.id AND child.category_id=p.category_id AND `+catalogVisible("child")+`) FROM products p WHERE `+where+` ORDER BY p.id LIMIT ? OFFSET ?`, categoryID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]ProductGroupRow, 0)
	for rows.Next() {
		var g ProductGroupRow
		if err = rows.Scan(&g.ID, &g.ModificationCount); err != nil {
			return nil, 0, err
		}
		out = append(out, g)
	}
	return out, total, rows.Err()
}
func (s *ProductGroupStore) ListModifications(ctx context.Context, parentID int64, limit, offset int) ([]int64, int, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, 0, ErrInvalidProductGroup
	}
	where := `m.parent_id=? AND child.category_id=main.category_id AND ` + catalogVisible("child") + ` AND ` + catalogVisible("main")
	from := ` FROM product_modifications m JOIN products child ON child.id=m.product_id JOIN products main ON main.id=m.parent_id WHERE ` + where
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, parentID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT child.id`+from+` ORDER BY child.id LIMIT ? OFFSET ?`, parentID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, 0, err
		}
		out = append(out, id)
	}
	return out, total, rows.Err()
}
