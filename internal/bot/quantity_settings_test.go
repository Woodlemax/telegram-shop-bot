package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestAdminQuantitySettingsAndCart(t *testing.T) {
	e := newE2EEnv(t)
	const buyer = int64(9510)
	e.cmd(buyer, "/start", "ru")
	id := e.prodReg
	e.cb(buyer, fmt.Sprintf("admin:infinitestock:%d", id), "ru")
	if e.qInt("SELECT infinite_stock FROM products WHERE id=?", id) != 0 {
		t.Fatal("non-admin changed inventory")
	}
	calls := e.cb(e2eAdminID, fmt.Sprintf("admin:infinitestock:%d", id), "ru")
	if strings.Contains(tgText(calls), "%!") || !strings.Contains(tgText(calls), "Остаток: ∞") {
		t.Fatalf("invalid admin stock display: %s", tgText(calls))
	}
	if !tgHasCall(calls, "Выключить бесконечный запас") {
		t.Fatal("admin toggle button missing")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d stock 0", id), "ru")
	for i := 0; i < 3; i++ {
		e.cb(buyer, fmt.Sprintf("cart:add:%d", id), "ru")
	}
	if e.qInt("SELECT quantity FROM cart_items WHERE user_id=? AND product_id=?", buyer, id) != 3 {
		t.Fatal("unlimited item cannot be added at stock zero")
	}
	e.cb(e2eAdminID, fmt.Sprintf("admin:singleincart:%d", id), "ru")
	if e.qInt("SELECT quantity FROM cart_items WHERE user_id=? AND product_id=?", buyer, id) != 1 {
		t.Fatal("existing cart not clamped")
	}
	e.cb(buyer, fmt.Sprintf("cart:add:%d", id), "ru")
	e.cb(buyer, fmt.Sprintf("productqty:plus:%d", id), "ru")
	if e.qInt("SELECT quantity FROM cart_items WHERE user_id=? AND product_id=?", buyer, id) != 1 {
		t.Fatal("single item duplicated")
	}
	p, err := e.bot.products.GetProduct(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range e.bot.productKeyboard(p, false, 1, "ru") {
		for _, btn := range row {
			if strings.HasPrefix(btn.CallbackData, "productqty:plus:") {
				t.Fatal("quantity increase still visible")
			}
		}
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d singleincart false", id), "ru")
	e.cb(buyer, fmt.Sprintf("cart:plus:%d", id), "ru")
	if e.qInt("SELECT quantity FROM cart_items WHERE user_id=? AND product_id=?", buyer, id) != 2 {
		t.Fatal("multiple items not restored")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d infinitestock false", id), "ru")
	e.cb(buyer, fmt.Sprintf("cart:plus:%d", id), "ru")
	if e.qInt("SELECT quantity FROM cart_items WHERE user_id=? AND product_id=?", buyer, id) != 2 {
		t.Fatal("finite zero stock bypassed")
	}
}
