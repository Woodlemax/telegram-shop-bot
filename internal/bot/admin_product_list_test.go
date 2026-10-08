package bot

import (
	"context"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/config"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestListProductAdminPaginationAndVisibility(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	ctx := context.Background()
	if _, err := e.db.Conn().Exec(`UPDATE products SET is_active=0,stock=0 WHERE id=?`, e.prodReg); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Conn().Exec(`UPDATE categories SET is_active=0 WHERE id=?`, e.catID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 23; i++ {
		if _, err := e.db.Conn().Exec(`INSERT INTO products(category_id,name,price_usd,stock,is_active) VALUES(?,?,0,0,1)`, e.catID, fmt.Sprintf("Model %02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	list := requireCall(t, e.cmd(e2eAdminID, "/listproduct", "ru"), "sendMessage", "страница 1 / 2").Params.Get("text")
	if !strings.Contains(list, fmt.Sprintf("%d: Tee (неактивен)", e.prodReg)) || !strings.Contains(list, fmt.Sprintf("%d: Club", e.prodSub)) || !strings.Contains(list, "Товары: 25") || strings.Contains(list, "Model 18") {
		t.Fatal("list omitted hidden/sold-out inventory or exceeded page", list)
	}
	second := requireCall(t, e.cmd(e2eAdminID, "/listproduct 2", "ru"), "sendMessage", "страница 2 / 2").Params.Get("text")
	if !strings.Contains(second, "Model 18") || !strings.Contains(second, "Model 22") || strings.Contains(second, "Model 17") {
		t.Fatal("pagination lost/duplicated items", second)
	}
	requireCall(t, e.cmd(e2eAdminID, "/listproduct 3", "ru"), "sendMessage", e.bot.t("ru", "admin_product_list_page_missing"))
	for _, arg := range []string{"0", "-1", "oops", "1 2", "9223372036854775807"} {
		requireCall(t, e.cmd(e2eAdminID, "/listproduct "+arg, "ru"), "sendMessage", e.bot.t("ru", "admin_product_list_usage"))
	}
	if e.qInt(`SELECT COUNT(*) FROM products`) != 25 || e.qInt(`SELECT COUNT(*) FROM orders`) != 0 {
		t.Fatal("read-only command mutated inventory/orders")
	}
	// Direct DB reads bypass the buyer catalog's cache/availability filters.
	products, total, err := e.bot.adminProducts.ListProductsAdmin(ctx, 20, 20)
	if err != nil || total != 25 || len(products) != 5 {
		t.Fatal("inventory paging", products, total, err)
	}
}
func TestListProductPrivacyAndEmpty(t *testing.T) {
	for _, adminOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(adminOnly), func(t *testing.T) {
			e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = adminOnly })
			for _, call := range e.cmd(19017, "/listproduct", "ru") {
				if strings.Contains(call.Params.Get("text"), "Tee") || strings.Contains(call.Params.Get("text"), "Club") {
					t.Fatal("buyer received admin inventory")
				}
			}
			before := e.tg.count()
			e.bot.handleListProduct(context.Background(), &tgbotapi.Message{From: &tgbotapi.User{ID: e2eAdminID, LanguageCode: "ru"}, Chat: &tgbotapi.Chat{ID: -123}, Text: "/listproduct"})
			if len(e.tg.since(before)) != 0 {
				t.Fatal("group received admin inventory")
			}
			if _, err := e.db.Conn().Exec(`DELETE FROM products`); err != nil {
				t.Fatal(err)
			}
			requireCall(t, e.cmd(e2eAdminID, "/listproduct", "ru"), "sendMessage", e.bot.t("ru", "admin_product_list_empty"))
		})
	}
}
func TestListProductLongNamesAndStorageFailure(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	for i := 0; i < 18; i++ {
		if _, err := e.db.Conn().Exec(`INSERT INTO products(category_id,name,price_usd,stock,is_active) VALUES(?,?,0,0,0)`, e.catID, strings.Repeat("🚀", 200)+"\nextra"); err != nil {
			t.Fatal(err)
		}
	}
	text := requireCall(t, e.cmd(e2eAdminID, "/listproduct", "ru"), "sendMessage", "страница").Params.Get("text")
	if len(utf16.Encode([]rune(text))) > 4096 || strings.Contains(text, "extra") || !strings.Contains(text, "…") {
		t.Fatal("oversized/unsafe list", len(text))
	}
	before := e.tg.count()
	e.db.Close()
	e.bot.handleListProduct(context.Background(), &tgbotapi.Message{From: &tgbotapi.User{ID: e2eAdminID, LanguageCode: "ru"}, Chat: &tgbotapi.Chat{ID: e2eAdminID}, Text: "/listproduct"})
	requireCall(t, e.tg.since(before), "sendMessage", e.bot.t("ru", "error_short"))
}
