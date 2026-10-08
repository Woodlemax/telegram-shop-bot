-- Retain IDs even after product deletion so a scoped promo never becomes global.
ALTER TABLE promo_codes ADD COLUMN product_ids TEXT NOT NULL DEFAULT '[]';
