CREATE TABLE product_file_requirements (
    product_id INTEGER PRIMARY KEY REFERENCES products(id),
    enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
    description TEXT NOT NULL DEFAULT '',
    reference_sha256 TEXT NOT NULL DEFAULT '',
    reference_name TEXT NOT NULL DEFAULT '',
    reference_bytes INTEGER NOT NULL DEFAULT 0,
    version INTEGER NOT NULL DEFAULT 0,
    revision INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE product_file_verifications (
    user_id INTEGER NOT NULL,
    product_id INTEGER NOT NULL REFERENCES products(id),
    version INTEGER NOT NULL,
    verified_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(user_id, product_id)
);
