package bot

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *Bot) isAdmin(userID int64) bool {
	for _, id := range b.cfg.AdminIDs {
		if id == userID {
			return true
		}
	}
	return false
}

func (b *Bot) handleAdmin(msg *tgbotapi.Message) {
	if !b.isAdmin(msg.From.ID) {
		return
	}

	text := b.t(msg.From.LanguageCode, "admin_panel")

	b.sendOrEditStyled(msg.Chat.ID, 0, text, "", StyledKeyboard{{Btn(b.t(msg.From.LanguageCode, "admin_rub_rate_button"), "admin:rubrate")}})
}
