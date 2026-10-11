ALTER TABLE products ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0 CHECK(sort_order BETWEEN -1000000000 AND 1000000000);
CREATE INDEX idx_products_catalog_order ON products(category_id, sort_order, id) WHERE deleted_at IS NULL;
