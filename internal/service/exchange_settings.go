package service

import (
	"context"
	"errors"
	"fmt"
	"math"

	"shop_bot/internal/storage"
)

var ErrInvalidRUBRate = errors.New("RUB rate must be between 1 and 1000000")

func ValidRUBRate(rate float64) bool {
	return rate >= 1 && rate <= 1000000 && !math.IsNaN(rate) && !math.IsInf(rate, 0)
}

// UpdateRUBRate serializes persistence and publication. A failed write leaves the active rate unchanged.
func (s *ExchangeService) UpdateRUBRate(ctx context.Context, rate float64) error {
	if !ValidRUBRate(rate) {
		return ErrInvalidRUBRate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rubRates == nil {
		return errors.New("RUB rate persistence unavailable")
	}
	if err := s.rubRates.SetRUBRate(ctx, rate); err != nil {
		return fmt.Errorf("save RUB rate: %w", err)
	}
	s.usdToRUB = rate
	return nil
}
func (s *ExchangeService) GetUSDToRUBRate() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.usdToRUB
}

// Snapshot fixes conversion rates for all items and totals of one cart calculation.
func (s *ExchangeService) Snapshot() *ExchangeService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return NewExchangeService(s.usdToStars, s.usdToRUB, s.usdPerTON)
}

func NewPersistentExchangeService(ctx context.Context, store storage.RUBRateStore, stars int, rub, ton float64) (*ExchangeService, error) {
	rate, err := store.GetRUBRate(ctx)
	if err != nil {
		return nil, fmt.Errorf("load RUB rate: %w", err)
	}
	if rate != nil {
		if !ValidRUBRate(*rate) {
			return nil, ErrInvalidRUBRate
		}
		rub = *rate
	}
	svc := NewExchangeService(stars, rub, ton)
	svc.rubRates = store
	return svc, nil
}
