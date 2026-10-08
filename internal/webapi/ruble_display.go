package webapi

import (
	"math"
	"shop_bot/internal/storage"
)

func (s *Server) displayProductJSON(p *storage.Product) productJSON {
	result := toProductJSON(p)
	if p.PriceRUB == nil && s.deps.StarsOnlyPayments && s.deps.USDToRUBRate > 0 {
		rub := math.Round(p.PriceUSD*s.deps.USDToRUBRate*100) / 100
		result.PriceRUB = &rub
	}
	return result
}
func (s *Server) displayOrderJSON(o *storage.Order) orderJSON {
	result := toOrderJSON(o)
	if o.TotalRUB == 0 && o.TotalUSD > 0 && s.deps.StarsOnlyPayments && s.deps.USDToRUBRate > 0 {
		rub := math.Round(o.TotalUSD*s.deps.USDToRUBRate*100) / 100
		result.DisplayTotalRUB = &rub
	}
	return result
}
