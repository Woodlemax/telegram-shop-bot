package webapi

import (
	"errors"
	"net/http"
	"shop_bot/internal/storage"
	"strconv"
)

func (s *Server) handleGroupedProducts(w http.ResponseWriter, r *http.Request, categoryID int64, page int) {
	w.Header().Set("Cache-Control", "no-store")
	groups, total, err := s.deps.ProductGroups.ListRoots(r.Context(), categoryID, productsPerPage, (page-1)*productsPerPage)
	if err != nil {
		s.writeError(w, 500, "webapp_err_internal")
		return
	}
	out := make([]productJSON, 0, len(groups))
	for _, g := range groups {
		p, err := s.deps.Catalog.GetProduct(r.Context(), g.ID)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			s.writeError(w, 500, "webapp_err_internal")
			return
		}
		if !p.IsActive || !(p.InfiniteStock || p.Stock > 0 || p.ComingSoon || p.IsAuthorSale()) {
			continue
		}
		item := s.displayProductJSON(p)
		item.ModificationCount = g.ModificationCount
		out = append(out, item)
	}
	s.writeJSON(w, 200, map[string]any{"products": out, "total": total, "page": page, "per_page": productsPerPage})
}
func (s *Server) handleModifications(w http.ResponseWriter, r *http.Request, _ *AuthResult) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, 400, "webapp_err_bad_request")
		return
	}
	if s.deps.ProductGroups == nil {
		s.writeError(w, 404, "webapp_err_not_found")
		return
	}
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 || page > 1000000 {
			s.writeError(w, 400, "webapp_err_bad_request")
			return
		}
	}
	main, err := s.deps.Catalog.GetProduct(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && (!main.IsActive || !(main.InfiniteStock || main.Stock > 0 || main.ComingSoon || main.IsAuthorSale()))) {
		s.writeError(w, 404, "webapp_err_not_found")
		return
	}
	if err != nil {
		s.writeError(w, 500, "webapp_err_internal")
		return
	}
	const perPage = 20
	ids, total, err := s.deps.ProductGroups.ListModifications(r.Context(), id, perPage, (page-1)*perPage)
	if err != nil {
		s.writeError(w, 500, "webapp_err_internal")
		return
	}
	out := make([]productJSON, 0, len(ids))
	for _, childID := range ids {
		p, err := s.deps.Catalog.GetProduct(r.Context(), childID)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			s.writeError(w, 500, "webapp_err_internal")
			return
		}
		if p.CategoryID != main.CategoryID || !p.IsActive || !(p.InfiniteStock || p.Stock > 0 || p.ComingSoon || p.IsAuthorSale()) {
			continue
		}
		out = append(out, s.displayProductJSON(p))
	}
	s.writeJSON(w, 200, map[string]any{"products": out, "total": total, "page": page, "per_page": perPage})
}
