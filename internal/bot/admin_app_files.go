package bot

import (
	"archive/zip"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"shop_bot/internal/service"
	"shop_bot/internal/storage"
)

var telegramPhotoID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,512}$`)

func validAdminPhoto(ref string) bool {
	if telegramPhotoID.MatchString(ref) {
		return true
	}
	u, err := url.Parse(ref)
	return err == nil && len(ref) <= 2048 && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

func (b *Bot) AdminMediaDirectory() string {
	return filepath.Join(filepath.Dir(b.cfg.DBPath), "admin-media")
}

func (b *Bot) AdminUpload(ctx context.Context, actor, id int64, kind, name string, reader io.Reader) (any, error) {
	if !b.isAdmin(actor) {
		return nil, adminAppError{403, "Доступ разрешён только администратору."}
	}
	p, err := b.adminStore().GetProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	if kind != "photo" && kind != "archive" {
		return nil, adminInvalid("Неизвестный тип файла.")
	}
	if kind == "archive" && p.SubPeriodDays > 0 {
		return nil, adminInvalid("Архив нельзя прикрепить к подписке.")
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if len(name) > 255 || strings.ContainsAny(name, "\r\n\x00") {
		return nil, adminInvalid("Некорректное имя файла.")
	}
	if kind == "archive" && !strings.HasSuffix(strings.ToLower(name), ".zip") {
		return nil, adminInvalid("Загрузите ZIP-архив.")
	}
	dir := b.AdminMediaDirectory()
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return nil, err
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	limit := int64(16 << 20)
	if kind == "archive" {
		limit = 128 << 20
	}
	count, err := io.Copy(f, io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if count == 0 || count > limit {
		return nil, adminInvalid("Пустой или слишком большой файл.")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	fileID := strings.ReplaceAll(uuid.NewString(), "-", "")
	var ext string
	if kind == "archive" {
		archive, e := zip.NewReader(f, count)
		if e != nil || len(archive.File) == 0 || len(archive.File) > 10000 {
			return nil, adminInvalid("Не удалось прочитать ZIP-архив.")
		}
		ext = ".zip"
	} else {
		header := make([]byte, 512)
		n, _ := f.ReadAt(header, 0)
		switch http.DetectContentType(header[:n]) {
		case "image/jpeg":
			ext = ".jpg"
		case "image/png":
			ext = ".png"
		case "image/webp":
			ext = ".webp"
		default:
			return nil, adminInvalid("Поддерживаются фотографии JPG, PNG и WebP.")
		}
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	final := filepath.Join(dir, fileID+ext)
	if err = os.Rename(temp, final); err != nil {
		return nil, err
	}
	saved := false
	defer func() {
		if !saved {
			_ = os.Remove(final)
		}
	}()
	b.adminAppMu.Lock()
	defer b.adminAppMu.Unlock()
	if kind == "archive" {
		version, e := b.archives.Add(ctx, id, "local:"+fileID, name, count)
		if e != nil {
			return nil, e
		}
		saved = true
		b.invalidateAdminProduct(ctx, id)
		_, _ = b.adminDB.Conn().ExecContext(ctx, `INSERT INTO admin_app_audit(admin_id,action,target_id) VALUES(?,'archive-upload',?)`, actor, id)
		return map[string]any{"version": version, "name": name, "bytes": count, "message": "Архив заменён. Новый файл доступен всем прежним покупателям."}, nil
	}
	data, err := b.adminProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	photos := data.(map[string]any)["photos"].([]string)
	if len(photos) >= storage.MaxProductPhotos {
		return nil, storage.ErrTooManyPhotos
	}
	u, err := url.Parse(b.cfg.WebAppURL)
	if err != nil {
		return nil, err
	}
	u.Path = "/api/media/" + fileID + ext
	u.RawQuery = ""
	u.Fragment = ""
	photos = append(photos, u.String())
	if err = b.adminStore().SetAdminPhotos(ctx, id, photos); err != nil {
		return nil, err
	}
	saved = true
	b.invalidateAdminProduct(ctx, id)
	_, _ = b.adminDB.Conn().ExecContext(ctx, `INSERT INTO admin_app_audit(admin_id,action,target_id) VALUES(?,'photo-upload',?)`, actor, id)
	return b.adminProduct(ctx, id)
}

func (b *Bot) localDigitalDownloadURL(d *storage.DigitalDelivery) string {
	return service.ArchiveDownloadURL(b.cfg.WebAppURL, b.cfg.BotToken, d.UserID, d.ID, time.Now())
}
