package bot

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/storage"
)

func (b *Bot) handleSetArchive(ctx context.Context, msg *tgbotapi.Message) {
	if msg.From == nil || !b.isAdmin(msg.From.ID) {
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(msg.CommandArguments()), 10, 64)
	if err != nil || id <= 0 {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "digital_admin_usage")))
		return
	}
	b.beginArchiveUpload(ctx, msg.Chat.ID, msg.From.ID, id, msg.From.LanguageCode)
}

func (b *Bot) beginArchiveUpload(ctx context.Context, chatID, userID, productID int64, lang string) {
	// Archives may only be attached from the administrator's private chat.
	if chatID != userID {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "digital_private_only")))
		return
	}
	if err := b.archives.BeginUpload(ctx, userID, chatID, productID); err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "digital_admin_product_error")))
		return
	}
	_ = b.fsm.DelAddProductState(ctx, userID)
	_ = b.fsm.DelPromoState(ctx, userID)
	b.send(tgbotapi.NewMessage(chatID, fmt.Sprintf(b.t(lang, "digital_admin_prompt"), productID)))
}

func (b *Bot) handleArchiveUpload(ctx context.Context, msg *tgbotapi.Message) bool {
	if b.archives == nil || msg.From == nil || msg.Chat == nil || msg.Chat.ID != msg.From.ID || !b.isAdmin(msg.From.ID) {
		return false
	}
	id, err := b.archives.PendingUpload(ctx, msg.From.ID, msg.Chat.ID)
	if err != nil {
		b.loggerFor(ctx).Error("read archive upload state")
		return true
	}
	if id == 0 {
		return false
	}
	if msg.Command() == "cancel" {
		_ = b.archives.CancelUpload(ctx, msg.From.ID)
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "admin_cancelled")))
		return true
	}
	if msg.Command() != "" {
		return false
	}
	if msg.Document == nil {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "digital_admin_zip_only")))
		return true
	}
	d := msg.Document
	version, err := b.archives.Add(ctx, id, d.FileID, d.FileName, int64(d.FileSize))
	if err != nil {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "digital_admin_zip_only")))
		return true
	}
	_ = b.archives.CancelUpload(ctx, msg.From.ID)
	if cache, ok := b.products.(interface {
		Invalidate(context.Context, ...int64)
	}); ok {
		cache.Invalidate(ctx, id)
	}
	b.send(tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf(b.t(msg.From.LanguageCode, "digital_admin_saved"), id, version)))
	return true
}

type deliveryHTTPClient struct {
	base tgbotapi.HTTPClient
	ctx  context.Context
}

func (c deliveryHTTPClient) Do(r *http.Request) (*http.Response, error) {
	return c.base.Do(r.Clone(c.ctx))
}

func (b *Bot) sendDigitalDocument(ctx context.Context, d *storage.DigitalDelivery, lang string) (int, error) {
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	api := *b.api
	api.Client = sanitizedTelegramClient{client: deliveryHTTPClient{b.api.Client, sendCtx}}
	doc := tgbotapi.NewDocument(d.UserID, tgbotapi.FileID(d.FileID))
	name := []rune(d.ProductName)
	if len(name) > 300 {
		name = name[:300]
	}
	doc.Caption = fmt.Sprintf(b.t(lang, "digital_delivery_caption"), string(name), d.ArchiveID, d.OrderID)
	doc.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(b.t(lang, "digital_download_again"), fmt.Sprintf("digital:download:%d", d.ID)),
	))
	msg, err := api.Send(doc)
	return msg.MessageID, err
}

// The durable queue is populated when the order is created. Claim only admits
// settled orders, so delivery survives a crash immediately after payment.
func (b *Bot) ProcessDigitalDeliveries(ctx context.Context) {
	if b.archives == nil {
		return
	}
	for i := 0; i < 10 && ctx.Err() == nil; i++ {
		d, err := b.archives.Claim(ctx)
		if err != nil {
			b.logger.Error("digital delivery claim failed")
			return
		}
		if d == nil {
			return
		}
		messageID, sendErr := b.sendDigitalDocument(ctx, d, b.userLang(ctx, d.UserID))
		// Save the result even during shutdown; an abandoned lease otherwise
		// becomes retryable on restart. Telegram has no sendDocument idempotency key.
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = b.archives.Finish(finishCtx, d, messageID, sendErr == nil)
		cancel()
		if err != nil {
			b.logger.Error("digital delivery result failed", "delivery_id", d.ID)
		}
		if sendErr != nil {
			b.logger.Warn("digital delivery will retry", "delivery_id", d.ID)
		}
	}
}

func (b *Bot) RunDigitalDeliveries(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		b.ProcessDigitalDeliveries(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) sendDigitalLibrary(ctx context.Context, chatID, userID int64, lang string) {
	if chatID != userID {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "digital_private_only")))
		return
	}
	items, err := b.archives.Library(ctx, userID)
	if err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "error_load_orders")))
		return
	}
	if len(items) == 0 {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "digital_library_empty")))
		return
	}
	kb := StyledKeyboard{}
	for _, d := range items {
		name := []rune(d.ProductName)
		if len(name) > 35 {
			name = name[:35]
		}
		kb = append(kb, []StyledButton{Btn(fmt.Sprintf("📦 %s · v%d", string(name), d.ArchiveID), fmt.Sprintf("digital:download:%d", d.ID))})
	}
	b.sendOrEditStyled(chatID, 0, b.t(lang, "digital_library_title"), "", kb)
}

func (b *Bot) onDigitalDownload(ctx context.Context, cbID string, chatID, userID int64, data, lang string) {
	id, err := parseIDFromCallback(data, "digital:download:")
	if err != nil || chatID != userID {
		b.alert(cbID, b.t(lang, "digital_no_access"))
		return
	}
	d, err := b.archives.Owned(ctx, userID, id)
	if err != nil {
		b.alert(cbID, b.t(lang, "digital_no_access"))
		return
	}
	b.ack(cbID)
	if _, err := b.sendDigitalDocument(ctx, d, lang); err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "digital_send_error")))
	}
}
