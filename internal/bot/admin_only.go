package bot

import (
	"encoding/json"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"strconv"
	"strings"
)

func (b *Bot) adminOnly() bool { return b.cfg != nil && b.cfg.BotAdminOnly }

// Payment updates are routed before this gate; administration stays private.
func (b *Bot) routeAdminOnlyMessage(msg *tgbotapi.Message) bool {
	if msg.From == nil || msg.Chat == nil {
		return true
	}
	if msg.Command() == "start" {
		if msg.Chat.ID == msg.From.ID {
			b.sendMiniAppMessage(msg, "bot_start_welcome")
		} else {
			b.sendMiniAppEntry(msg)
		}
		return true
	}
	if b.isAdmin(msg.From.ID) && msg.Chat.ID == msg.From.ID {
		switch msg.Command() {
		case "help":
			b.handleAdmin(msg)
			return true
		case "", "skip", "done", "cancel", "admin", "rubrate", "setarchive",
			"addproduct", "editproduct", "deleteproduct", "listproduct", "orders_all", "order", "setdelivered",
			"reviews", "addcategory", "editcategory", "deletecategory", "listcategories",
			"addpromo", "listpromos", "deletepromo", "analytics", "payreview", "refund",
			"paystatus", "setbalance", "export_orders", "btnstyle":
			return false
		}
	}
	b.sendMiniAppEntry(msg)
	return true
}

func adminCallback(data string) bool {
	return strings.HasPrefix(data, "admin:") || strings.HasPrefix(data, "analytics:") || strings.HasPrefix(data, "review:del:")
}

func (b *Bot) sendMiniAppEntry(msg *tgbotapi.Message) {
	b.sendMiniAppMessage(msg, "bot_miniapp_only")
}

func (b *Bot) sendMiniAppMessage(msg *tgbotapi.Message, textKey string) {
	b.sendShopInfoMessage(msg.Chat.ID, msg.From.ID, msg.From.LanguageCode, textKey, 0)
}

func (b *Bot) sendShopInfoMessage(chatID, userID int64, lang, textKey string, msgID int) {
	text := b.t(lang, textKey)
	if textKey == "bot_contacts_title" {
		text += "\n\n" + b.cfg.ShopContactsText
	}
	params := tgbotapi.Params{"chat_id": strconv.FormatInt(chatID, 10), "text": text}
	rows := make([][]any, 0, 2)
	if chatID == userID {
		if textKey == "bot_contacts_title" {
			rows = append(rows, []any{map[string]any{"text": b.t(lang, "btn_back"), "callback_data": "info:welcome"}})
		} else {
			if b.cfg.WebAppURL != "" {
				rows = append(rows, []any{map[string]any{"text": b.t(lang, "bot_open_shop"), "web_app": map[string]string{"url": b.cfg.WebAppURL}}})
			}
			if textKey == "bot_start_welcome" {
				info := make([]any, 0, 2)
				if b.cfg.ShopOfferURL != "" {
					info = append(info, map[string]string{"text": b.t(lang, "bot_offer"), "url": b.cfg.ShopOfferURL})
				}
				if b.cfg.ShopContactsText != "" {
					info = append(info, map[string]string{"text": b.t(lang, "bot_contacts"), "callback_data": "info:contacts"})
				}
				if len(info) > 0 {
					rows = append(rows, info)
				}
			}
		}
	}
	markup, err := json.Marshal(map[string]any{"inline_keyboard": rows})
	if err != nil {
		return
	}
	// Empty markup on edits removes any previous information keyboard.
	if len(rows) > 0 || msgID > 0 {
		params["reply_markup"] = string(markup)
	}
	method := "sendMessage"
	if msgID > 0 {
		method = "editMessageText"
		params["message_id"] = strconv.Itoa(msgID)
	}
	if _, err := b.api.MakeRequest(method, params); err != nil && !isNotModified(err) {
		b.logger.Warn("send shop information", "error", err)
	}
}

// Public information callbacks are the only buyer callbacks allowed alongside administration.
func (b *Bot) handleShopInfoCallback(cb *tgbotapi.CallbackQuery) bool {
	if cb.Data != "info:contacts" && cb.Data != "info:welcome" {
		return false
	}
	b.ack(cb.ID)
	if cb.From == nil || cb.Message == nil || cb.Message.Chat == nil || cb.Message.Chat.ID != cb.From.ID {
		return true
	}
	if cb.Data == "info:contacts" {
		if b.cfg.ShopContactsText != "" {
			b.sendShopInfoMessage(cb.From.ID, cb.From.ID, cb.From.LanguageCode, "bot_contacts_title", cb.Message.MessageID)
		}
	} else {
		b.sendShopInfoMessage(cb.From.ID, cb.From.ID, cb.From.LanguageCode, "bot_start_welcome", cb.Message.MessageID)
	}
	return true
}
