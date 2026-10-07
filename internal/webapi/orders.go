package webapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"shop_bot/internal/storage"
)

const ordersPerPage = 10

// Only buyer-visible snapshots are returned: no provider identifiers, user IDs,
// archive references or administrative payment records.
type orderJSON struct {
	ID            int64           `json:"id"`
	Status        string          `json:"status"`
	PaymentState  string          `json:"payment_state"`
	PaymentMethod string          `json:"payment_method"`
	TotalRUB      float64         `json:"total_rub"`
	TotalUSD      float64         `json:"total_usd"`
	TotalStars    int             `json:"total_stars"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Items         []orderItemJSON `json:"items"`
}
type orderItemJSON struct {
	ProductID         int64  `json:"product_id"`
	Name              string `json:"name"`
	Quantity          int    `json:"quantity"`
	DownloadAvailable bool   `json:"download_available"`
	ArchiveName       string `json:"archive_name,omitempty"`
}

func toOrderJSON(o *storage.Order) orderJSON {
	items := make([]orderItemJSON, 0, len(o.Items))
	for _, item := range o.Items {
		items = append(items, orderItemJSON{ProductID: item.ProductID, Name: item.ProductName, Quantity: item.Quantity})
	}
	return orderJSON{ID: o.ID, Status: o.Status, PaymentState: o.PaymentState, PaymentMethod: o.PaymentMethod, TotalRUB: o.TotalRUB, TotalUSD: o.TotalUSD, TotalStars: o.TotalStars, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt, Items: items}
}
func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request, auth *AuthResult) {
	w.Header().Set("Cache-Control", "no-store")
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		var err error
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 || page > 1000000 {
			s.writeError(w, http.StatusBadRequest, "webapp_err_bad_request")
			return
		}
	}
	orders, total, err := s.deps.Orders.GetUserOrdersPaged(r.Context(), auth.User.ID, ordersPerPage, (page-1)*ordersPerPage)
	if err != nil {
		s.logger.Error("webapi: user order history", "error", err)
		s.writeError(w, http.StatusInternalServerError, "webapp_err_internal")
		return
	}
	items := make([]orderJSON, 0, len(orders))
	for _, o := range orders {
		if o.UserID == auth.User.ID {
			items = append(items, toOrderJSON(&o))
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"orders": items, "total": total, "page": page, "per_page": ordersPerPage})
}
func (s *Server) handleOrder(w http.ResponseWriter, r *http.Request, auth *AuthResult) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, http.StatusBadRequest, "webapp_err_bad_request")
		return
	}
	o, err := s.deps.Orders.GetOrder(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && (o == nil || o.UserID != auth.User.ID)) {
		s.writeError(w, http.StatusNotFound, "webapp_err_not_found")
		return
	}
	if err != nil {
		s.logger.Error("webapi: order history detail", "order_id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "webapp_err_internal")
		return
	}
	result := toOrderJSON(o)
	if s.deps.Archives != nil {
		files, err := s.deps.Archives.ForOrder(r.Context(), auth.User.ID, id)
		if err != nil {
			s.logger.Error("webapi: order archives unavailable", "order_id", id)
			s.writeError(w, http.StatusInternalServerError, "webapp_err_internal")
			return
		}
		for i := range result.Items {
			for _, file := range files {
				if file.ProductID == result.Items[i].ProductID {
					result.Items[i].DownloadAvailable = true
					result.Items[i].ArchiveName = file.FileName
					break
				}
			}
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"order": result})
}

func (s *Server) handleOrderCancel(w http.ResponseWriter, r *http.Request, auth *AuthResult) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, http.StatusBadRequest, "webapp_err_bad_request")
		return
	}
	o, err := s.deps.Orders.GetOrder(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && (o == nil || o.UserID != auth.User.ID)) {
		s.writeError(w, http.StatusNotFound, "webapp_err_not_found")
		return
	}
	if err != nil {
		s.logger.Error("webapi: load order for cancellation", "order_id", id)
		s.writeError(w, http.StatusInternalServerError, "webapp_err_internal")
		return
	}
	if o.Status != storage.OrderStatusPending || o.PaymentState != storage.PaymentStatePending {
		s.writeError(w, http.StatusConflict, "webapp_order_cancel_unavailable")
		return
	}
	// The store atomically checks ownership and pending payment again, so a
	// payment confirmed after the read cannot be overwritten by cancellation.
	err = s.deps.Orders.CancelOrder(r.Context(), id, auth.User.ID)
	if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrOrderStatusConflict) {
		s.writeError(w, http.StatusConflict, "webapp_order_cancel_unavailable")
		return
	}
	if err != nil {
		s.logger.Error("webapi: cancel order", "order_id", id)
		s.writeError(w, http.StatusInternalServerError, "webapp_err_internal")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

func (s *Server) handleOrderDownload(w http.ResponseWriter, r *http.Request, auth *AuthResult) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		ProductID int64 `json:"product_id"`
	}
	if err != nil || id <= 0 {
		s.writeError(w, http.StatusBadRequest, "webapp_err_bad_request")
		return
	}
	if !s.decodeBody(w, r, &req) {
		return
	}
	if req.ProductID <= 0 {
		s.writeError(w, http.StatusBadRequest, "webapp_err_bad_request")
		return
	}
	if s.deps.Archives == nil {
		s.writeError(w, http.StatusNotFound, "digital_no_access")
		return
	}
	err = s.deps.Archives.RequestOrderDownload(r.Context(), auth.User.ID, id, req.ProductID)
	if errors.Is(err, storage.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "digital_no_access")
		return
	}
	if err != nil {
		s.logger.Error("webapi: archive download queue failed", "order_id", id)
		s.writeError(w, http.StatusInternalServerError, "webapp_err_internal")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"queued": true})
}
