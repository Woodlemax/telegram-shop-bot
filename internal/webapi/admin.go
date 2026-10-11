package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"shop_bot/internal/service"
	"shop_bot/internal/storage"
)

// AdminBackend shares the bot's stores and business operations, never Telegram commands.
type AdminBackend interface {
	AdminRead(context.Context, int64, string, int64, url.Values) (any, error)
	AdminWrite(context.Context, int64, string, int64, json.RawMessage) (any, error)
	AdminUpload(context.Context, int64, int64, string, string, io.Reader) (any, error)
}

func (s *Server) withAdmin(next func(http.ResponseWriter, *http.Request, *AuthResult)) http.HandlerFunc {
	return s.withAuth(func(w http.ResponseWriter, r *http.Request, a *AuthResult) {
		w.Header().Set("Cache-Control", "no-store")
		allowed := false
		for _, id := range s.deps.AdminIDs {
			if id > 0 && id == a.User.ID {
				allowed = true
				break
			}
		}
		if !allowed || s.deps.Admin == nil {
			s.writeError(w, http.StatusForbidden, "Доступ разрешён только администратору.")
			return
		}
		// Provider confirmation and receipt delivery can outlast the public API deadline.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
		next(w, r, a)
	})
}

func (s *Server) adminResult(w http.ResponseWriter, v any, err error) {
	if err == nil {
		s.writeJSON(w, http.StatusOK, v)
		return
	}
	status := http.StatusBadRequest
	message := "Не удалось выполнить действие. Проверьте данные и повторите."
	var explained interface {
		AdminStatus() int
		Error() string
	}
	if errors.As(err, &explained) {
		status = explained.AdminStatus()
		message = explained.Error()
	} else if errors.Is(err, storage.ErrNotFound) {
		status = 404
		message = "Запись не найдена."
	} else if errors.Is(err, storage.ErrAdminEditConflict) {
		status = 409
		message = "Товар уже изменился. Откройте его заново перед сохранением."
	} else if errors.Is(err, storage.ErrInvalidProductGroup) {
		message = "Основная модель должна быть из той же категории. Вложенные модификации недопустимы."
	} else if errors.Is(err, storage.ErrInvalidAuthorSale) || errors.Is(err, storage.ErrInvalidTelegramURL) {
		message = "Проверьте ссылку покупки, Telegram-ссылку и тип товара."
	} else if errors.Is(err, storage.ErrTooManyPhotos) {
		message = "Можно добавить не больше 10 фотографий."
	} else if errors.Is(err, storage.ErrPromoExists) {
		message = "Такой промокод уже существует."
	} else if errors.Is(err, storage.ErrInvalidMoney) {
		message = "Проверьте цену, количество и остальные поля."
	} else if errors.Is(err, storage.ErrPaymentReviewConflict) || errors.Is(err, storage.ErrOrderStatusConflict) {
		status = 409
		message = "Состояние заказа или платежа изменилось. Обновите страницу."
	} else {
		s.logger.Error("admin operation failed")
	}
	s.writeError(w, status, message)
}

func (s *Server) handleAdminRead(w http.ResponseWriter, r *http.Request, a *AuthResult) {
	id, ok := adminPathID(r)
	if !ok {
		s.writeError(w, 400, "Некорректный номер записи.")
		return
	}
	v, err := s.deps.Admin.AdminRead(r.Context(), a.User.ID, r.PathValue("resource"), id, r.URL.Query())
	s.adminResult(w, v, err)
}
func (s *Server) handleAdminWrite(w http.ResponseWriter, r *http.Request, a *AuthResult) {
	id, ok := adminPathID(r)
	if !ok {
		s.writeError(w, 400, "Некорректный номер записи.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		s.writeError(w, 413, "Слишком большой запрос.")
		return
	}
	if !json.Valid(body) {
		s.writeError(w, 400, "Некорректные данные.")
		return
	}
	v, err := s.deps.Admin.AdminWrite(r.Context(), a.User.ID, r.PathValue("resource"), id, body)
	s.adminResult(w, v, err)
}
func adminPathID(r *http.Request) (int64, bool) {
	if r.PathValue("id") == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}
func (s *Server) handleAdminUpload(w http.ResponseWriter, r *http.Request, a *AuthResult) {
	id, ok := adminPathID(r)
	if !ok || id == 0 {
		s.writeError(w, 400, "Некорректный номер товара.")
		return
	}
	kind := r.PathValue("kind")
	limit := int64(16 << 20)
	if kind == "archive" {
		limit = 128 << 20
	} else if kind != "photo" {
		s.writeError(w, 404, "Неизвестный тип файла.")
		return
	}
	if r.ContentLength > limit {
		s.writeError(w, 413, "Файл слишком большой.")
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(3 * time.Minute))
	_ = controller.SetWriteDeadline(time.Now().Add(4 * time.Minute))
	reader := http.MaxBytesReader(w, r.Body, limit)
	v, err := s.deps.Admin.AdminUpload(r.Context(), a.User.ID, id, kind, r.URL.Query().Get("name"), reader)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		s.writeError(w, 413, "Файл слишком большой.")
		return
	}
	s.adminResult(w, v, err)
}

var adminImageName = regexp.MustCompile(`^[a-f0-9]{32}\.(jpg|png|webp)$`)

func (s *Server) handleAdminMedia(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !adminImageName.MatchString(name) || s.deps.AdminMediaDir == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, filepath.Join(s.deps.AdminMediaDir, name))
}
func (s *Server) handleArchiveDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	user, delivery, ok := service.ValidArchiveDownload(s.deps.ArchiveDownloadKey, r.URL.Query(), time.Now())
	owned, available := s.deps.Archives.(interface {
		Owned(context.Context, int64, int64) (*storage.DigitalDelivery, error)
	})
	if !ok || !available || s.deps.ArchiveDownloadKey == "" {
		http.Error(w, "Ссылка недействительна или устарела. Запросите скачивание из заказа.", 403)
		return
	}
	d, err := owned.Owned(r.Context(), user, delivery)
	if err != nil {
		http.Error(w, "Нет доступа к архиву.", 403)
		return
	}
	file, valid := service.LocalArchivePath(s.deps.AdminMediaDir, d.FileID)
	if !valid {
		http.NotFound(w, r)
		return
	}
	if _, err = os.Stat(file); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.FileName}))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute))
	http.ServeFile(w, r, file)
}
