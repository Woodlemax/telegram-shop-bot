package bot

import (
	"context"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/shop"
	"shop_bot/internal/storage"
	"strconv"
	"strings"
)

func (b *Bot) onOpenPrice(ctx context.Context, chatID, userID int64, data, lang string) {
	id, err := parseIDFromCallback(data, "price:enter:")
	if err != nil || chatID != userID {
		return
	}
	if err := b.cart.BeginPriceInput(ctx, userID, chatID, id); err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "open_price_invalid")))
		return
	}
	_ = b.fsm.DelPromoState(ctx, userID)
	b.send(tgbotapi.NewMessage(chatID, b.t(lang, "open_price_prompt")))
}

func (b *Bot) handlePriceInput(ctx context.Context, msg *tgbotapi.Message) bool {
	if b.cart == nil {
		return false
	}
	id, err := b.cart.PendingPriceInput(ctx, msg.From.ID, msg.Chat.ID)
	if err != nil || id == 0 {
		return false
	}
	if msg.Command() == "cancel" {
		b.cart.CancelPriceInput(ctx, msg.From.ID)
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "admin_cancelled")))
		return true
	}
	if msg.IsCommand() {
		b.cart.CancelPriceInput(ctx, msg.From.ID)
		return false
	}
	text := strings.TrimSpace(msg.Text)
	valid := text != ""
	for _, r := range text {
		if r < '0' || r > '9' {
			valid = false
		}
	}
	amount, parseErr := strconv.Atoi(text)
	if !valid || parseErr != nil || b.cart.SetPrice(ctx, msg.From.ID, id, amount) != nil {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "open_price_invalid")))
		return true
	}
	b.cart.CancelPriceInput(ctx, msg.From.ID)
	b.sendCart(ctx, msg.Chat.ID, msg.From.ID, 0, msg.From.LanguageCode)
	return true
}

func (b *Bot) onAdminOpenPrice(ctx context.Context, chatID int64, data, lang string) {
	id, err := parseIDFromCallback(data, "admin:openprice:")
	if err != nil {
		return
	}
	p, err := b.products.GetProduct(ctx, id)
	if err != nil || p.SubPeriodDays > 0 {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "open_price_sub_error")))
		return
	}
	p.OpenPrice = !p.OpenPrice
	if p.OpenPrice {
		zero := float64(0)
		p.PriceRUB = &zero
		p.PriceUSD = 0
		p.PriceStars = 0
	}
	if err := b.products.UpdateProduct(ctx, p); err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_product_update_failed")))
		return
	}
	if cache, ok := b.products.(interface {
		Invalidate(context.Context, ...int64)
	}); ok {
		cache.Invalidate(ctx, id)
	}
	b.sendAdminProductDetails(chatID, p, lang)
}

func (b *Bot) onFreeOrder(ctx context.Context, cbID string, chatID, userID int64, data, lang string) {
	id, err := parseIDFromCallback(data, "order:free:")
	if err != nil {
		b.ack(cbID)
		return
	}
	if err := b.order.ConfirmFreeOrder(ctx, id, userID); err != nil {
		b.alert(cbID, b.t(lang, "free_order_error"))
		return
	}
	b.ack(cbID)
	b.sendOrEditStyled(chatID, 0, fmt.Sprintf(b.t(lang, "free_order_done"), id), "", StyledKeyboard{{Btn(b.t(lang, "btn_orders"), "back:orders"), Btn(b.t(lang, "btn_menu"), "back:menu")}})
	b.ProcessDigitalDeliveries(ctx)
}

func freeCart(view *shop.CartView) bool {
	return len(view.Items) > 0 && view.TotalUSD == 0 && view.TotalStars == 0 && view.TotalRUB == 0 && view.TotalTONNano == 0 && !cartHasSubscription(view)
}

func productAmount(p *storage.Product) float64 {
	if p.PriceRUB != nil {
		return *p.PriceRUB
	}
	return p.PriceUSD
}
func currencyText(text string, rub bool) string {
	if rub {
		return strings.ReplaceAll(text, "$", "₽")
	}
	return text
}
