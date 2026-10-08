-- Keep the product identity for immutable orders, payments and file entitlements.
ALTER TABLE products ADD COLUMN deleted_at TEXT;
