package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"shop_bot/internal/storage"
)

func (b *Bot) handleTaxReceipt(ctx context.Context, msg *tgbotapi.Message) {
	if msg.From == nil || msg.Chat == nil || !b.isAdmin(msg.From.ID) || msg.Chat.ID != msg.From.ID {
		return
	}
	lang := msg.From.LanguageCode
	args := strings.Fields(msg.CommandArguments())
	if len(args) != 2 {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_receipt_usage")))
		return
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_receipt_usage")))
		return
	}
	receipt, err := b.taxReceipts.Attach(ctx, id, msg.From.ID, args[1])
	if err != nil {
		key := "admin_receipt_error"
		switch {
		case errors.Is(err, storage.ErrInvalidReceiptURL):
			key = "admin_receipt_invalid_url"
		case errors.Is(err, storage.ErrReceiptConflict):
			key = "admin_receipt_conflict"
		case errors.Is(err, storage.ErrOrderStatusConflict):
			key = "admin_receipt_unavailable"
		}
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, key)))
		return
	}
	if receipt.DeliveryStatus == "sent" {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_receipt_already_sent")))
		return
	}
	token := uuid.NewString()
	claimed, err := b.taxReceipts.ClaimDelivery(ctx, id, token)
	if err != nil {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_receipt_error")))
		return
	}
	if !claimed {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_receipt_sending")))
		return
	}
	buyerLang := b.userLang(ctx, receipt.UserID)
	markup, _ := json.Marshal(tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonURL(b.t(buyerLang, "webapp_order_receipt"), receipt.URL))))
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	receiptAPI := *b.api
	receiptAPI.Client = receiptHTTPClient{ctx: sendCtx, inner: b.api.Client}
	response, sendErr := receiptAPI.MakeRequest("sendMessage", tgbotapi.Params{
		"chat_id": strconv.FormatInt(receipt.UserID, 10), "text": fmt.Sprintf(b.t(buyerLang, "buyer_tax_receipt"), id), "reply_markup": string(markup),
	})
	var sentMessage tgbotapi.Message
	if sendErr == nil {
		sendErr = json.Unmarshal(response.Result, &sentMessage)
		if sendErr == nil && sentMessage.MessageID <= 0 {
			sendErr = errors.New("missing message id")
		}
	}
	// Transport errors may contain a bot token; only the delivery outcome is logged.
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer saveCancel()
	if err = b.taxReceipts.FinishDelivery(saveCtx, id, token, sentMessage.MessageID, sendErr == nil); err != nil {
		b.loggerFor(ctx).Error("save tax receipt delivery", "order_id", id)
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_receipt_save_failed")))
		return
	}
	key := "admin_receipt_sent"
	if sendErr != nil {
		key = "admin_receipt_send_failed"
	}
	b.send(tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf(b.t(lang, key), id)))
}

// Apply a per-send deadline without mutating the shared Telegram client.
type receiptHTTPClient struct {
	ctx   context.Context
	inner interface {
		Do(*http.Request) (*http.Response, error)
	}
}

func (c receiptHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return c.inner.Do(req.WithContext(c.ctx))
}
