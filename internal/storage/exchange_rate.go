package storage

import (
	"context"
	"database/sql"
	"errors"
)

// RUBRateStore persists the administrator's override; nil means use the env default.
type RUBRateStore interface {
	GetRUBRate(context.Context) (*float64, error)
	SetRUBRate(context.Context, float64) error
}
type SQLRUBRateStore struct{ db *sql.DB }

func NewSQLRUBRateStore(db *sql.DB) *SQLRUBRateStore { return &SQLRUBRateStore{db: db} }
func (s *SQLRUBRateStore) GetRUBRate(ctx context.Context) (*float64, error) {
	var rate float64
	err := s.db.QueryRowContext(ctx, `SELECT rub_per_usd FROM shop_exchange_rate WHERE id=1`).Scan(&rate)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rate, nil
}
func (s *SQLRUBRateStore) SetRUBRate(ctx context.Context, rate float64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO shop_exchange_rate(id,rub_per_usd) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET rub_per_usd=excluded.rub_per_usd`, rate)
	return err
}
