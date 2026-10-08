package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"shop_bot/internal/storage"
)

type productTelegramState struct {
	ProductID int64
	Until     time.Time
}

func (b *Bot) onAdminProductTelegram(ctx context.Context, chatID, userID int64, data, lang string) {
	parts := strings.Split(data, ":")
	if len(parts) != 4 {
		return
	}
	id, err := parseIDFromCallback(data, "admin:telegram:"+parts[2]+":")
	if err != nil {
		return
	}
	p, err := b.products.GetProduct(ctx, id)
	if err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_product_not_found")))
		return
	}
	switch parts[2] {
	case "remove":
		b.saveProductTelegram(ctx, chatID, id, "", lang)
	case "cancel":
		b.sendAdminProductDetails(chatID, p, lang)
	case "edit":
		b.rubRateInput.Delete(userID)
		_ = b.archives.CancelUpload(ctx, userID)
		b.cart.CancelPriceInput(ctx, userID)
		_ = b.fsm.DelAddProductState(ctx, userID)
		_ = b.fsm.DelPromoState(ctx, userID)
		_ = b.fsm.DelReviewState(ctx, userID)
		b.telegramInput.Store(userID, productTelegramState{id, time.Now().Add(15 * time.Minute)})
		b.sendOrEditStyled(chatID, 0, fmt.Sprintf(b.t(lang, "admin_telegram_prompt"), id), "", StyledKeyboard{{Btn(b.t(lang, "admin_rub_rate_cancel"), fmt.Sprintf("admin:telegram:cancel:%d", id))}})
	}
}
func (b *Bot) handleProductTelegramInput(ctx context.Context, msg *tgbotapi.Message) bool {
	if msg.From == nil || msg.Chat == nil {
		return false
	}
	pending, ok := b.telegramInput.Load(msg.From.ID)
	if !ok {
		return false
	}
	state := pending.(productTelegramState)
	if !b.isAdmin(msg.From.ID) || time.Now().After(state.Until) {
		b.telegramInput.Delete(msg.From.ID)
		return false
	}
	if msg.Chat.ID != msg.From.ID {
		return false
	}
	if msg.IsCommand() {
		b.telegramInput.Delete(msg.From.ID)
		if msg.Command() == "cancel" {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "admin_cancelled")))
			return true
		}
		return false
	}
	raw := strings.TrimSpace(msg.Text)
	if raw == "" {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "admin_telegram_invalid")))
		return true
	}
	if raw == "-" {
		raw = ""
	}
	if b.saveProductTelegram(ctx, msg.Chat.ID, state.ProductID, raw, msg.From.LanguageCode) {
		b.telegramInput.Delete(msg.From.ID)
	}
	return true
}
func (b *Bot) saveProductTelegram(ctx context.Context, chatID, id int64, raw, lang string) bool {
	link, err := storage.NormalizeTelegramURL(raw)
	if err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_telegram_invalid")))
		return false
	}
	p, err := b.products.GetProduct(ctx, id)
	if err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_product_not_found")))
		return false
	}
	p.TelegramURL = link
	if err := b.products.UpdateProduct(ctx, p); err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_product_update_failed")))
		return false
	}
	b.sendAdminProductDetails(chatID, p, lang)
	return true
}
