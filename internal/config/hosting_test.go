package config

import (
	"strings"
	"testing"
)

func TestHostingSettings(t *testing.T) {
	cases := []struct {
		name, port, backupDir string
		wantPort              int
		wantBackup            string
		invalid               bool
	}{
		{name: "local defaults", wantPort: 8080, wantBackup: "backups"},
		{name: "Railway volume and port", port: " 3000 ", backupDir: "/app/data/backups", wantPort: 3000, wantBackup: "/app/data/backups"},
		{name: "highest port", port: "65535", wantPort: 65535, wantBackup: "backups"},
		{name: "zero", port: "0", invalid: true},
		{name: "negative", port: "-1", invalid: true},
		{name: "too large", port: "65536", invalid: true},
		{name: "not a port", port: "tcp", invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadFromMap(map[string]string{
				"BOT_TOKEN":  "123456789:abcdefghijklmnopqrstuvwxyz_ABCD",
				"PORT":       tc.port,
				"BACKUP_DIR": tc.backupDir,
			})
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "PORT") {
					t.Fatalf("expected PORT validation error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Port != tc.wantPort || cfg.BackupDir != tc.wantBackup {
				t.Fatalf("got port %d and backup directory %q", cfg.Port, cfg.BackupDir)
			}
		})
	}
}
