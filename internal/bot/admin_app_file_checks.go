package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path/filepath"
	"shop_bot/internal/storage"
	"strings"
)

func (b *Bot) adminReferenceUpload(ctx context.Context, actor, id int64, name string, reader io.Reader) (any, error) {
	store := storage.NewProductFileCheckStore(b.adminDB.Conn())
	reference, err := store.Requirement(ctx, id)
	if err != nil {
		return nil, err
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." || len(name) > 255 || strings.ContainsAny(name, "\r\n\x00") {
		return nil, adminInvalid("Проверьте имя эталонного файла.")
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(reader, storage.MaxVerificationFileBytes+1))
	if err != nil {
		return nil, err
	}
	if size == 0 || size > storage.MaxVerificationFileBytes {
		return nil, adminInvalid("Эталон должен быть непустым файлом до 128 МБ.")
	}
	b.adminAppMu.Lock()
	defer b.adminAppMu.Unlock()
	result, err := store.ReplaceReference(ctx, id, name, hex.EncodeToString(hash.Sum(nil)), size, reference.Revision)
	if err == nil {
		_, _ = b.adminDB.Conn().ExecContext(ctx, `INSERT INTO admin_app_audit(admin_id,action,target_id) VALUES(?,'reference-upload',?)`, actor, id)
	}
	return result, err
}
