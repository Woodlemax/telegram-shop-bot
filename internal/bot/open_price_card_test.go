package bot

import (
	"context"
	"fmt"
	"shop_bot/internal/config"
	"strings"
	"testing"
)

func TestOpenPriceProductCardShowsBuyersSelectedPrice(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.USDToRUBRate = 100; c.StarsOnlyPayments = true })
	id := e.prodReg
	buyer := int64(1751)
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d openprice true", id), "ru")
	e.cmd(buyer, "/start", "ru")
	if err := e.bot.cart.SetPrice(context.Background(), buyer, id, 100); err != nil {
		t.Fatal(err)
	}
	text := tgText(e.cb(buyer, fmt.Sprintf("product:%d", id), "ru"))
	if !strings.Contains(text, "100.00") || !strings.Contains(text, "50 ⭐") || strings.Contains(text, "0.00 ₽ / 0") {
		t.Fatal("buyer sees base zero price", text)
	}
	other := tgText(e.cb(1752, fmt.Sprintf("product:%d", id), "ru"))
	if strings.Contains(other, "100.00") || !strings.Contains(other, "0.00") {
		t.Fatal("selected price leaked to other buyer", other)
	}
}
