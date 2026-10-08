package bot

import (
	"context"
	"errors"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/storage"
	"strconv"
	"strings"
	"time"
)

func (b *Bot) handleAddPromo(ctx context.Context, msg *tgbotapi.Message) {
	if !b.isAdmin(msg.From.ID) || msg.Chat.ID != msg.From.ID {
		return
	}
	lang := msg.From.LanguageCode
	fail := func(key string) { b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, key))) }
	args := strings.Fields(msg.CommandArguments())
	if len(args) < 2 || len(args) > 6 {
		fail("admin_promo_usage")
		return
	}
	discount, err := strconv.Atoi(args[1])
	if err != nil {
		fail("admin_promo_usage")
		return
	}
	p := &storage.PromoCode{Code: strings.ToUpper(args[0]), Discount: discount, IsActive: true}
	// Named product selector does not consume the legacy positional options.
	positional := append([]string(nil), args[:2]...)
	for _, arg := range args[2:] {
		if strings.HasPrefix(arg, "products=") {
			if len(p.ProductIDs) > 0 {
				fail("admin_promo_usage")
				return
			}
			for _, value := range strings.Split(strings.TrimPrefix(arg, "products="), ",") {
				id, err := strconv.ParseInt(value, 10, 64)
				if err != nil || id <= 0 {
					fail("admin_promo_usage")
					return
				}
				p.ProductIDs = append(p.ProductIDs, id)
			}
		} else {
			positional = append(positional, arg)
		}
	}
	args = positional
	if len(args) > 5 {
		fail("admin_promo_usage")
		return
	}
	if len(args) > 2 {
		p.MaxUses, err = strconv.Atoi(args[2])
		if err != nil || p.MaxUses < 0 {
			fail("admin_promo_usage")
			return
		}
	}
	if len(args) > 3 {
		days, err := strconv.Atoi(args[3])
		if err != nil || days < 0 || days > 36500 {
			fail("admin_promo_usage")
			return
		}
		if days > 0 {
			expires := time.Now().UTC().AddDate(0, 0, days)
			p.ExpiresAt = &expires
		}
	}
	if len(args) > 4 {
		id, err := strconv.ParseInt(args[4], 10, 64)
		if err != nil || id <= 0 {
			fail("admin_promo_usage")
			return
		}
		if _, err := b.products.GetCategory(ctx, id); err != nil {
			fail("admin_promo_category_invalid")
			return
		}
		p.CategoryID = &id
	}
	if err := storage.ValidatePromo(p); err != nil {
		fail("admin_promo_usage")
		return
	}
	for _, id := range p.ProductIDs {
		if _, err := b.products.GetProduct(ctx, id); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				fail("admin_promo_product_invalid")
			} else {
				fail("error_short")
			}
			return
		}
	}
	if _, err := b.promos.CreatePromo(ctx, p); err != nil {
		if errors.Is(err, storage.ErrPromoExists) {
			fail("admin_promo_exists")
		} else {
			b.loggerFor(ctx).Error("create promo", "error", err)
			fail("error_short")
		}
		return
	}
	fail("admin_promo_created")
}

func (b *Bot) handleListPromos(ctx context.Context, msg *tgbotapi.Message) {
	if !b.isAdmin(msg.From.ID) || msg.Chat.ID != msg.From.ID {
		return
	}
	lang := msg.From.LanguageCode
	promos, err := b.promos.ListPromos(ctx)
	if err != nil {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "error_short")))
		return
	}
	if len(promos) == 0 {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_promo_empty")))
		return
	}
	var sb strings.Builder
	for _, p := range promos {
		limit := b.t(lang, "admin_promo_unlimited")
		if p.MaxUses > 0 {
			limit = strconv.Itoa(p.MaxUses)
		}
		expires := b.t(lang, "admin_promo_no_expiry")
		if p.ExpiresAt != nil {
			expires = p.ExpiresAt.UTC().Format("2006-01-02 15:04 UTC")
		}
		category := b.t(lang, "admin_promo_all_categories")
		if p.CategoryID != nil {
			category = strconv.FormatInt(*p.CategoryID, 10)
		}
		sb.WriteString(fmt.Sprintf("%d: %s (−%d%%)\n", p.ID, p.Code, p.Discount))
		if len(p.ProductIDs) > 0 {
			ids := make([]string, len(p.ProductIDs))
			for i, id := range p.ProductIDs {
				ids[i] = strconv.FormatInt(id, 10)
			}
			sb.WriteString(fmt.Sprintf(b.t(lang, "admin_promo_product_details"), p.UsedCount, limit, expires, strings.Join(ids, ", ")))
		} else {
			sb.WriteString(fmt.Sprintf(b.t(lang, "admin_promo_details"), p.UsedCount, limit, expires, category))
		}
	}
	b.send(tgbotapi.NewMessage(msg.Chat.ID, sb.String()))
}

func (b *Bot) handleDeletePromo(ctx context.Context, msg *tgbotapi.Message) {
	if !b.isAdmin(msg.From.ID) || msg.Chat.ID != msg.From.ID {
		return
	}
	lang := msg.From.LanguageCode
	id, err := strconv.ParseInt(strings.TrimSpace(msg.CommandArguments()), 10, 64)
	if err != nil || id <= 0 {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_promo_delete_usage")))
		return
	}
	if err := b.promos.DeactivatePromo(ctx, id); err != nil {
		key := "error_short"
		if errors.Is(err, storage.ErrNotFound) {
			key = "admin_promo_missing"
		}
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, key)))
		return
	}
	b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_promo_deactivated")))
}
