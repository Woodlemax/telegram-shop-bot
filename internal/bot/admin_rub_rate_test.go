package bot

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"shop_bot/internal/config"
	"shop_bot/internal/storage"
	"shop_bot/internal/webapi"
	"strings"
	"testing"
	"time"
)

func rateAPIRequest(t *testing.T, handler http.Handler, userID int64, method, path, body string) map[string]any {
	t.Helper()
	user := fmt.Sprintf(`{"id":%d,"first_name":"Test","language_code":"ru"}`, userID)
	stamp := fmt.Sprint(time.Now().Unix())
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(e2eBotToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte("auth_date=" + stamp + "\nuser=" + user))
	values := url.Values{"auth_date": {stamp}, "user": {user}, "hash": {hex.EncodeToString(mac.Sum(nil))}}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "tma "+values.Encode())
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("%s %s: %d %s", method, path, res.Code, res.Body.String())
	}
	var data map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	return data
}
func TestAdminRUBRateBotMiniAppAndOldOrder(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.StarsOnlyPayments = true; c.USDToRUBRate = 100 })
	ctx := context.Background()
	buyer := int64(1701)
	e.cmd(buyer, "/start", "ru")
	if _, err := e.db.Conn().Exec(`UPDATE products SET price_rub=100 WHERE id=?`, e.prodReg); err != nil {
		t.Fatal(err)
	}
	first := e.placeOrder(buyer, e.prodReg, "")
	if e.qInt(`SELECT total_stars FROM orders WHERE id=?`, first) != 50 {
		t.Fatal("initial Stars")
	}
	e.cb(buyer, fmt.Sprintf("cart:add:%d", e.prodReg), "ru")
	api := webapi.New(webapi.Deps{Auth: webapi.NewAuthenticator(e2eBotToken, time.Hour), Catalog: e.bot.catalog, Cart: e.bot.cart, Orders: e.bot.order, Users: e.bot.users, Promos: e.bot.promos, I18n: e.bot.i18n, Reviews: e.bot.reviews, Photos: e.bot.photos, Tg: e.bot.API(), Exchange: e.bot.ExchangeService(), StarsOnlyPayments: true}, nil).Handler()
	if data := rateAPIRequest(t, api, buyer, "GET", "/api/cart", ""); data["total_stars"] != float64(50) {
		t.Fatal(data)
	}
	admin := e.cmd(e2eAdminID, "/admin", "ru")
	if !buyerHasAction(t, admin, "admin:rubrate") {
		t.Fatal("rate not in admin panel")
	}
	e.cb(e2eAdminID, "admin:rubrate", "ru")
	calls := e.cb(e2eAdminID, "admin:rubrate:edit", "ru")
	if !buyerHasAction(t, calls, "admin:rubrate") {
		t.Fatal("cancel button missing")
	}
	e.text(e2eAdminID, "200", "ru")
	if e.bot.exchange.GetUSDToRUBRate() != 200 {
		t.Fatal("admin change not applied")
	}
	if value := e.qInt(`SELECT rub_per_usd FROM shop_exchange_rate WHERE id=1`); value != 200 {
		t.Fatal("not persisted")
	}
	view, err := e.bot.cart.Get(ctx, buyer)
	if err != nil || view.TotalRUB != 100 || view.TotalStars != 25 || view.TotalUSD != 0.5 {
		t.Fatalf("bot cart: %+v %v", view, err)
	}
	data := rateAPIRequest(t, api, buyer, "GET", "/api/cart", "")
	if data["total_rub"] != float64(100) || data["total_stars"] != float64(25) || data["total_usd"] != 0.5 {
		t.Fatal("API/cart diverged", data)
	}
	card := rateAPIRequest(t, api, buyer, "GET", fmt.Sprintf("/api/products/%d", e.prodReg), "")["product"].(map[string]any)
	if card["price_rub"] != float64(100) || card["price_stars"] != float64(25) {
		t.Fatal(card)
	}
	product, err := e.bot.catalog.GetProduct(ctx, e.prodReg)
	if err != nil || product.PriceStars != 25 || *product.PriceRUB != 100 {
		t.Fatalf("bot card: %+v %v", product, err)
	}
	data = rateAPIRequest(t, api, buyer, "POST", "/api/checkout", `{"method":"stars"}`)
	next := int64(data["order_id"].(float64))
	if next == first || e.qInt(`SELECT total_stars FROM orders WHERE id=?`, next) != 25 {
		t.Fatal("new invoice rate not used")
	}
	resumed := e.cb(buyer, fmt.Sprintf("order:resume:%d", first), "ru")
	if !buyerHasAction(t, resumed, fmt.Sprintf("pay:stars:%d", first)) {
		t.Fatal("old order cannot resume")
	}
	e.payWithStars(buyer, first, "rate-old-charge")
	if e.qInt(`SELECT total_stars FROM orders WHERE id=?`, first) != 50 || e.qStr(`SELECT status FROM orders WHERE id=?`, first) != "paid" {
		t.Fatal("old order rewritten or not payable")
	}
	rebuilt, err := NewWithAPI(e.bot.cfg, e.bot.API(), e.db, e.bot.metrics, storage.NewMemoryFSMStore(), nil, e.bot.logger)
	if err != nil || rebuilt.ExchangeService().GetUSDToRUBRate() != 200 {
		t.Fatalf("restart lost rate: %v", err)
	}
}
func TestAdminRUBRateValidationPermissionsAndCancellation(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.USDToRUBRate = 100 })
	e.cmd(1702, "/rubrate 200", "ru")
	e.cb(1702, "admin:rubrate:edit", "ru")
	e.text(1702, "200", "ru")
	if e.bot.exchange.GetUSDToRUBRate() != 100 || e.qInt(`SELECT COUNT(*) FROM shop_exchange_rate`) != 0 {
		t.Fatal("non-admin changed rate")
	}
	// An admin's group message cannot update the rate.
	e.do(tgbotapi.Update{Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: -200, Type: "group"}, From: &tgbotapi.User{ID: e2eAdminID}, Text: "/rubrate 200", Entities: []tgbotapi.MessageEntity{{Offset: 0, Length: 8, Type: "bot_command"}}}})
	if e.bot.exchange.GetUSDToRUBRate() != 100 {
		t.Fatal("group changed rate")
	}
	for _, text := range []string{"0", "-1", "NaN", "Inf", "1e2", "1,2.3", "1000001", "100.12345", "100 200", ""} {
		e.cmd(e2eAdminID, "/rubrate "+text, "ru")
		if e.bot.exchange.GetUSDToRUBRate() != 100 {
			t.Fatalf("invalid value changed rate: %q", text)
		}
	}
	e.cb(e2eAdminID, "admin:rubrate:edit", "ru")
	e.text(e2eAdminID, "no", "ru")
	e.text(e2eAdminID, "92,5", "ru")
	if e.bot.exchange.GetUSDToRUBRate() != 92.5 {
		t.Fatal("comma or retry failed")
	}
	e.cb(e2eAdminID, "admin:rubrate:edit", "ru")
	e.cmd(e2eAdminID, "/cancel", "ru")
	e.text(e2eAdminID, "200", "ru")
	if e.bot.exchange.GetUSDToRUBRate() != 92.5 {
		t.Fatal("cancelled input saved")
	}
	e.cb(e2eAdminID, "admin:rubrate:edit", "ru")
	e.cb(e2eAdminID, "admin:rubrate", "ru")
	e.text(e2eAdminID, "200", "ru")
	if e.bot.exchange.GetUSDToRUBRate() != 92.5 {
		t.Fatal("cancel button failed")
	}
	e.cb(e2eAdminID, "admin:rubrate:edit", "ru")
	e.bot.rubRateInput.Store(e2eAdminID, time.Now().Add(-time.Second))
	e.text(e2eAdminID, "200", "ru")
	if e.bot.exchange.GetUSDToRUBRate() != 92.5 {
		t.Fatal("expired input saved")
	}
	e.cb(e2eAdminID, "admin:rubrate:edit", "ru")
	e.cmd(e2eAdminID, "/catalog", "ru")
	e.text(e2eAdminID, "200", "ru")
	if e.bot.exchange.GetUSDToRUBRate() != 92.5 {
		t.Fatal("other command did not end dialog")
	}
	// Beginning rate input cancels a pending archive upload.
	e.cmd(e2eAdminID, fmt.Sprintf("/setarchive %d", e.prodReg), "ru")
	e.cb(e2eAdminID, "admin:rubrate:edit", "ru")
	e.text(e2eAdminID, "100", "ru")
	if e.bot.exchange.GetUSDToRUBRate() != 100 {
		t.Fatal("archive dialog intercepted number")
	}
	if _, err := e.db.Conn().Exec(`DROP TABLE shop_exchange_rate`); err != nil {
		t.Fatal(err)
	}
	calls := e.cmd(e2eAdminID, "/rubrate 200", "ru")
	if e.bot.exchange.GetUSDToRUBRate() != 100 || !strings.Contains(fmt.Sprint(calls), e.bot.t("ru", "admin_rub_rate_failed")) {
		t.Fatal("failed persistence changed rate or no error")
	}
}
