package webapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"shop_bot/internal/storage"
	"strconv"
	"time"
)

func (s *Server) handleVerifyProductFile(w http.ResponseWriter, r *http.Request, auth *AuthResult) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 || s.deps.FileChecks == nil {
		s.writeError(w, 400, "webapp_err_bad_request")
		return
	}
	reference, err := s.deps.FileChecks.Requirement(r.Context(), id)
	if err != nil {
		s.writeError(w, 404, "webapp_err_not_found")
		return
	}
	if !reference.Enabled {
		s.writeError(w, 409, "file_check_not_required")
		return
	}
	limit := storage.MaxVerificationFileBytes
	if r.ContentLength > limit {
		s.writeError(w, 413, "file_check_too_large")
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(3 * time.Minute))
	_ = controller.SetWriteDeadline(time.Now().Add(4 * time.Minute))
	hash := sha256.New()
	size, err := io.Copy(hash, http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeError(w, 413, "file_check_too_large")
		} else {
			s.writeError(w, 400, "file_check_upload_failed")
		}
		return
	}
	if size == 0 {
		s.writeError(w, 400, "file_check_empty")
		return
	}
	err = s.deps.FileChecks.Verify(r.Context(), auth.User.ID, id, reference.Version, hex.EncodeToString(hash.Sum(nil)), size)
	if errors.Is(err, storage.ErrFileVerificationMismatch) {
		s.writeError(w, 422, "file_check_mismatch")
		return
	}
	if err != nil {
		s.writeError(w, 500, "webapp_err_internal")
		return
	}
	status, err := s.deps.FileChecks.Status(r.Context(), auth.User.ID, id)
	if err != nil {
		s.writeError(w, 500, "webapp_err_internal")
		return
	}
	if !status.Verified {
		s.writeError(w, 409, "file_check_reference_changed")
		return
	}
	s.writeJSON(w, 200, map[string]any{"file_check": status})
}
func (s *Server) checkFilePurchase(w http.ResponseWriter, r *http.Request, userID, id int64) bool {
	if s.deps.FileChecks == nil {
		return true
	}
	status, err := s.deps.FileChecks.Status(r.Context(), userID, id)
	if errors.Is(err, storage.ErrNotFound) {
		s.writeError(w, 404, "webapp_err_not_found")
		return false
	}
	if err != nil {
		s.writeError(w, 500, "webapp_err_internal")
		return false
	}
	if status.Required && !status.Verified {
		s.writeError(w, 403, "file_check_required")
		return false
	}
	return true
}
func (s *Server) checkOrderFiles(w http.ResponseWriter, r *http.Request, auth *AuthResult, items []storage.OrderItem) bool {
	for _, item := range items {
		if !s.checkFilePurchase(w, r, auth.User.ID, item.ProductID) {
			return false
		}
	}
	return true
}
