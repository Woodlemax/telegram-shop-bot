package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

var ErrAdminEditConflict = errors.New("product changed; reload before saving")

// AdminProductGroups describes both sides of existing links, including hidden models.
func (s *SQLProductStore) AdminProductGroups(ctx context.Context) (map[int64]int64, map[int64]int, error) {
	parents, counts := make(map[int64]int64), make(map[int64]int)
	rows, err := s.db.QueryContext(ctx, `SELECT m.product_id,m.parent_id FROM product_modifications m
 JOIN products child ON child.id=m.product_id JOIN products main ON main.id=m.parent_id
 WHERE child.deleted_at IS NULL AND main.deleted_at IS NULL`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var child, parent int64
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, nil, err
		}
		parents[child] = parent
		counts[parent]++
	}
	return parents, counts, rows.Err()
}

// ProductRevision also covers the grouping, which is saved together with the product.
func ProductRevision(p *Product, parent int64) string {
	data, _ := json.Marshal(struct {
		Product *Product
		Parent  int64
	}{p, parent})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (s *SQLProductStore) ListCategoriesAdmin(ctx context.Context) ([]Category, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,COALESCE(emoji,''),COALESCE(custom_emoji_id,''),sort_order,is_active FROM categories ORDER BY sort_order,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Category, 0)
	for rows.Next() {
		var c Category
		if err = rows.Scan(&c.ID, &c.Name, &c.Emoji, &c.CustomEmojiID, &c.SortOrder, &c.IsActive); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveAdminProduct prevents partial saves and stale browser tabs overwriting inventory.
func (s *SQLProductStore) SaveAdminProduct(ctx context.Context, p *Product, parent int64, revision string) (int64, error) {
	if strings.TrimSpace(p.Name) == "" || len([]rune(p.Name)) > 200 || len([]rune(p.Description)) > 6000 || p.Stock < 0 || p.Stock > 1000000000 || parent < 0 || (p.SubPeriodDays != 0 && p.SubPeriodDays != 30) || (p.PriceRUB == nil && !p.IsAuthorSale()) {
		return 0, ErrInvalidMoney
	}
	if err := validateProduct(p); err != nil {
		return 0, err
	}
	if p.SubPeriodDays > 0 && (p.PriceRUB == nil || *p.PriceRUB <= 0 || p.PriceStars <= 0) {
		return 0, ErrInvalidMoney
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var category int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM categories WHERE id=?`, p.CategoryID).Scan(&category); err != nil {
		return 0, ErrNotFound
	}
	if p.ID > 0 {
		var old Product
		err = tx.QueryRowContext(ctx, `SELECT id,category_id,name,COALESCE(description,''),COALESCE(photo_url,''),price_usd,COALESCE(price_stars,0),stock,is_digital,COALESCE(digital_content,''),is_active,sub_period_days,open_price,price_rub,infinite_stock,single_in_cart,telegram_url,coming_soon,author_telegram_url,created_at FROM products WHERE id=? AND deleted_at IS NULL`, p.ID).Scan(&old.ID, &old.CategoryID, &old.Name, &old.Description, &old.PhotoURL, &old.PriceUSD, &old.PriceStars, &old.Stock, &old.IsDigital, &old.DigitalContent, &old.IsActive, &old.SubPeriodDays, &old.OpenPrice, &old.PriceRUB, &old.InfiniteStock, &old.SingleInCart, &old.TelegramURL, &old.ComingSoon, &old.AuthorTelegramURL, &old.CreatedAt)
		if err != nil {
			return 0, ErrNotFound
		}
		var oldParent int64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT parent_id FROM product_modifications WHERE product_id=?),0)`, p.ID).Scan(&oldParent); err != nil {
			return 0, err
		}
		if revision == "" || revision != ProductRevision(&old, oldParent) {
			return 0, ErrAdminEditConflict
		}
		p.IsDigital, p.DigitalContent = old.IsDigital, old.DigitalContent
		if p.IsDigital && p.SubPeriodDays > 0 {
			return 0, ErrInvalidMoney
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM product_modifications WHERE product_id=?`, p.ID); err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE products SET category_id=?,name=?,description=?,price_usd=?,price_stars=?,stock=?,is_active=?,sub_period_days=?,open_price=?,price_rub=?,infinite_stock=?,single_in_cart=?,telegram_url=?,coming_soon=?,author_telegram_url=? WHERE id=? AND deleted_at IS NULL`, p.CategoryID, p.Name, p.Description, p.PriceUSD, p.PriceStars, p.Stock, p.IsActive, p.SubPeriodDays, p.OpenPrice, p.PriceRUB, p.InfiniteStock, p.SingleInCart, p.TelegramURL, p.ComingSoon, p.AuthorTelegramURL, p.ID)
	} else {
		p.IsDigital = false
		p.DigitalContent = ""
		p.PhotoURL = ""
		res, e := tx.ExecContext(ctx, `INSERT INTO products(category_id,name,description,price_usd,price_stars,stock,is_active,sub_period_days,open_price,price_rub,infinite_stock,single_in_cart,telegram_url,coming_soon,author_telegram_url) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.CategoryID, p.Name, p.Description, p.PriceUSD, p.PriceStars, p.Stock, p.IsActive, p.SubPeriodDays, p.OpenPrice, p.PriceRUB, p.InfiniteStock, p.SingleInCart, p.TelegramURL, p.ComingSoon, p.AuthorTelegramURL)
		if e != nil {
			return 0, e
		}
		p.ID, err = res.LastInsertId()
	}
	if err != nil {
		return 0, err
	}
	if parent > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO product_modifications(product_id,parent_id) VALUES(?,?)`, p.ID, parent); err != nil {
			return 0, ErrInvalidProductGroup
		}
	}
	var invalid int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM product_modifications m JOIN products c ON c.id=m.product_id JOIN products p ON p.id=m.parent_id WHERE (m.product_id=? OR m.parent_id=?) AND c.category_id<>p.category_id`, p.ID, p.ID).Scan(&invalid); err != nil {
		return 0, err
	}
	if invalid > 0 {
		return 0, ErrInvalidProductGroup
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return p.ID, nil
}

// Photos are replaced in one transaction; the first is always the cover.
func (s *SQLProductStore) SetAdminPhotos(ctx context.Context, id int64, photos []string) error {
	if len(photos) > MaxProductPhotos {
		return ErrTooManyPhotos
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cover := ""
	if len(photos) > 0 {
		cover = photos[0]
	}
	res, err := tx.ExecContext(ctx, `UPDATE products SET photo_url=? WHERE id=? AND deleted_at IS NULL`, cover, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM product_photos WHERE product_id=?`, id); err != nil {
		return err
	}
	for i, photo := range photos {
		if i == 0 {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO product_photos(product_id,file_id,sort_order) VALUES(?,?,?)`, id, photo, i-1); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *DigitalArchiveStore) Latest(ctx context.Context, productID int64) (*DigitalArchive, error) {
	var a DigitalArchive
	err := s.db.QueryRowContext(ctx, `SELECT id,product_id,file_id,file_name,size_bytes FROM digital_archives WHERE product_id=? ORDER BY id DESC LIMIT 1`, productID).Scan(&a.ID, &a.ProductID, &a.FileID, &a.FileName, &a.SizeBytes)
	if err != nil {
		return nil, err
	}
	return &a, nil
}
