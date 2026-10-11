package storage

import (
	"context"
	"testing"
)

func TestArchivePartsMigrationPreservesOldVersions(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	store := NewSQLProductStore(db)
	category, err := store.CreateCategory(ctx, &Category{Name: "Planes", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	product, err := store.CreateProduct(ctx, &Product{CategoryID: category, Name: "Plane", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-multipart schema with a large local archive and a legacy Telegram archive.
	for _, statement := range []string{"DROP TABLE digital_delivery_parts", "DROP TABLE digital_archive_parts", "DELETE FROM schema_migrations WHERE version='040_archive_parts.sql'"} {
		if _, err = db.Conn().ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	local, err := db.Conn().ExecContext(ctx, `INSERT INTO digital_archives(product_id,file_id,file_name,size_bytes) VALUES(?,'local:0123456789abcdef0123456789abcdef','plane.zip',100000000)`, product)
	if err != nil {
		t.Fatal(err)
	}
	localID, _ := local.LastInsertId()
	if _, err = db.Conn().ExecContext(ctx, `INSERT INTO digital_archives(product_id,file_id,file_name,size_bytes) VALUES(?,'legacy-tg','legacy.zip',80000000)`, product); err != nil {
		t.Fatal(err)
	}
	if err = db.migrate(); err != nil {
		t.Fatal(err)
	}
	archives := NewDigitalArchiveStore(db)
	parts, err := archives.Parts(ctx, &DigitalDelivery{ArchiveID: localID})
	if err != nil || len(parts) != 3 {
		t.Fatal("old local archive not migrated", err)
	}
	var end int64
	for _, part := range parts {
		if part.OffsetBytes != end || part.SizeBytes > MaxTelegramArchiveUploadBytes {
			t.Fatal("gap or oversized part", part)
		}
		end += part.SizeBytes
	}
	if end != 100000000 || parts[2].FileName != "plane.zip.003" {
		t.Fatal("incomplete migration", parts)
	}
	latest, err := archives.Latest(ctx, product)
	if err != nil || latest.FileID != "legacy-tg" || latest.Parts != 1 {
		t.Fatal("legacy Telegram file changed", latest, err)
	}
	if err = db.migrate(); err != nil {
		t.Fatal("migration not repeatable", err)
	}
}
