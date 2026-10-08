package bot

import (
	"context"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/config"
	"strings"
	"testing"
	"time"
)

func TestPromoAdminLifecycle(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	e.cmd(1917, "/addpromo BUYER25 25", "ru")
	if e.qInt(`SELECT COUNT(*) FROM promo_codes WHERE code='BUYER25'`) != 0 {
		t.Fatal("buyer created promo")
	}
	e.cmd(e2eAdminID, "/addpromo test25 25", "ru")
	if e.qInt(`SELECT discount FROM promo_codes WHERE code='TEST25'`) != 25 {
		t.Fatal("simple creation failed")
	}
	e.cmd(e2eAdminID, "/listpromos", "ru")
	id := e.qInt(`SELECT id FROM promo_codes WHERE code='TEST25'`)
	e.cmd(e2eAdminID, fmt.Sprintf("/deletepromo %d", id), "ru")
	if e.qInt(`SELECT is_active FROM promo_codes WHERE code='TEST25'`) != 0 {
		t.Fatal("deactivation failed")
	}
}
func TestPromoAdminOptionalFields(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	e.cmd(e2eAdminID, fmt.Sprintf("/addpromo LIMITED10 10 3 7 %d", e.catID), "ru")
	var max int
	var expires, category any
	if err := e.db.Conn().QueryRow(`SELECT max_uses,expires_at,category_id FROM promo_codes WHERE code='LIMITED10'`).Scan(&max, &expires, &category); err != nil {
		t.Fatal(err)
	}
	if max != 3 || expires == nil || category == nil {
		t.Fatalf("options ignored: max_uses=%d expires=%v category=%v", max, expires, category)
	}
}
func TestPromoAdminInvalidDiscount(t *testing.T) {
	for _, value := range []string{"-1", "0", "101", "abc", "10.5"} {
		t.Run(value, func(t *testing.T) {
			e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
			e.cmd(e2eAdminID, "/addpromo INVALID "+value, "ru")
			if e.qInt(`SELECT COUNT(*) FROM promo_codes WHERE code='INVALID'`) != 0 {
				t.Fatal("invalid discount accepted")
			}
		})
	}
}
func TestPromoAdminDuplicateDoesNotClaimSuccess(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	calls := e.cmd(e2eAdminID, "/addpromo save10 50", "ru")
	requireCall(t, calls, "sendMessage", e.bot.t("ru", "admin_promo_exists"))
	if e.qInt(`SELECT discount FROM promo_codes WHERE code='SAVE10'`) != 10 {
		t.Fatal("duplicate overwrote discount")
	}
	for _, call := range calls {
		if call.Params.Get("text") == e.bot.t("ru", "admin_promo_created") {
			t.Fatal("duplicate insertion failed but bot claimed promo created")
		}
	}
}

func TestPromoAdminInvalidOptionsAndPrivateAccess(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	for _, args := range []string{"", "CODE", "CODE 10 -1", "CODE 10 abc", "CODE 10 1 -2", "CODE 10 1 abc", "CODE 10 1 36501", "CODE 10 1 7 0", "CODE 10 1 7 99999", "CODE 10 1 7 1 extra", "BAD:CODE 10", strings.Repeat("A", 33) + " 10"} {
		e.cmd(e2eAdminID, "/addpromo "+args, "ru")
	}
	if e.qInt(`SELECT COUNT(*) FROM promo_codes`) != 1 {
		t.Fatal("invalid options created a promo")
	}
	group := &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: -1}, From: &tgbotapi.User{ID: e2eAdminID, LanguageCode: "ru"}, Text: "/addpromo GROUP 10", Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Length: 9}}}
	e.bot.handleAddPromo(context.Background(), group)
	if e.qInt(`SELECT COUNT(*) FROM promo_codes`) != 1 {
		t.Fatal("group admin created promo")
	}
}
func TestPromoAdminShowsSavedLimitsAndExpiry(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	e.cmd(e2eAdminID, fmt.Sprintf("/addpromo FULL10 10 3 7 %d", e.catID), "ru")
	promo, err := e.bot.promos.GetPromoByCode(context.Background(), "FULL10")
	if err != nil {
		t.Fatal(err)
	}
	if promo.ExpiresAt == nil || time.Until(*promo.ExpiresAt) < 6*24*time.Hour || time.Until(*promo.ExpiresAt) > 8*24*time.Hour || promo.CategoryID == nil || *promo.CategoryID != e.catID {
		t.Fatal("expiry or category differs from command")
	}
	message := requireCall(t, e.cmd(e2eAdminID, "/listpromos", "ru"), "sendMessage", "FULL10").Params.Get("text")
	if !strings.Contains(message, "0 / 3") || !strings.Contains(message, "UTC") {
		t.Fatal("listing omitted saved settings", message)
	}
	e.cmd(e2eAdminID, "/addpromo FOREVER 100 0 0", "ru")
	forever, err := e.bot.promos.GetPromoByCode(context.Background(), "FOREVER")
	if err != nil || forever.ExpiresAt != nil || forever.MaxUses != 0 {
		t.Fatal("unlimited defaults", err)
	}
	requireCall(t, e.cmd(e2eAdminID, "/deletepromo not-an-id", "ru"), "sendMessage", e.bot.t("ru", "admin_promo_delete_usage"))
	requireCall(t, e.cmd(e2eAdminID, "/deletepromo 9999", "ru"), "sendMessage", e.bot.t("ru", "admin_promo_missing"))
}
func TestPromoAdminStorageFailureDoesNotClaimSuccess(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	before := e.tg.count()
	e.db.Close()
	msg := &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: e2eAdminID}, From: &tgbotapi.User{ID: e2eAdminID, LanguageCode: "ru"}, Text: "/addpromo FAILURE 10", Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Length: 9}}}
	e.bot.handleAddPromo(context.Background(), msg)
	requireCall(t, e.tg.since(before), "sendMessage", e.bot.t("ru", "error_short"))
}

func TestPromoAdminProductSelector(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	var a, b int64
	if err := e.db.Conn().QueryRow(`SELECT MIN(id),MAX(id) FROM products`).Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		code, options string
		ids           int
	}{
		{"ONE10", fmt.Sprintf("products=%d", a), 1},
		{"TWO20", fmt.Sprintf("3 7 products=%d,%d", a, b), 2},
	} {
		requireCall(t, e.cmd(e2eAdminID, "/addpromo "+tc.code+" 10 "+tc.options, "ru"), "sendMessage", e.bot.t("ru", "admin_promo_created"))
		p, err := e.bot.promos.GetPromoByCode(context.Background(), tc.code)
		if err != nil || len(p.ProductIDs) != tc.ids || p.CategoryID != nil {
			t.Fatal("command scope lost", p, err)
		}
		if tc.ids == 2 && (p.MaxUses != 3 || p.ExpiresAt == nil) {
			t.Fatal("positional limits lost")
		}
	}
	text := requireCall(t, e.cmd(e2eAdminID, "/listpromos", "ru"), "sendMessage", "TWO20").Params.Get("text")
	if !strings.Contains(text, fmt.Sprintf("товары (ID): %d, %d", a, b)) {
		t.Fatal("scope not shown", text)
	}
	before := e.qInt(`SELECT COUNT(*) FROM promo_codes`)
	for _, args := range []string{"products=", "products=0", "products=-1", "products=abc", "products=1,", "products=1,1", "products=999999", fmt.Sprintf("products=%d products=%d", a, b), fmt.Sprintf("0 0 %d products=%d", e.catID, a), "items=1"} {
		e.cmd(e2eAdminID, "/addpromo BAD10 10 "+args, "ru")
	}
	if e.qInt(`SELECT COUNT(*) FROM promo_codes`) != before {
		t.Fatal("invalid product filter created promo")
	}
}
