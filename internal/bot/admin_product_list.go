package bot

import (
	"context"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"strconv"
	"strings"
)

func (b *Bot) handleListProduct(ctx context.Context, msg *tgbotapi.Message) {
	if msg.From == nil || msg.Chat == nil || !b.isAdmin(msg.From.ID) || msg.Chat.ID != msg.From.ID {
		return
	}
	lang := msg.From.LanguageCode
	send := func(key string) { b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, key))) }
	const perPage = 20
	page := 1
	args := strings.Fields(msg.CommandArguments())
	if len(args) > 1 {
		send("admin_product_list_usage")
		return
	}
	if len(args) == 1 {
		var err error
		page, err = strconv.Atoi(args[0])
		if err != nil || page < 1 || page > int(^uint(0)>>1)/perPage {
			send("admin_product_list_usage")
			return
		}
	}
	products, total, err := b.adminProducts.ListProductsAdmin(ctx, perPage, (page-1)*perPage)
	if err != nil {
		b.loggerFor(ctx).Error("list administrator products", "error", err)
		send("error_short")
		return
	}
	if total == 0 {
		send("admin_product_list_empty")
		return
	}
	if len(products) == 0 {
		send("admin_product_list_page_missing")
		return
	}
	var sb strings.Builder
	pages := (total-1)/perPage + 1
	sb.WriteString(fmt.Sprintf(b.t(lang, "admin_product_list_title"), total, page, pages))
	for _, p := range products {
		name := []rune(strings.Join(strings.Fields(p.Name), " "))
		if len(name) > 80 {
			name = append(name[:79], '…')
		}
		status := ""
		if !p.IsActive {
			status = " " + b.t(lang, "admin_product_list_inactive")
		}
		fmt.Fprintf(&sb, "%d: %s%s\n", p.ID, string(name), status)
	}
	message := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
	b.send(message)
}
