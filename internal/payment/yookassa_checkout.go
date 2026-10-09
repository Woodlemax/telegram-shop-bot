package payment

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"shop_bot/internal/storage"
)

var ErrYooKassaAwaitingConfirmation = errors.New("yookassa: payment already submitted or creation result requires verification")

type YooKassaCheckoutStore interface {
	GetOrCreate(context.Context, storage.YooKassaCheckoutIntent) (*storage.YooKassaCheckoutIntent, error)
	Save(context.Context, *storage.YooKassaCheckoutIntent, string, string) error
	Rotate(context.Context, *storage.YooKassaCheckoutIntent, string, time.Time) (bool, error)
}

// YooKassaCheckout coordinates the bot and Mini App using one durable intent.
// Concurrent creation and uncertain API failures reuse the SAME request key
// and frozen body. A new charge is permitted only after verified cancellation.
type YooKassaCheckout struct {
	*YooKassaPayment
	intents YooKassaCheckoutStore
	now     func() time.Time
}

func NewYooKassaCheckout(shopID, secretKey, returnURL string, intents YooKassaCheckoutStore) *YooKassaCheckout {
	return &YooKassaCheckout{YooKassaPayment: NewYooKassaPayment(shopID, secretKey, returnURL), intents: intents, now: time.Now}
}
func (y *YooKassaCheckout) CreatePayment(ctx context.Context, orderID, amountMinor int64, description string) (*Invoice, error) {
	if !y.Configured() {
		return nil, ErrYooKassaNotConfigured
	}
	if y.intents == nil || orderID <= 0 || amountMinor <= 0 {
		return nil, ErrInvalidYooKassaReceipt
	}
	for attempt := 0; attempt < 5; attempt++ {
		intent, err := y.intents.GetOrCreate(ctx, storage.YooKassaCheckoutIntent{
			OrderID: orderID, ShopID: y.shopID, RequestKey: uuid.NewString(), AmountMinor: amountMinor,
			Description: description, ReturnURL: y.returnURL, CreatedAt: y.now().UTC(),
		})
		if err != nil {
			return nil, err
		}
		if intent.PaymentID != "" {
			object, err := y.getPaymentObject(ctx, intent.PaymentID)
			if err != nil {
				return nil, err
			} // Never create a replacement when GET is uncertain.
			if object.ID != intent.PaymentID || object.Metadata["order_id"] != strconv.FormatInt(orderID, 10) ||
				object.Amount.Currency != "RUB" || object.Amount.Value != formatMinorUnits(intent.AmountMinor, 2) {
				return nil, ErrInvalidYooKassaReceipt
			}
			if object.Paid || object.Status == "succeeded" || object.Status == "waiting_for_capture" {
				return nil, ErrYooKassaAwaitingConfirmation
			}
			switch object.Status {
			case "pending":
				if strings.TrimSpace(intent.PayURL) == "" {
					return nil, ErrInvalidYooKassaReceipt
				}
				return &Invoice{InvoiceID: intent.PaymentID, PayURL: intent.PayURL}, nil
			case "canceled":
				_, err = y.intents.Rotate(ctx, intent, uuid.NewString(), y.now().UTC())
				if err != nil {
					return nil, err
				}
				continue
			default:
				return nil, ErrInvalidYooKassaReceipt
			}
		}
		// YooKassa only guarantees request deduplication for 24 hours. An unknown
		// creation result older than that must NEVER be retried as a new charge.
		if y.now().Sub(intent.CreatedAt) >= 23*time.Hour {
			return nil, ErrYooKassaAwaitingConfirmation
		}
		invoice, err := y.createPaymentWithKey(ctx, orderID, intent.AmountMinor, intent.Description, intent.ReturnURL, intent.RequestKey)
		if err != nil {
			return nil, err
		}
		if err = y.intents.Save(ctx, intent, invoice.InvoiceID, invoice.PayURL); err != nil {
			return nil, err
		}
		return invoice, nil
	}
	return nil, ErrYooKassaAwaitingConfirmation
}
