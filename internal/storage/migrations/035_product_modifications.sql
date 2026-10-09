CREATE TABLE product_modifications (
 product_id INTEGER PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
 parent_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
 CHECK(product_id<>parent_id)
);
CREATE INDEX product_modifications_parent ON product_modifications(parent_id,product_id);
CREATE TRIGGER product_modification_insert_guard BEFORE INSERT ON product_modifications
WHEN EXISTS(SELECT 1 FROM product_modifications WHERE product_id=NEW.parent_id OR parent_id=NEW.product_id)
 OR NOT EXISTS(SELECT 1 FROM products child JOIN products main ON main.id=NEW.parent_id
 WHERE child.id=NEW.product_id AND child.category_id=main.category_id AND child.deleted_at IS NULL AND main.deleted_at IS NULL)
BEGIN SELECT RAISE(ABORT,'invalid product group'); END;
CREATE TRIGGER product_modification_update_guard BEFORE UPDATE ON product_modifications
WHEN EXISTS(SELECT 1 FROM product_modifications WHERE product_id=NEW.parent_id OR parent_id=NEW.product_id)
 OR NOT EXISTS(SELECT 1 FROM products child JOIN products main ON main.id=NEW.parent_id
 WHERE child.id=NEW.product_id AND child.category_id=main.category_id AND child.deleted_at IS NULL AND main.deleted_at IS NULL)
BEGIN SELECT RAISE(ABORT,'invalid product group'); END;
