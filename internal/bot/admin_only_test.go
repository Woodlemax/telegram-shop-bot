package bot

import (
	"context"
	"encoding/json"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/config"
	"shop_bot/internal/webapi"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAdminOnlyRoutesBuyersToMiniApp(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) {
		c.BotAdminOnly = true
		c.WebAppURL = "https://shop.example/app/"
		c.USDToRUBRate = 100
	})
	buyer := int64(1911)
	for _, user := range []int64{buyer, e2eAdminID} {
		for _, command := range []string{"/catalog", "/cart", "/orders", "/files", "/search plane", "/wishlist", "/profile", "/referral", "/mysubs"} {
			calls := e.cmd(user, command, "ru")
			message := requireCall(t, calls, "sendMessage", "")
			if message.Params.Get("text") != e.bot.t("ru", "bot_miniapp_only") || !strings.Contains(message.markup(), `"web_app":{"url":"https://shop.example/app/"}`) {
				t.Fatal("buyer route not replaced", command, calls)
			}
		}
		for _, callback := range []string{"back:catalog", "product:1", "cart:add:1", "price:enter:1", "cart:checkout", "pay:stars:1", "order:free:1", "order:cancel:1", "order:resume:1", "digital:library", "digital:download:1", "review:1:5"} {
			calls := e.cb(user, callback, "ru")
			if len(calls) != 1 || calls[0].Method != "answerCallbackQuery" || calls[0].Params.Get("text") != e.bot.t("ru", "bot_miniapp_only") {
				t.Fatal("stale buyer callback acted", callback, calls)
			}
		}
	}
	if e.qInt(`SELECT COUNT(*) FROM orders`) != 0 || e.qInt(`SELECT COUNT(*) FROM cart_items`) != 0 {
		t.Fatal("disabled actions mutated purchases")
	}
	e.text(buyer, "100", "ru")
	calls := e.cmd(buyer, "/start", "ru")
	if !strings.Contains(tgText(calls), "Mini App") {
		t.Fatal("start showed buyer menu")
	}
	e.bot.registerCommands()
	commands := requireCall(t, e.tg.since(0), "setMyCommands", "")
	if strings.Contains(commands.Params.Get("commands"), `"catalog"`) || !strings.Contains(commands.Params.Get("commands"), `"start"`) {
		t.Fatal("buyer commands still advertised")
	}
	inline := e.do(tgbotapi.Update{InlineQuery: &tgbotapi.InlineQuery{ID: "inline", From: &tgbotapi.User{ID: buyer}, Query: "plane"}})
	answer := requireCall(t, inline, "answerInlineQuery", "")
	if answer.Params.Get("results") != "[]" {
		t.Fatal("inline catalog still available", answer)
	}
}

func TestAdminOnlyPreservesPrivateAdministration(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true; c.USDToRUBRate = 100 })
	if tgText(e.cmd(e2eAdminID, "/admin", "ru")) != e.bot.t("ru", "admin_panel") {
		t.Fatal("admin command missing panel")
	}
	e.cmd(e2eAdminID, "/rubrate 200", "ru")
	if e.bot.exchange.GetUSDToRUBRate() != 200 {
		t.Fatal("rate dialog blocked")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d telegram @plane_group", e.prodReg), "ru")
	product, err := e.bot.products.GetProduct(context.Background(), e.prodReg)
	if err != nil || product.TelegramURL != "https://t.me/plane_group" {
		t.Fatal("admin product edit blocked", err)
	}
	e.cmd(e2eAdminID, "/addproduct", "ru")
	e.text(e2eAdminID, "Admin model", "ru")
	state, err := e.bot.fsm.GetAddProductState(context.Background(), e2eAdminID)
	if err != nil || state == nil || state.Name != "Admin model" {
		t.Fatal("wizard text blocked", err)
	}
	e.cmd(e2eAdminID, "/cancel", "ru")
	uploadDigital(e, "admin-only-archive")
	e.cb(1912, "admin:rubrate:edit", "ru")
	e.cmd(1912, "/rubrate 300", "ru")
	e.do(tgbotapi.Update{Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: -200, Type: "group"}, From: &tgbotapi.User{ID: e2eAdminID}, Text: "/rubrate 300", Entities: []tgbotapi.MessageEntity{{Offset: 0, Length: 8, Type: "bot_command"}}}})
	if e.bot.exchange.GetUSDToRUBRate() != 200 {
		t.Fatal("non-admin/group administration allowed")
	}
}

func TestAdminOnlyMiniAppPaymentAndArchive(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true; c.StarsOnlyPayments = true; c.USDToRUBRate = 100 })
	ctx := context.Background()
	buyer := int64(1913)
	uploadDigital(e, "miniapp-paid-archive")
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d openprice true", e.prodReg), "ru")
	api := webapi.New(webapi.Deps{Auth: webapi.NewAuthenticator(e2eBotToken, time.Hour), Catalog: e.bot.catalog, Cart: e.bot.cart, Orders: e.bot.order, Users: e.bot.users, Promos: e.bot.promos, I18n: e.bot.i18n, Reviews: e.bot.reviews, Photos: e.bot.photos, Tg: e.bot.API(), Exchange: e.bot.exchange, Archives: e.bot.archives, StarsOnlyPayments: true}, nil).Handler()
	rateAPIRequest(t, api, buyer, "POST", "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":100}`, e.prodReg))
	data := rateAPIRequest(t, api, buyer, "POST", "/api/checkout", `{"method":"stars"}`)
	id := int64(data["order_id"].(float64))
	payload := strconv.FormatInt(id, 10)
	if !strings.Contains(data["invoice_link"].(string), "test-invoice") {
		t.Fatal("Mini App invoice missing")
	}
	answer := requireCall(t, e.preCheckout(buyer, "miniapp-check", payload, 50), "answerPreCheckoutQuery", "")
	if answer.Params.Get("ok") != "true" {
		t.Fatal("payment gate rejected Mini App")
	}
	e.successfulPayment(buyer, payload, 50, "miniapp-admin-only-charge")
	e.bot.ProcessDigitalDeliveries(ctx)
	if e.qStr(`SELECT status FROM orders WHERE id=?`, id) != "delivered" {
		t.Fatal("payment not settled/delivered")
	}
	if len(documentCalls(e.tg.since(0))) != 1 {
		t.Fatal("archive worker disabled")
	}
	rateAPIRequest(t, api, buyer, "POST", fmt.Sprintf("/api/orders/%d/download", id), fmt.Sprintf(`{"product_id":%d}`, e.prodReg))
	e.bot.ProcessDigitalDeliveries(ctx)
	if len(documentCalls(e.tg.since(0))) != 2 {
		t.Fatal("repeat Mini App download disabled")
	}
	for _, call := range e.tg.since(0) {
		if strings.Contains(call.markup(), `review:`) || strings.Contains(call.markup(), `digital:download:`) {
			t.Fatal("disabled review invite sent")
		}
	}
	// Free checkout still uses the same worker when buyer bot callbacks are disabled.
	rateAPIRequest(t, api, buyer, "POST", "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":0}`, e.prodReg))
	free := rateAPIRequest(t, api, buyer, "POST", "/api/checkout", `{"method":"free"}`)
	if free["free"] != true {
		t.Fatal("free grant disabled")
	}
	e.bot.ProcessDigitalDeliveries(ctx)
	if len(documentCalls(e.tg.since(0))) != 3 {
		t.Fatal("free archive disabled")
	}
	var commands []map[string]string
	e.bot.registerCommands()
	if err := json.Unmarshal([]byte(requireCall(t, e.tg.since(0), "setMyCommands", "").Params.Get("commands")), &commands); err != nil || len(commands) != 2 {
		t.Fatal("invalid command menu", err)
	}
}

func TestMiniAppStartWelcomeForBuyerAndAdmin(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true; c.WebAppURL = "https://shop.example/app/" })
	for _, user := range []int64{1914, e2eAdminID} {
		for _, lang := range []string{"ru", "en"} {
			for _, command := range []string{"/start", "/start ref_unused"} {
				calls := e.cmd(user, command, lang)
				if len(calls) != 1 {
					t.Fatal("welcome also sent old menu/admin panel", calls)
				}
				message := requireCall(t, calls, "sendMessage", "")
				if message.Params.Get("text") != e.bot.t(lang, "bot_start_welcome") || !strings.Contains(message.Params.Get("text"), "WoodleWing") {
					t.Fatal("welcome missing", calls)
				}
				var markup struct {
					Rows [][]struct {
						Text   string `json:"text"`
						WebApp struct {
							URL string `json:"url"`
						} `json:"web_app"`
						Callback string `json:"callback_data"`
					} `json:"inline_keyboard"`
				}
				if err := json.Unmarshal([]byte(message.markup()), &markup); err != nil || len(markup.Rows) != 1 || len(markup.Rows[0]) != 1 {
					t.Fatal("launch button missing", err)
				}
				button := markup.Rows[0][0]
				if button.Text != e.bot.t(lang, "bot_open_shop") || button.WebApp.URL != e.bot.cfg.WebAppURL || button.Callback != "" {
					t.Fatal("button does not open Mini App", button)
				}
			}
		}
	}
	if tgText(e.cmd(e2eAdminID, "/admin", "ru")) != e.bot.t("ru", "admin_panel") {
		t.Fatal("admin access lost")
	}
	if tgText(e.cmd(e2eAdminID, "/help", "ru")) != e.bot.t("ru", "admin_panel") {
		t.Fatal("admin help lost")
	}
	if e.qInt(`SELECT COUNT(*) FROM orders`) != 0 || e.qInt(`SELECT COUNT(*) FROM cart_items`) != 0 {
		t.Fatal("start mutated buyer purchases")
	}
	// Telegram permits web_app buttons in private chats only.
	group := e.do(tgbotapi.Update{Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: -201, Type: "group"}, From: &tgbotapi.User{ID: 1914, LanguageCode: "ru"}, Text: "/start", Entities: []tgbotapi.MessageEntity{{Offset: 0, Length: 6, Type: "bot_command"}}}})
	if requireCall(t, group, "sendMessage", "").markup() != "" {
		t.Fatal("group received unsupported launch button")
	}
}
