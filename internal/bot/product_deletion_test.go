package bot

import (
	"context"
	"fmt"
	"shop_bot/internal/config"
	"testing"
)

func TestAdminDeleteProductRemovesCartsKeepsOrders(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	ctx := context.Background()
	if err := e.bot.cart.Add(ctx, e2eAdminID, e.prodReg); err != nil {
		t.Fatal(err)
	}
	view, err := e.bot.cart.Get(ctx, e2eAdminID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.bot.order.CreateFromCart(ctx, e2eAdminID, view, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireCall(t, e.cmd(e2eAdminID, fmt.Sprintf("/deleteproduct %d", e.prodReg), "ru"), "sendMessage", e.bot.t("ru", "admin_product_deleted"))
	if e.qInt(`SELECT COUNT(*) FROM cart_items WHERE product_id=?`, e.prodReg) != 0 {
		t.Fatal("deleted item remains in cart")
	}
	if e.qInt(`SELECT COUNT(*) FROM order_items WHERE order_id=? AND product_id=?`, id, e.prodReg) != 1 {
		t.Fatal("deletion erased history")
	}
	requireCall(t, e.cmd(e2eAdminID, fmt.Sprintf("/deleteproduct %d", e.prodReg), "ru"), "sendMessage", e.bot.t("ru", "admin_product_not_found"))
	requireCall(t, e.cmd(e2eAdminID, "/deleteproduct 0", "ru"), "sendMessage", e.bot.t("ru", "admin_usage_deleteproduct"))
}
