CREATE TABLE order_tax_receipts (
 order_id INTEGER PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL,
 payment_id TEXT NOT NULL UNIQUE,
 url TEXT NOT NULL UNIQUE,
 attached_by INTEGER NOT NULL,
 attached_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 delivery_status TEXT NOT NULL DEFAULT 'pending' CHECK(delivery_status IN ('pending','sending','sent','failed')),
 sent_at DATETIME,
 message_id INTEGER NOT NULL DEFAULT 0,
 delivery_token TEXT NOT NULL DEFAULT '',
 delivery_lease_until INTEGER NOT NULL DEFAULT 0
);
