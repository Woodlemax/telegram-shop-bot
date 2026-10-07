CREATE TABLE digital_archives (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    product_id INTEGER NOT NULL REFERENCES products(id),
    file_id TEXT NOT NULL,
    file_name TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK(size_bytes > 0),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX digital_archives_product ON digital_archives(product_id, id);
CREATE TABLE digital_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    order_id INTEGER NOT NULL REFERENCES orders(id),
    product_id INTEGER NOT NULL REFERENCES products(id),
    archive_id INTEGER NOT NULL REFERENCES digital_archives(id),
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sending','sent')),
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at INTEGER NOT NULL DEFAULT 0,
    lease_until INTEGER NOT NULL DEFAULT 0,
    lease_token TEXT NOT NULL DEFAULT '',
    message_id INTEGER,
    UNIQUE(order_id, product_id)
);
CREATE TABLE digital_uploads (
    admin_id INTEGER PRIMARY KEY,
    chat_id INTEGER NOT NULL,
    product_id INTEGER NOT NULL REFERENCES products(id),
    expires_at INTEGER NOT NULL
);
