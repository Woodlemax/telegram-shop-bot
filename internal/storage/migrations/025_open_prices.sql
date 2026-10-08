ALTER TABLE products ADD COLUMN open_price INTEGER NOT NULL DEFAULT 0 CHECK(open_price IN (0,1));
ALTER TABLE products ADD COLUMN price_rub REAL CHECK(price_rub >= 0);
ALTER TABLE cart_items ADD COLUMN custom_price INTEGER NOT NULL DEFAULT 0 CHECK(custom_price >= 0 AND custom_price <= 1000000);
CREATE TABLE free_order_grants (
    order_id INTEGER PRIMARY KEY REFERENCES orders(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE open_price_inputs (
    user_id INTEGER PRIMARY KEY,
    chat_id INTEGER NOT NULL,
    product_id INTEGER NOT NULL REFERENCES products(id),
    expires_at INTEGER NOT NULL
);
