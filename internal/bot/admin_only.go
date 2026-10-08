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
			"addproduct", "editproduct", "deleteproduct", "orders_all", "order", "setdelivered",
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
	params := tgbotapi.Params{"chat_id": strconv.FormatInt(msg.Chat.ID, 10), "text": b.t(msg.From.LanguageCode, textKey)}
	if msg.Chat.ID == msg.From.ID && b.cfg.WebAppURL != "" {
		markup, err := json.Marshal(map[string]any{"inline_keyboard": [][]any{{map[string]any{
			"text": b.t(msg.From.LanguageCode, "bot_open_shop"), "web_app": map[string]string{"url": b.cfg.WebAppURL},
		}}}})
		if err == nil {
			params["reply_markup"] = string(markup)
		}
	}
	if _, err := b.api.MakeRequest("sendMessage", params); err != nil {
		b.logger.Warn("send Mini App entry", "error", err)
	}
}
