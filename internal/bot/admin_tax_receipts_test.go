package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"shop_bot/internal/config"
	"shop_bot/internal/storage"
)

const testTaxReceiptURL = "https://lknpd.nalog.ru/api/v1/receipt/123456789012/example/print"

func TestAdminTaxReceiptDeliveryAndDeduplication(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	e.cmd(777, "/start", "ru")
	id := seedOrderCardOrder(t, e, 777, 100, 0)
	markOrderPaid(t, e, id, storage.PaymentMethodYooKassa, "yoo-receipt-1")
	command := fmt.Sprintf("/receipt %d %s", id, testTaxReceiptURL)
	calls := e.cmd(e2eAdminID, command, "ru")
	if len(calls) != 2 || calls[0].Params.Get("chat_id") != "777" || !strings.Contains(calls[0].markup(), testTaxReceiptURL) {
		t.Fatalf("wrong recipient/button: %+v", calls)
	}
	receipt, err := e.bot.taxReceipts.Get(context.Background(), id)
	if err != nil || receipt == nil || receipt.PaymentID != "yoo-receipt-1" || receipt.UserID != 777 || receipt.DeliveryStatus != "sent" || !receipt.SentAt.Valid || receipt.MessageID <= 0 {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	calls = e.cmd(e2eAdminID, command, "ru")
	if len(calls) != 1 || !strings.Contains(tgText(calls), e.bot.t("ru", "admin_receipt_already_sent")) {
		t.Fatalf("duplicate: %+v", calls)
	}
	card := tgText(e.cmd(e2eAdminID, fmt.Sprintf("/order %d", id), "ru"))
	for _, want := range []string{testTaxReceiptURL, "yoo-receipt-1", "777", "100.00", "отправлен"} {
		if !strings.Contains(card, want) {
			t.Fatalf("missing %s: %s", want, card)
		}
	}
}
func TestAdminTaxReceiptFailedDeliveryRemainsAvailableAndRetries(t *testing.T) {
	e := newE2EEnv(t)
	e.cmd(777, "/start", "ru")
	id := seedOrderCardOrder(t, e, 777, 100, 0)
	markOrderPaid(t, e, id, storage.PaymentMethodYooKassa, "yoo-fail")
	e.tg.mu.Lock()
	e.tg.failSendChatID = 777
	e.tg.mu.Unlock()
	command := fmt.Sprintf("/receipt %d %s", id, testTaxReceiptURL)
	calls := e.cmd(e2eAdminID, command, "ru")
	if !strings.Contains(tgText(calls), "отправка в Telegram не подтверждена") {
		t.Fatalf("failed send: %+v", calls)
	}
	order, err := e.bot.order.GetOrder(context.Background(), id)
	if err != nil || order.TaxReceipt == nil || order.TaxReceipt.DeliveryStatus != "failed" || order.TaxReceipt.URL != testTaxReceiptURL {
		t.Fatalf("lost receipt: %+v %v", order, err)
	}
	e.tg.mu.Lock()
	e.tg.failSendChatID = 0
	e.tg.mu.Unlock()
	calls = e.cmd(e2eAdminID, command, "ru")
	if len(calls) != 2 || calls[0].Params.Get("chat_id") != "777" {
		t.Fatalf("retry: %+v", calls)
	}
	receipt, _ := e.bot.taxReceipts.Get(context.Background(), id)
	if receipt.DeliveryStatus != "sent" {
		t.Fatalf("retry status: %+v", receipt)
	}
}
func TestAdminTaxReceiptRejectsUnauthorizedUnpaidAndWrongLinks(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	id := seedOrderCardOrder(t, e, 777, 100, 0)
	e.cmd(777, fmt.Sprintf("/receipt %d %s", id, testTaxReceiptURL), "ru")
	e.cmd(e2eAdminID, fmt.Sprintf("/receipt %d %s", id, testTaxReceiptURL), "ru")
	receipt, _ := e.bot.taxReceipts.Get(context.Background(), id)
	if receipt != nil {
		t.Fatal("unauthorized/unpaid attachment")
	}
	markOrderPaid(t, e, id, storage.PaymentMethodYooKassa, "paid-link")
	for _, link := range []string{"javascript:alert(1)", "https://example.com/receipt", "https://lknpd.nalog.ru.evil.test/api/v1/receipt/a", "https://evil@lknpd.nalog.ru/api/v1/receipt/a"} {
		calls := e.cmd(e2eAdminID, fmt.Sprintf("/receipt %d %s", id, link), "ru")
		if len(calls) != 1 || calls[0].Params.Get("chat_id") != "9001" {
			t.Fatalf("bad link delivered: %+v", calls)
		}
	}
	receipt, _ = e.bot.taxReceipts.Get(context.Background(), id)
	if receipt != nil {
		t.Fatal("invalid URL persisted")
	}
}
