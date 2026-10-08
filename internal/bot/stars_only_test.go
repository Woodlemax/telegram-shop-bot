package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"shop_bot/internal/config"
)

func TestBotStarsOnlyPricesCheckoutResumeAndStaleButtons(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.StarsOnlyPayments = true; c.USDToRUBRate = 100 })
	ctx := context.Background()
	buyer := int64(1201)
	e.cmd(buyer, "/start", "ru")
	grantBalance(e, buyer, "25.00", "test")
	product, err := e.bot.catalog.GetProduct(ctx, e.prodReg)
	if err != nil {
		t.Fatal(err)
	}
	text := e.bot.formatProductText("ru", product)
	if strings.Contains(text, "$") || !strings.Contains(text, "1000.00") || !strings.Contains(text, "500") || !strings.Contains(text, "₽") {
		t.Fatalf("legacy USD display: %s", text)
	}
	if _, err := e.db.Conn().Exec(`UPDATE products SET price_rub=100 WHERE id=?`, e.prodReg); err != nil {
		t.Fatal(err)
	}
	e.cb(buyer, fmt.Sprintf("cart:add:%d", e.prodReg), "ru")
	view, err := e.bot.cart.Get(ctx, buyer)
	if err != nil {
		t.Fatal(err)
	}
	if view.TotalRUB != 100 || view.TotalUSD != 1 || view.TotalStars != 50 {
		t.Fatalf("bot totals: %+v", view)
	}
	if text := e.bot.formatCartText("ru", view); strings.Contains(text, "$") || !strings.Contains(text, "100.00") || !strings.Contains(text, "50") {
		t.Fatalf("cart display: %s", text)
	}
	calls := e.cb(buyer, "order:confirm", "ru")
	id := e.qInt(`SELECT MAX(id) FROM orders WHERE user_id=?`, buyer)
	for _, method := range []string{"crypto", "yookassa", "stripe", "ton", "nowpayments", "balance"} {
		if buyerHasAction(t, calls, fmt.Sprintf("pay:%s:%d", method, id)) {
			t.Fatalf("checkout offers %s", method)
		}
	}
	calls = e.cb(buyer, fmt.Sprintf("order:resume:%d", id), "ru")
	if !buyerHasAction(t, calls, fmt.Sprintf("pay:stars:%d", id)) {
		t.Fatal("Stars missing")
	}
	for _, method := range []string{"crypto", "yookassa", "stripe", "ton", "nowpayments", "balance"} {
		if buyerHasAction(t, calls, fmt.Sprintf("pay:%s:%d", method, id)) {
			t.Fatalf("resume offers %s", method)
		}
		rejected := e.cb(buyer, fmt.Sprintf("pay:%s:%d", method, id), "ru")
		for _, call := range rejected {
			if call.Method != "answerCallbackQuery" {
				t.Fatalf("disabled payment created output: %s", call.Method)
			}
		}
	}
	if e.qStr(`SELECT printf('%.2f',balance_usd) FROM users WHERE telegram_id=?`, buyer) != "25.00" || e.qStr(`SELECT status FROM orders WHERE id=?`, id) != "pending" {
		t.Fatal("old payment button mutated money/order")
	}
	e.payWithStars(buyer, id, "stars-only-charge")
	if e.qStr(`SELECT payment_method FROM orders WHERE id=?`, id) != "stars" {
		t.Fatal("Stars payment failed")
	}
	// A zero-total order can still be fulfilled without creating an invoice.
	uploadDigital(e, "stars-only-free-file")
	if _, err := e.db.Conn().Exec(`UPDATE products SET open_price=1,price_rub=0,price_usd=0,price_stars=0 WHERE id=?`, e.prodReg); err != nil {
		t.Fatal(err)
	}
	free := e.placeOrder(buyer, e.prodReg, "")
	if e.qStr(`SELECT payment_method FROM orders WHERE id=?`, free) != "free" {
		t.Fatal("free delivery blocked")
	}
	var stock int
	if err := e.db.Conn().QueryRow(`SELECT stock FROM products WHERE id=?`, e.prodReg).Scan(&stock); err != nil || stock != 4 {
		t.Fatalf("unexpected stock: %d %v", stock, err)
	}
}
