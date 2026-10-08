package shop

import (
	"math"
	"shop_bot/internal/storage"
)

// DiscountCart is shared by preview and order creation. It never mutates the
// cart or its items; category restrictions apply only to eligible line totals.
func DiscountCart(view *CartView, promo *storage.PromoCode) (*CartView, error) {
	result := *view
	if result.BaseRUB {
		result.TotalUSD = math.Round(result.TotalUSD*100) / 100
	}
	if promo == nil {
		return &result, nil
	}
	if err := storage.ValidatePromo(promo); err != nil {
		return nil, err
	}
	usd, rub, stars := result.TotalUSD, view.TotalRUB, view.TotalStars
	if promo.CategoryID != nil {
		matched := false
		rawUSD := float64(0)
		rub, stars = 0, 0
		for _, item := range view.Items {
			if item.Product.CategoryID != *promo.CategoryID {
				continue
			}
			matched = true
			rawUSD += item.Product.PriceUSD * float64(item.Quantity)
			stars += item.Product.PriceStars * item.Quantity
			if item.Product.PriceRUB != nil {
				rub += *item.Product.PriceRUB * float64(item.Quantity)
			}
		}
		if !matched {
			return nil, storage.ErrInvalidPromo
		}
		usd = rawUSD
		if view.TotalUSD > 0 {
			usd = result.TotalUSD * math.Min(1, rawUSD/view.TotalUSD)
			if !view.BaseRUB {
				rub = view.TotalRUB * math.Min(1, rawUSD/view.TotalUSD)
			}
		}
	}
	keep := float64(100-promo.Discount) / 100
	result.TotalUSD = math.Max(0, result.TotalUSD-usd) + usd*keep
	result.TotalRUB = math.Round((math.Max(0, view.TotalRUB-rub)+rub*keep)*100) / 100
	result.TotalStars = view.TotalStars - stars + stars*(100-promo.Discount)/100
	// A positive price must never become a zero-Star invoice due to rounding.
	if view.TotalStars > 0 && result.TotalStars == 0 && (result.TotalUSD > 0 || result.TotalRUB > 0) {
		result.TotalStars = 1
	}
	result.TotalTONNano = 0
	if view.TotalUSD > 0 {
		result.TotalTONNano = int64(math.Round(float64(view.TotalTONNano) * result.TotalUSD / view.TotalUSD))
	}
	return &result, nil
}
