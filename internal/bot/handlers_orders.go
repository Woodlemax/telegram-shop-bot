package bot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"shop_bot/internal/shop"
	"shop_bot/internal/storage"
)

func (b *Bot) handleOrders(ctx context.Context, msg *tgbotapi.Message) {
	b.sendOrders(ctx, msg.Chat.ID, msg.From.ID, 0, msg.From.LanguageCode)
}
func (b *Bot) sendOrders(ctx context.Context, chatID, userID int64, msgID int, lang string) {
	b.sendOrdersPage(ctx, chatID, userID, msgID, lang, 1)
}
func (b *Bot) sendOrdersPage(ctx context.Context, chatID, userID int64, msgID int, lang string, page int) {
	if page < 1 || page > 1000000 {
		return
	}
	orders, total, err := b.order.GetUserOrdersPaged(ctx, userID, 10, (page-1)*10)
	if err != nil {
		b.loggerFor(ctx).Error("get user orders", "error", err)
		b.sendOrEditStyled(chatID, msgID, b.t(lang, "error_load_orders"), "", nil)
		return
	}
	kb := StyledKeyboard{}
	for _, order := range orders {
		if order.Status == storage.OrderStatusPending && order.PaymentState == storage.PaymentStatePending {
			kb = append(kb, []StyledButton{
				Btn(fmt.Sprintf("%s #%d", b.t(lang, "webapp_order_pay_continue"), order.ID), fmt.Sprintf("order:resume:%d", order.ID)),
				Btn(fmt.Sprintf("%s #%d", b.t(lang, "btn_cancel_order"), order.ID), fmt.Sprintf("order:cancelask:%d", order.ID)),
			})
		}
	}
	pages := (total + 9) / 10
	if pages > 1 {
		var pager []StyledButton
		if page > 1 {
			pager = append(pager, Btn("‹", fmt.Sprintf("orders:page:%d", page-1)))
		}
		if page < pages {
			pager = append(pager, Btn("›", fmt.Sprintf("orders:page:%d", page+1)))
		}
		kb = append(kb, pager)
	}
	kb = append(kb, []StyledButton{Btn(b.t(lang, "digital_library_title"), "digital:library")})
	kb = append(kb, []StyledButton{Btn(b.t(lang, "btn_back"), "back:menu"), Btn(b.t(lang, "btn_menu"), "back:menu")})
	text := b.formatOrdersText(lang, orders)
	if len(orders) == 0 {
		text = b.t(lang, "orders_empty")
	}
	if pages > 1 {
		text += fmt.Sprintf("\n%d / %d", page, pages)
	}
	b.sendOrEditStyled(chatID, msgID, text, "HTML", kb)
}

// Resume from committed snapshots; this never creates an order or reads/clears
// the current cart, and reuses the existing payment handlers for every rail.
func (b *Bot) onOrderResume(ctx context.Context, cbID string, chatID, userID int64, msgID int, data, lang string) {
	id, err := parseIDFromCallback(data, "order:resume:")
	if err != nil {
		b.ack(cbID)
		return
	}
	order, err := b.loadPayableOrder(ctx, userID, id)
	if err != nil {
		key := "webapp_order_pay_unavailable"
		if errors.Is(err, storage.ErrNotFound) {
			key = "order_not_found"
		}
		b.alert(cbID, b.t(lang, key))
		return
	}
	b.ack(cbID)
	text := b.formatOrdersText(lang, []storage.Order{*order}) + b.t(lang, "payment_methods_items_header")
	var items strings.Builder
	for _, item := range order.Items {
		fmt.Fprintf(&items, "• %s × %d\n", escapeHTML(item.ProductName), item.Quantity)
	}
	text += items.String() + b.t(lang, "payment_methods_hint")
	if shop.IsFreeOrder(order) {
		b.sendOrEditStyled(chatID, msgID, text, "HTML", StyledKeyboard{
			{Btn(b.t(lang, "free_order_button"), fmt.Sprintf("order:free:%d", id))},
			{Btn(b.t(lang, "btn_cancel_order"), fmt.Sprintf("order:cancelask:%d", id)), Btn(b.t(lang, "btn_orders"), "back:orders")},
		})
		return
	}
	_, subDays, err := b.orderSubscriptionProduct(ctx, order)
	if err != nil {
		b.sendOrEditStyled(chatID, msgID, b.t(lang, "error_short"), "", nil)
		return
	}
	starsOnly := subDays > 0
	cryptoOK := b.cryptoPaymentsEnabled() && !starsOnly && math.Round(order.TotalUSD*100) > 0
	yooOK := b.yooKassaPaymentsEnabled() && !starsOnly && math.Round(order.TotalRUB*100) > 0
	stripeOK := b.stripePaymentsEnabled() && !starsOnly && math.Round(order.TotalUSD*100) >= 50
	tonOK := b.tonPaymentsEnabled() && !starsOnly && order.TotalTonNano > 0
	nowOK := b.nowpaymentsEnabled() && !starsOnly && math.Round(order.TotalUSD*100) > 0
	balance := 0.0
	if b.balances != nil && !starsOnly && !b.starsOnlyPayments() && order.TotalUSD > 0 {
		if value, err := b.balances.GetBalance(ctx, userID); err == nil {
			balance = value
		}
	}
	kb := paymentMethodKeyboard(id, cryptoOK, yooOK, stripeOK, tonOK, nowOK, balance, order.TotalRUB, order.TotalStars, order.TotalUSD, order.TotalTonNano, lang, b)
	if order.TotalStars <= 0 {
		kb = kb[1:]
	}
	for i := range kb {
		for j := range kb[i] {
			if kb[i][j].CallbackData == fmt.Sprintf("order:cancel:%d", id) {
				kb[i][j].CallbackData = fmt.Sprintf("order:cancelask:%d", id)
			}
		}
	}
	b.sendOrEditStyled(chatID, msgID, text, "HTML", kb)
}
func (b *Bot) onOrderCancelAsk(ctx context.Context, cbID string, chatID, userID int64, msgID int, data, lang string) {
	id, err := parseIDFromCallback(data, "order:cancelask:")
	if err != nil {
		b.ack(cbID)
		return
	}
	if _, err := b.loadPayableOrder(ctx, userID, id); err != nil {
		key := "webapp_order_cancel_unavailable"
		if errors.Is(err, storage.ErrNotFound) {
			key = "order_not_found"
		}
		b.alert(cbID, b.t(lang, key))
		return
	}
	b.ack(cbID)
	b.sendOrEditStyled(chatID, msgID, fmt.Sprintf("%s #%d", b.t(lang, "webapp_order_cancel_confirm"), id), "", StyledKeyboard{
		{Btn(b.t(lang, "btn_cancel_order"), fmt.Sprintf("order:cancel:%d", id))},
		{Btn(b.t(lang, "btn_back"), "back:orders")},
	})
}
