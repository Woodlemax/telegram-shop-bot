package bot

import (
	"context"
	"errors"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"math"
	"shop_bot/internal/config"
	"shop_bot/internal/storage"
	"strings"
	"testing"
)

func openPriceText(e *e2eEnv, userID int64, text string) []tgCall {
	return e.do(tgbotapi.Update{Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: userID, Type: "private"}, From: &tgbotapi.User{ID: userID, LanguageCode: "en"}, Text: text}})
}

func TestOpenPriceFreeArchiveAndUpdates(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.USDToRUBRate = 85.7116 })
	ctx := context.Background()
	const buyer = int64(9310)
	uploadDigital(e, "free-archive-v1")
	e.cb(e2eAdminID, fmt.Sprintf("admin:openprice:%d", e.prodReg), "ru")
	if e.qInt("SELECT open_price FROM products WHERE id=?", e.prodReg) != 1 || e.qInt("SELECT price_rub FROM products WHERE id=?", e.prodReg) != 0 {
		t.Fatal("admin open price did not default to 0 RUB")
	}
	e.cmd(buyer, "/start", "ru")
	calls := e.cb(buyer, fmt.Sprintf("product:%d", e.prodReg), "ru")
	if !strings.Contains(tgText(calls), "Свободная цена") {
		t.Fatal("product has no open-price hint")
	}
	e.cb(buyer, fmt.Sprintf("cart:add:%d", e.prodReg), "ru")
	calls = e.cmd(buyer, "/cart", "ru")
	if !strings.Contains(tgText(calls), "0.00") || !strings.Contains(tgText(calls), "₽") {
		t.Fatal("cart does not show 0 RUB")
	}
	e.cb(buyer, "cart:checkout", "ru")
	before := e.tg.count()
	calls = e.cb(buyer, "order:confirm", "ru")
	id := e.qInt("SELECT MAX(id) FROM orders WHERE user_id=?", buyer)
	if e.qStr("SELECT payment_method FROM orders WHERE id=?", id) != storage.PaymentMethodFree || e.qStr("SELECT status FROM orders WHERE id=?", id) != "delivered" {
		t.Fatal("free checkout not fulfilled")
	}
	if e.qInt("SELECT COUNT(*) FROM payment_attempts WHERE order_id=?", id) != 0 || e.qInt("SELECT COUNT(*) FROM free_order_grants WHERE order_id=?", id) != 1 {
		t.Fatal("free grant created a money receipt or is missing")
	}
	docs := documentCalls(e.tg.since(before))
	if len(docs) != 1 || docs[0].Params.Get("document") != "free-archive-v1" {
		t.Fatalf("free delivery: %+v", docs)
	}
	for _, call := range calls {
		if call.Method == "sendInvoice" {
			t.Fatal("free checkout called a payment provider")
		}
	}
	if err := e.bot.order.ConfirmFreeOrder(ctx, id, buyer); !errors.Is(err, storage.ErrOrderStatusConflict) {
		t.Fatalf("free replay: %v", err)
	}
	if err := e.bot.order.ConfirmFreeOrder(ctx, id, buyer+1); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("foreign free order: %v", err)
	}
	deliveryID := e.qInt("SELECT id FROM digital_deliveries WHERE order_id=?", id)
	uploadDigital(e, "free-archive-v2")
	docs = documentCalls(e.cb(buyer, fmt.Sprintf("digital:download:%d", deliveryID), "ru"))
	if len(docs) != 1 || docs[0].Params.Get("document") != "free-archive-v2" {
		t.Fatal("free purchase did not get archive update")
	}
	if _, err := e.db.Conn().Exec("UPDATE orders SET order_state='cancelled' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.bot.archives.Owned(ctx, buyer, deliveryID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("canceled free access: %v", err)
	}
}

func TestOpenPriceWholeRublesAndPaidCheckout(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.USDToRUBRate = 85.7116 })
	ctx := context.Background()
	const buyer = int64(9320)
	uploadDigital(e, "donation-archive")
	e.cb(e2eAdminID, fmt.Sprintf("admin:openprice:%d", e.prodReg), "en")
	e.cmd(buyer, "/start", "en")
	e.cb(buyer, fmt.Sprintf("price:enter:%d", e.prodReg), "en")
	for _, invalid := range []string{"-1", "1.5", "NaN", "1e2", "1000001"} {
		openPriceText(e, buyer, invalid)
		if e.qInt("SELECT COUNT(*) FROM cart_items WHERE user_id=?", buyer) != 0 {
			t.Fatalf("invalid price accepted: %s", invalid)
		}
	}
	openPriceText(e, buyer, "100")
	view, err := e.bot.cart.Get(ctx, buyer)
	if err != nil {
		t.Fatal(err)
	}
	if view.TotalRUB != 100 || !view.BaseRUB || math.Abs(view.TotalUSD-100/85.7116) > 0.000001 || view.TotalStars != 58 {
		t.Fatalf("RUB pricing: %+v", view)
	}
	if err := e.bot.cart.SetPrice(ctx, buyer, e.prodSub, 0); err == nil {
		t.Fatal("custom price accepted for a fixed-price subscription")
	}
	id := e.placeOrder(buyer, e.prodReg, "")
	if err := e.bot.order.ConfirmFreeOrder(ctx, id, buyer); !errors.Is(err, storage.ErrInvalidMoney) {
		t.Fatalf("nonzero free checkout: %v", err)
	}
	if e.qInt("SELECT COUNT(*) FROM free_order_grants WHERE order_id=?", id) != 0 {
		t.Fatal("positive order received free grant")
	}
	e.successfulPayment(buyer, fmt.Sprint(id), 58, "rub-stars-charge")
	before := e.tg.count()
	e.bot.ProcessDigitalDeliveries(ctx)
	if len(documentCalls(e.tg.since(before))) != 1 {
		t.Fatal("positive RUB purchase not delivered")
	}
	if e.qStr("SELECT printf('%.2f',total_rub) FROM orders WHERE id=?", id) != "100.00" {
		t.Fatal("chosen RUB price not frozen on order")
	}
}

func TestOpenPriceMixedCartCannotSkipPayment(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.USDToRUBRate = 85.7116 })
	ctx := context.Background()
	const buyer = int64(9330)
	uploadDigital(e, "mixed-archive")
	e.cb(e2eAdminID, fmt.Sprintf("admin:openprice:%d", e.prodReg), "en")
	e.cmd(buyer, "/start", "en")
	e.cb(buyer, fmt.Sprintf("cart:add:%d", e.prodReg), "en")
	// Another regular product has a fixed RUB price.
	rub := float64(200)
	id, err := e.bot.products.CreateProduct(ctx, &storage.Product{CategoryID: e.catID, Name: "Fixed", PriceRUB: &rub, Stock: 5, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.bot.cart.SetPrice(ctx, buyer, id, 0); err == nil {
		t.Fatal("fixed price overridden")
	}
	e.cb(buyer, fmt.Sprintf("cart:add:%d", id), "en")
	e.cb(buyer, "order:confirm", "en")
	orderID := e.qInt("SELECT MAX(id) FROM orders WHERE user_id=?", buyer)
	if e.qStr("SELECT status FROM orders WHERE id=?", orderID) != "pending" {
		t.Fatal("mixed cart skipped payment")
	}
	if err := e.bot.order.ConfirmFreeOrder(ctx, orderID, buyer); !errors.Is(err, storage.ErrInvalidMoney) {
		t.Fatalf("mixed free checkout: %v", err)
	}
}
