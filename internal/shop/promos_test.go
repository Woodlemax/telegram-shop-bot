package shop

import (
	"errors"
	"math"
	"shop_bot/internal/storage"
	"testing"
)

func TestPromoTotalsScopedAndImmutable(t *testing.T) {
	a, b := float64(100), float64(200)
	cat := int64(1)
	view := &CartView{BaseRUB: true, TotalRUB: 300, TotalUSD: 3, TotalStars: 150, Items: []CartItemView{{Product: storage.Product{CategoryID: 1, PriceRUB: &a, PriceUSD: 1, PriceStars: 50}, Quantity: 1}, {Product: storage.Product{CategoryID: 2, PriceRUB: &b, PriceUSD: 2, PriceStars: 100}, Quantity: 1}}}
	discounted, err := DiscountCart(view, &storage.PromoCode{Code: "CATEGORY10", Discount: 10, CategoryID: &cat})
	if err != nil || discounted.TotalRUB != 290 || discounted.TotalStars != 145 || math.Abs(discounted.TotalUSD-2.9) > .00001 {
		t.Fatal("scoped totals", discounted, err)
	}
	if view.TotalRUB != 300 || view.TotalStars != 150 || *view.Items[0].Product.PriceRUB != 100 {
		t.Fatal("discount mutated source cart")
	}
	tiny := &CartView{BaseRUB: true, TotalRUB: 1, TotalUSD: .01, TotalStars: 1}
	result, err := DiscountCart(tiny, &storage.PromoCode{Code: "SAVE99", Discount: 99})
	if err != nil || result.TotalStars != 1 || result.TotalRUB != .01 {
		t.Fatal("positive total lost minimum Star", result, err)
	}
	for _, d := range []int{-1, 0, 101} {
		if _, err := DiscountCart(view, &storage.PromoCode{Code: "BAD", Discount: d}); !errors.Is(err, storage.ErrInvalidPromo) {
			t.Fatal("invalid discount accepted", d, err)
		}
	}
}
