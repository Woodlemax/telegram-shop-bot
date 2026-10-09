package shop

import (
	"context"
	"errors"
	"shop_bot/internal/service"
	"shop_bot/internal/storage"
)

var ErrOpenPrice = errors.New("open price must be an integer from zero to 1000000 rubles")
var ErrRUBRate = errors.New("RUB exchange rate unavailable")

func applyProductPrice(p *storage.Product, exchange *service.ExchangeService) {
	if p.IsAuthorSale() {
		p.PriceUSD = 0
		p.PriceStars = 0
		return
	}
	exchange = exchange.Snapshot()
	if p.OpenPrice {
		zero := float64(0)
		p.PriceRUB = &zero
		p.PriceUSD = 0
		p.PriceStars = 0
	}
	if p.PriceRUB != nil {
		p.PriceUSD = 0
		p.PriceStars = 0
		if exchange != nil {
			p.PriceUSD = exchange.ConvertRUBToUSD(*p.PriceRUB)
			p.PriceStars = exchange.ConvertUSDToStars(p.PriceUSD)
		}
	} else if exchange != nil {
		p.PriceStars = exchange.ConvertUSDToStars(p.PriceUSD)
		if exchange.RUBConfigured() {
			rub := exchange.ConvertUSDToRUB(p.PriceUSD)
			p.PriceRUB = &rub
		}
	}
}

func (s *CartService) SetPrice(ctx context.Context, userID, productID int64, amount int) error {
	if amount < 0 || amount > 1000000 {
		return ErrOpenPrice
	}
	p, err := s.products.GetProduct(ctx, productID)
	if err != nil {
		return err
	}
	if !p.OpenPrice || p.SubPeriodDays > 0 || !p.IsActive {
		return ErrOpenPrice
	}
	if amount > 0 && (s.exchange == nil || !s.exchange.RUBConfigured()) {
		return ErrRUBRate
	}
	store, ok := s.cart.(interface {
		SetPrice(context.Context, int64, int64, int) error
	})
	if !ok {
		return ErrOpenPrice
	}
	return store.SetPrice(ctx, userID, productID, amount)
}

func (s *CartService) BeginPriceInput(ctx context.Context, userID, chatID, productID int64) error {
	p, err := s.products.GetProduct(ctx, productID)
	if err != nil {
		return err
	}
	if !p.OpenPrice || !p.IsActive || p.SubPeriodDays > 0 {
		return ErrOpenPrice
	}
	store, ok := s.cart.(interface {
		BeginPriceInput(context.Context, int64, int64, int64) error
	})
	if !ok {
		return ErrOpenPrice
	}
	return store.BeginPriceInput(ctx, userID, chatID, productID)
}

func (s *CartService) PendingPriceInput(ctx context.Context, userID, chatID int64) (int64, error) {
	store, ok := s.cart.(interface {
		PendingPriceInput(context.Context, int64, int64) (int64, error)
	})
	if !ok {
		return 0, nil
	}
	return store.PendingPriceInput(ctx, userID, chatID)
}

func (s *CartService) CancelPriceInput(ctx context.Context, userID int64) {
	if store, ok := s.cart.(interface {
		CancelPriceInput(context.Context, int64) error
	}); ok {
		_ = store.CancelPriceInput(ctx, userID)
	}
}

func IsFreeOrder(o *storage.Order) bool {
	return o.TotalUSD == 0 && o.TotalStars == 0 && o.TotalRUB == 0 && o.TotalTonNano == 0 && o.SubscriptionProductID == 0
}

func (s *OrderService) ConfirmFreeOrder(ctx context.Context, orderID, userID int64) error {
	o, err := s.orders.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if o.UserID != userID {
		return storage.ErrNotFound
	}
	if !IsFreeOrder(o) {
		return storage.ErrInvalidMoney
	}
	store, ok := s.orders.(interface {
		ConfirmFreeOrder(context.Context, int64, int64) error
	})
	if !ok {
		return storage.ErrInvalidMoney
	}
	if err := store.ConfirmFreeOrder(ctx, orderID, userID); err != nil {
		return err
	}
	if s.payments.Cache != nil {
		for _, item := range o.Items {
			s.payments.Cache.Invalidate(ctx, item.ProductID)
		}
	}
	return nil
}
