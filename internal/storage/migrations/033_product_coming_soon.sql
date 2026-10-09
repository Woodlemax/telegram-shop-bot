ALTER TABLE products ADD COLUMN coming_soon INTEGER NOT NULL DEFAULT 0 CHECK(coming_soon IN (0,1));
