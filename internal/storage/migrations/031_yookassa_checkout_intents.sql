-- One recoverable creation request and one current provider payment per order/merchant.
CREATE TABLE yookassa_checkout_intents (
 order_id INTEGER NOT NULL REFERENCES orders(id),
 shop_id TEXT NOT NULL,
 request_key TEXT NOT NULL UNIQUE,
 amount_minor INTEGER NOT NULL CHECK(amount_minor > 0),
 description TEXT NOT NULL,
 return_url TEXT NOT NULL,
 payment_id TEXT NOT NULL DEFAULT '',
 pay_url TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 PRIMARY KEY(order_id, shop_id)
);
CREATE UNIQUE INDEX idx_yookassa_checkout_payment
 ON yookassa_checkout_intents(shop_id, payment_id) WHERE payment_id <> '';
