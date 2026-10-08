package webapi

import (
	"net/http"
	"shop_bot/internal/shop"
)

// Preview validates the committed cart without creating or consuming anything.
// Checkout re-reads the cart and revalidates the same code independently.
func (s *Server) handlePromoPreview(w http.ResponseWriter, r *http.Request, auth *AuthResult) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Promo string `json:"promo"`
	}
	if !s.decodeBody(w, r, &req) {
		return
	}
	view, err := s.deps.Cart.Get(r.Context(), auth.User.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "webapp_err_internal")
		return
	}
	if len(view.Items) == 0 {
		s.writeError(w, http.StatusBadRequest, "webapp_err_empty_cart")
		return
	}
	if err := shop.ValidateSubscriptionCart(view); err != nil {
		s.writeError(w, http.StatusBadRequest, "webapp_err_sub_alone")
		return
	}
	promo, key := s.resolvePromo(r.Context(), auth.User.ID, req.Promo, view)
	if key != "" {
		s.writeError(w, http.StatusBadRequest, key)
		return
	}
	discounted, err := shop.DiscountCart(view, promo)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "promo_not_found")
		return
	}
	response := s.cartJSON(discounted)
	response["original_total_rub"] = view.TotalRUB
	response["original_total_stars"] = view.TotalStars
	if promo != nil {
		response["promo"] = map[string]any{"code": promo.Code, "discount": promo.Discount, "category_id": promo.CategoryID, "product_ids": promo.ProductIDs}
	}
	s.writeJSON(w, http.StatusOK, response)
}
