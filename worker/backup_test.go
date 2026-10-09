package worker

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestBackupWritesReadableSnapshotToConfiguredDirectory(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE items (name TEXT); INSERT INTO items VALUES ('preserved')"); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "persistent", "backups")
	w := NewBackupWorkerAt(db, time.Hour, backupDir)
	w.runBackup(context.Background())
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one snapshot in persistent directory: %v, %v", entries, err)
	}
	snapshot, err := sql.Open("sqlite", filepath.Join(backupDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var name string
	if err := snapshot.QueryRow("SELECT name FROM items").Scan(&name); err != nil || name != "preserved" {
		t.Fatalf("snapshot did not preserve data: %q, %v", name, err)
	}
}
