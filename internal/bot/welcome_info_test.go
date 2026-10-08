package bot

import (
	"encoding/json"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/config"
	"testing"
)

func TestWelcomeOfferAndContacts(t *testing.T) {
	const contacts = "Seller Example\nseller@example.test\n@SellerExample\n<literal text>"
	e := newE2EEnvWithConfig(t, func(c *config.Config) {
		c.BotAdminOnly = true
		c.WebAppURL = "https://shop.example/app/"
		c.ShopOfferURL = "https://shop.example/offer"
		c.ShopContactsText = contacts
	})
	for _, user := range []int64{1915, e2eAdminID} {
		for _, lang := range []string{"ru", "en"} {
			message := requireCall(t, e.cmd(user, "/start", lang), "sendMessage", "")
			var markup struct {
				Rows [][]struct {
					Text     string `json:"text"`
					URL      string `json:"url"`
					Callback string `json:"callback_data"`
					WebApp   struct {
						URL string `json:"url"`
					} `json:"web_app"`
				} `json:"inline_keyboard"`
			}
			if err := json.Unmarshal([]byte(message.markup()), &markup); err != nil || len(markup.Rows) != 2 || len(markup.Rows[0]) != 1 || len(markup.Rows[1]) != 2 {
				t.Fatal("welcome information layout missing", err, message)
			}
			if markup.Rows[0][0].WebApp.URL != e.bot.cfg.WebAppURL || markup.Rows[1][0].URL != e.bot.cfg.ShopOfferURL || markup.Rows[1][0].Text != e.bot.t(lang, "bot_offer") || markup.Rows[1][1].Callback != "info:contacts" || markup.Rows[1][1].Text != e.bot.t(lang, "bot_contacts") {
				t.Fatal("welcome button targets/text incorrect", markup)
			}
			calls := e.cb(user, "info:contacts", lang)
			contactMessage := requireCall(t, calls, "editMessageText", "")
			if contactMessage.Params.Get("text") != e.bot.t(lang, "bot_contacts_title")+"\n\n"+contacts || contactMessage.Params.Get("parse_mode") != "" || !buyerHasAction(t, calls, "info:welcome") {
				t.Fatal("contacts text or back action lost", calls)
			}
			back := requireCall(t, e.cb(user, "info:welcome", lang), "editMessageText", "")
			if back.Params.Get("text") != e.bot.t(lang, "bot_start_welcome") || back.markup() != message.markup() {
				t.Fatal("back did not restore welcome", back)
			}
			if len(e.cb(user, "cart:add:1", lang)) != 1 {
				t.Fatal("information enabled buyer commands")
			}
		}
	}
	if e.qInt(`SELECT COUNT(*) FROM orders`) != 0 || e.qInt(`SELECT COUNT(*) FROM cart_items`) != 0 {
		t.Fatal("information altered purchases")
	}
	// A forged group callback cannot edit someone else's message or expose a web_app button there.
	group := e.do(tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{ID: "group-info", Data: "info:contacts", From: &tgbotapi.User{ID: 1915}, Message: &tgbotapi.Message{MessageID: 1, Chat: &tgbotapi.Chat{ID: -201, Type: "group"}}}})
	if len(group) != 1 || group[0].Method != "answerCallbackQuery" {
		t.Fatal("group callback edited information", group)
	}
}

func TestAbsentOfferDataDoesNotPublishInventedInformation(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true; c.WebAppURL = "https://shop.example/app/" })
	calls := e.cb(1916, "info:contacts", "ru")
	if len(calls) != 1 || calls[0].Method != "answerCallbackQuery" {
		t.Fatal("missing contacts replaced with a placeholder", calls)
	}
}
