-- Author sales are external offers, outside shop carts and payment processing.
ALTER TABLE products ADD COLUMN author_telegram_url TEXT NOT NULL DEFAULT '';

CREATE TRIGGER author_sale_clear_cart AFTER UPDATE OF author_telegram_url ON products
WHEN NEW.author_telegram_url <> ''
BEGIN
 DELETE FROM cart_items WHERE product_id=NEW.id;
 DELETE FROM open_price_inputs WHERE product_id=NEW.id;
END;

CREATE TRIGGER author_sale_cart_insert BEFORE INSERT ON cart_items
WHEN EXISTS(SELECT 1 FROM products WHERE id=NEW.product_id AND author_telegram_url <> '')
BEGIN SELECT RAISE(ABORT, 'author sale cannot enter cart'); END;
CREATE TRIGGER author_sale_cart_update BEFORE UPDATE OF product_id ON cart_items
WHEN EXISTS(SELECT 1 FROM products WHERE id=NEW.product_id AND author_telegram_url <> '')
BEGIN SELECT RAISE(ABORT, 'author sale cannot enter cart'); END;
