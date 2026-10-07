ALTER TABLE products ADD COLUMN infinite_stock INTEGER NOT NULL DEFAULT 0 CHECK (infinite_stock IN (0,1));
ALTER TABLE products ADD COLUMN single_in_cart INTEGER NOT NULL DEFAULT 0 CHECK (single_in_cart IN (0,1));

-- Preserve the existing default behavior of digital ZIP products.
UPDATE products SET infinite_stock=1, single_in_cart=1 WHERE is_digital=1;
UPDATE cart_items SET quantity=1 WHERE product_id IN (SELECT id FROM products WHERE single_in_cart=1);

-- Keep existing carts within the limit when an administrator enables it.
CREATE TRIGGER product_single_in_cart_enabled AFTER UPDATE OF single_in_cart ON products
WHEN NEW.single_in_cart=1
BEGIN
  UPDATE cart_items SET quantity=1 WHERE product_id=NEW.id AND quantity>1;
END;
