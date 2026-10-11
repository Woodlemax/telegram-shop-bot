package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"shop_bot/internal/service"
	"shop_bot/internal/storage"
)

func (b *Bot) announceAdminDelivery(o *storage.Order) {
	b.outWebhook.Send(service.OutboundWebhookEvent{Event: "order.delivered", OrderID: o.ID, UserID: o.UserID, TotalUSD: o.TotalUSD, TotalStars: o.TotalStars})
}

func (b *Bot) adminRefundPreview(ctx context.Context, actor, id int64, raw json.RawMessage) (any, error) {
	var input struct {
		AmountMinor int64 `json:"amount_minor"`
	}
	if err := decodeAdmin(raw, &input); err != nil {
		return nil, err
	}
	o, err := b.order.GetOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	rail := storage.CanonicalPaymentProvider(o.PaymentMethod)
	if !refundableOrder(o) || refundIsManualRail(rail) {
		return nil, adminInvalid("Для этого заказа автоматический возврат недоступен.")
	}
	full, currency, scale, valid := refundRailMoney(o, rail)
	amount := input.AmountMinor
	if amount == 0 {
		amount = full
	}
	if !valid || amount <= 0 || amount > full || (rail == storage.PaymentMethodStars && amount != full) {
		return nil, adminInvalid("Проверьте сумму возврата. Stars возвращаются только полностью.")
	}
	_, outcome, err := b.prepareRefund(ctx, o, rail, amount, currency, scale)
	if err != nil {
		return nil, adminInvalid(b.refundFailureText("ru", id, err))
	}
	if outcome != storage.PaymentIngressApply {
		return nil, adminConflict("Возврат уже выполнен или платёж требует проверки.")
	}
	token := b.newAdminConfirmation(adminConfirmation{Actor: actor, OrderID: id, AmountMinor: amount, PaymentID: o.PaymentID, Action: "refund"})
	return map[string]any{"token": token, "message": fmt.Sprintf("Вернуть %s покупателю %d по заказу #%d? Способ: %s.", refundAmountLabel(amount, currency, scale), o.UserID, id, rail), "amount_minor": amount, "currency": currency, "scale": scale}, nil
}

func (b *Bot) adminReviewPreview(ctx context.Context, actor int64, raw json.RawMessage) (any, error) {
	var input struct {
		Provider  string `json:"provider"`
		OrderID   int64  `json:"order_id"`
		AnomalyID int64  `json:"anomaly_id"`
		Action    string `json:"action"`
	}
	if err := decodeAdmin(raw, &input); err != nil {
		return nil, err
	}
	if !isPayReviewProvider(input.Provider) || !isPayReviewAction(input.Action) || input.OrderID < 0 || input.AnomalyID < 0 {
		return nil, adminInvalid("Проверьте выбранный платёж и действие.")
	}
	item, err := b.findReviewCase(ctx, payReviewRef{provider: input.Provider, orderID: input.OrderID, anomalyID: input.AnomalyID})
	if err != nil {
		return nil, err
	}
	resolution, err := buildReviewResolution(item, input.Action, actor)
	if err != nil {
		return nil, err
	}
	if _, err = b.payLedger.PreviewPaymentReviewResolution(ctx, resolution); err != nil {
		return nil, err
	}
	token := b.newAdminConfirmation(adminConfirmation{Actor: actor, OrderID: item.OrderID, Action: "review", Resolution: resolution})
	return map[string]any{"token": token, "message": fmt.Sprintf("Заказ #%d, %s. Действие: %s. Будут обработаны %d записей платежа.", item.OrderID, item.Provider, b.payReviewActionLabel("ru", input.Action), len(item.Targets))}, nil
}

func (b *Bot) confirmAdminAction(ctx context.Context, actor int64, token string, c adminConfirmation) (any, error) {
	switch c.Action {
	case "refund":
		o, err := b.order.GetOrder(ctx, c.OrderID)
		if err != nil {
			return nil, err
		}
		if o.PaymentID != c.PaymentID {
			return nil, adminConflict("Платёж изменился. Повторите проверку возврата.")
		}
		return map[string]string{"message": b.redactAdminMessage(b.executeAdminRefund(ctx, actor, c.OrderID, c.AmountMinor, "ru"))}, nil
	case "review":
		if _, err := b.payLedger.PreviewPaymentReviewResolution(ctx, c.Resolution); err != nil {
			return nil, err
		}
		err := b.payLedger.ResolvePaymentReview(ctx, c.Resolution)
		if err != nil && !errors.Is(err, storage.ErrPaymentNeedsReview) {
			return nil, err
		}
		return map[string]string{"message": "Решение по платежу сохранено."}, nil
	case "balance":
		balance, err := b.balances.AdjustBalance(ctx, c.UserID, c.DeltaUSD, "admin_adjust:"+token+": "+c.Reason, actor)
		return map[string]any{"balance_usd": balance, "message": "Баланс изменён."}, err
	}
	return nil, adminInvalid("Неизвестное подтверждение.")
}

// Transport errors may contain URLs. Credentials never belong in browser responses.
func (b *Bot) redactAdminMessage(message string) string {
	for _, secret := range []string{b.cfg.BotToken, b.cfg.CryptoBotToken, b.cfg.YooKassaSecretKey, b.cfg.StripeSecretKey, b.cfg.StripeWebhookSecret, b.cfg.TONAPIKey, b.cfg.NowpaymentsAPIKey, b.cfg.NowpaymentsIPNSecret, b.cfg.RedisPassword, b.cfg.TelegramWebhookSecret, b.cfg.OutboundWebhookSecret} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[скрыто]")
		}
	}
	return message
}

// Receipt delivery keeps the original claim/finish protocol and buyer language.
func (b *Bot) attachAdminReceipt(ctx context.Context, actor, id int64, rawURL string) (string, error) {
	receipt, err := b.taxReceipts.Attach(ctx, id, actor, rawURL)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidReceiptURL) {
			return "", adminInvalid("Нужна ссылка на чек «Мой налог» с lknpd.nalog.ru.")
		}
		if errors.Is(err, storage.ErrReceiptConflict) {
			return "", adminConflict("К заказу уже прикреплён другой чек.")
		}
		return "", err
	}
	if receipt.DeliveryStatus == "sent" {
		return "Чек уже отправлен покупателю.", nil
	}
	token := uuid.NewString()
	claimed, err := b.taxReceipts.ClaimDelivery(ctx, id, token)
	if err != nil {
		return "", err
	}
	if !claimed {
		return "Чек уже отправляется.", nil
	}
	lang := b.userLang(ctx, receipt.UserID)
	markup, _ := json.Marshal(tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonURL(b.t(lang, "webapp_order_receipt"), receipt.URL))))
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	api := *b.api
	api.Client = receiptHTTPClient{ctx: sendCtx, inner: b.api.Client}
	response, sendErr := api.MakeRequest("sendMessage", tgbotapi.Params{"chat_id": strconv.FormatInt(receipt.UserID, 10), "text": fmt.Sprintf(b.t(lang, "buyer_tax_receipt"), id), "reply_markup": string(markup)})
	var sent tgbotapi.Message
	if sendErr == nil {
		sendErr = json.Unmarshal(response.Result, &sent)
		if sendErr == nil && sent.MessageID <= 0 {
			sendErr = errors.New("missing message ID")
		}
	}
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer saveCancel()
	if err = b.taxReceipts.FinishDelivery(saveCtx, id, token, sent.MessageID, sendErr == nil); err != nil {
		return "", adminConflict("Не удалось сохранить результат отправки чека. Обновите заказ.")
	}
	if sendErr != nil {
		return "Чек сохранён, но отправка не удалась. Нажмите отправку ещё раз.", nil
	}
	return "Чек отправлен покупателю.", nil
}
