package webapi

import (
	"math"

	"shop_bot/internal/storage"
)

func (s *Server) displayProductJSON(p *storage.Product) productJSON {
	result := toProductJSON(p)
	rate := s.currentRUBRate()
	if p.PriceRUB == nil && s.deps.StarsOnlyPayments && rate > 0 {
		rub := math.Round(p.PriceUSD*rate*100) / 100
		result.PriceRUB = &rub
	}
	return result
}
func (s *Server) displayOrderJSON(o *storage.Order) orderJSON {
	result := toOrderJSON(o)
	rate := s.currentRUBRate()
	if o.TotalRUB == 0 && o.TotalUSD > 0 && s.deps.StarsOnlyPayments && rate > 0 {
		rub := math.Round(o.TotalUSD*rate*100) / 100
		result.DisplayTotalRUB = &rub
	}
	return result
}

func (s *Server) currentRUBRate() float64 {
	if s.deps.Exchange != nil {
		return s.deps.Exchange.GetUSDToRUBRate()
	}
	return s.deps.USDToRUBRate
}
