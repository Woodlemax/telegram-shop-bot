CREATE TABLE shop_exchange_rate (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    rub_per_usd REAL NOT NULL CHECK (rub_per_usd >= 1 AND rub_per_usd <= 1000000)
);
