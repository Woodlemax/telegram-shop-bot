package bot

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"shop_bot/internal/service"
)

var rubRateNumber = regexp.MustCompile(`^[0-9]{1,7}([.,][0-9]{1,4})?$`)

func parseRUBRate(text string) (float64, error) {
	text = strings.TrimSpace(text)
	if !rubRateNumber.MatchString(text) {
		return 0, service.ErrInvalidRUBRate
	}
	rate, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
	if err != nil || !service.ValidRUBRate(rate) {
		return 0, service.ErrInvalidRUBRate
	}
	return rate, nil
}
func (b *Bot) currentRUBRate() float64 {
	if b.exchange != nil {
		return b.exchange.GetUSDToRUBRate()
	}
	if b.cfg != nil {
		return b.cfg.USDToRUBRate
	}
	return 0
}
func (b *Bot) sendRUBRate(chatID int64, msgID int, lang string) {
	stars := b.exchange.GetUSDToStarsRate()
	text := fmt.Sprintf(b.t(lang, "admin_rub_rate_current"), formatPayStatusRate(b.currentRUBRate()), stars)
	b.sendOrEditStyled(chatID, msgID, text, "", StyledKeyboard{{Btn(b.t(lang, "admin_rub_rate_edit"), "admin:rubrate:edit")}})
}
func (b *Bot) handleRUBRate(ctx context.Context, msg *tgbotapi.Message) {
	if msg.From == nil || !b.isAdmin(msg.From.ID) {
		return
	}
	if msg.Chat.ID != msg.From.ID {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "admin_rub_rate_private")))
		return
	}
	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		b.sendRUBRate(msg.Chat.ID, 0, msg.From.LanguageCode)
		return
	}
	b.saveRUBRate(ctx, msg.Chat.ID, args, msg.From.LanguageCode)
}
func (b *Bot) beginRUBRateInput(ctx context.Context, chatID, userID int64, lang string) {
	// End other dialogs so the next number cannot edit a product or a buyer's price.
	_ = b.archives.CancelUpload(ctx, userID)
	b.cart.CancelPriceInput(ctx, userID)
	_ = b.fsm.DelAddProductState(ctx, userID)
	_ = b.fsm.DelPromoState(ctx, userID)
	_ = b.fsm.DelReviewState(ctx, userID)
	b.rubRateInput.Store(userID, time.Now().Add(15*time.Minute))
	b.sendOrEditStyled(chatID, 0, b.t(lang, "admin_rub_rate_prompt"), "", StyledKeyboard{{Btn(b.t(lang, "admin_rub_rate_cancel"), "admin:rubrate")}})
}
func (b *Bot) handleRUBRateInput(ctx context.Context, msg *tgbotapi.Message) bool {
	if msg.From == nil || msg.Chat == nil {
		return false
	}
	pending, ok := b.rubRateInput.Load(msg.From.ID)
	if !ok {
		return false
	}
	if !b.isAdmin(msg.From.ID) || time.Now().After(pending.(time.Time)) {
		b.rubRateInput.Delete(msg.From.ID)
		return false
	}
	if msg.Chat.ID != msg.From.ID {
		return false
	}
	if msg.IsCommand() {
		b.rubRateInput.Delete(msg.From.ID)
		if msg.Command() == "cancel" {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "admin_cancelled")))
			return true
		}
		return false
	}
	if b.saveRUBRate(ctx, msg.Chat.ID, msg.Text, msg.From.LanguageCode) {
		b.rubRateInput.Delete(msg.From.ID)
	}
	return true
}
func (b *Bot) saveRUBRate(ctx context.Context, chatID int64, text, lang string) bool {
	rate, err := parseRUBRate(text)
	if err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_rub_rate_invalid")))
		return false
	}
	if err = b.exchange.UpdateRUBRate(ctx, rate); err != nil {
		b.loggerFor(ctx).Error("save administrator RUB rate", "error", err)
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_rub_rate_failed")))
		return false
	}
	b.send(tgbotapi.NewMessage(chatID, fmt.Sprintf(b.t(lang, "admin_rub_rate_saved"), formatPayStatusRate(rate))))
	b.sendRUBRate(chatID, 0, lang)
	return true
}
