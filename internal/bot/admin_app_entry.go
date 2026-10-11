package bot

import (
	"encoding/json"
	"net/url"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *Bot) adminMiniAppOnly() bool {
	return b.cfg != nil && b.cfg.AdminMiniAppOnly && b.cfg.WebAppURL != ""
}
func (b *Bot) adminMiniAppURL() string {
	u, err := url.Parse(b.cfg.WebAppURL)
	if err != nil {
		return ""
	}
	u.Path = "/admin/"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func (b *Bot) sendAdminMiniApp(msg *tgbotapi.Message) {
	if msg.From == nil || msg.Chat == nil || msg.Chat.ID != msg.From.ID || !b.isAdmin(msg.From.ID) {
		return
	}
	markup, _ := json.Marshal(map[string]any{"inline_keyboard": [][]any{{map[string]any{"text": b.t(msg.From.LanguageCode, "admin_app_open"), "web_app": map[string]string{"url": b.adminMiniAppURL()}}}}})
	if _, err := b.api.MakeRequest("sendMessage", tgbotapi.Params{"chat_id": strconv.FormatInt(msg.Chat.ID, 10), "text": b.t(msg.From.LanguageCode, "admin_app_entry"), "reply_markup": string(markup)}); err != nil {
		b.logger.Warn("send admin app entry failed")
	}
}
func adminCommand(command string) bool {
	switch command {
	case "admin", "rubrate", "setarchive", "addproduct", "editproduct", "deleteproduct", "listproduct", "orders_all", "order", "receipt", "setdelivered", "reviews", "addcategory", "editcategory", "deletecategory", "listcategories", "addpromo", "listpromos", "deletepromo", "analytics", "payreview", "refund", "paystatus", "setbalance", "export_orders", "btnstyle", "skip", "done", "cancel":
		return true
	}
	return false
}
func (b *Bot) routeAdminMiniAppMessage(msg *tgbotapi.Message) bool {
	if !b.adminMiniAppOnly() || msg.From == nil || msg.Chat == nil {
		return false
	}
	if adminCommand(msg.Command()) || (b.isAdmin(msg.From.ID) && (msg.Command() == "help" || msg.Command() == "")) {
		b.sendAdminMiniApp(msg)
		return true
	}
	return false
}
func (b *Bot) registerAdminAppCommand() {
	if !b.adminMiniAppOnly() {
		return
	}
	commands := `[{"command":"start","description":"Открыть магазин"},{"command":"admin","description":"Открыть админку"}]`
	for _, id := range b.cfg.AdminIDs {
		scope, _ := json.Marshal(map[string]any{"type": "chat", "chat_id": id})
		if _, err := b.api.MakeRequest("setMyCommands", tgbotapi.Params{"scope": string(scope), "commands": commands}); err != nil {
			b.logger.Warn("set admin commands failed")
		}
	}
}
