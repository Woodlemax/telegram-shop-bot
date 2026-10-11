package bot

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"shop_bot/internal/config"
	"shop_bot/internal/shop"
	"shop_bot/internal/storage"
)

func TestPaymentsWithoutPurchaseRewardsKeepDiscountsAndArchives(t *testing.T) {
	for _, provider := range []string{storage.PaymentMethodStars, storage.PaymentMethodYooKassa, storage.PaymentMethodBalance} {
		t.Run(provider, func(t *testing.T) {
			e := newE2EEnvWithConfig(t, func(c *config.Config) { c.PurchaseRewardsEnabled = false })
			ctx := context.Background()
			const buyer int64 = 781
			const referrer int64 = 782
			uploadDigital(e, "no-rewards-archive")
			e.cmd(buyer, "/start", "ru")
			e.cmd(referrer, "/start", "ru")
			if _, err := e.db.Conn().Exec(`UPDATE users SET loyalty_pts=123,loyalty_level='silver',balance_usd=25,referred_by=(SELECT id FROM users WHERE telegram_id=?) WHERE telegram_id=?`, referrer, buyer); err != nil {
				t.Fatal(err)
			}
			if _, err := e.db.Conn().Exec(`UPDATE users SET loyalty_pts=456 WHERE telegram_id=?`, referrer); err != nil {
				t.Fatal(err)
			}
			orderID := e.placeOrder(buyer, e.prodReg, "SAVE10")
			if _, err := e.db.Conn().Exec(`UPDATE orders SET total_rub=900 WHERE id=?`, orderID); err != nil {
				t.Fatal(err)
			}
			order, err := e.bot.order.GetOrder(ctx, orderID)
			if err != nil {
				t.Fatal(err)
			}
			if order.TotalUSD != 9 || order.DiscountPct != 10 {
				t.Fatal("ordinary promo discount lost", order.TotalUSD, order.DiscountPct)
			}
			var outcome *shop.PaymentOutcome
			switch provider {
			case storage.PaymentMethodBalance:
				outcome, err = e.bot.order.ConfirmBalancePayment(ctx, orderID, buyer)
			default:
				receipt := shop.PaymentReceipt{OrderID: orderID, Provider: provider, ExternalID: "no-rewards-" + provider, PayerID: buyer, Currency: "XTR", AmountMinor: int64(order.TotalStars)}
				if provider == storage.PaymentMethodYooKassa {
					receipt.PayerID = 0
					receipt.Currency = "RUB"
					receipt.AmountMinor = int64(math.Round(order.TotalRUB * 100))
					receipt.Scale = 2
				}
				outcome, err = e.bot.order.ConfirmPaymentReceipt(ctx, receipt)
			}
			if err != nil {
				t.Fatal(err)
			}
			if outcome.PointsAwarded != 0 || outcome.NewLevel != "" || outcome.NewUserPromo != "" || outcome.ReferralReferrer != 0 {
				t.Fatal("purchase rewards applied", outcome)
			}
			before := e.tg.count()
			e.bot.AnnouncePaidOutcome(ctx, outcome, provider)
			if !findMessage(e.tg.since(before), buyer, fmt.Sprintf(e.bot.t("ru", "payment_success"), orderID)) {
				t.Fatal("payment success message lost")
			}
			// Even a stale worker outcome must not produce bonus notifications.
			before = e.tg.count()
			e.bot.NotifyPaymentOutcome(ctx, &shop.PaymentOutcome{Order: order, PointsAwarded: 10, NewLevel: "gold", NewUserPromo: "OLD-BONUS", ReferralReferrer: referrer, ReferrerPoints: 100})
			if e.tg.count() != before {
				t.Fatal("bonus notification sent")
			}
			if e.qInt(`SELECT count(*) FROM loyalty_txs`) != 0 || e.qInt(`SELECT count(*) FROM referral_awards`) != 0 || e.qInt(`SELECT count(*) FROM promo_codes WHERE code LIKE 'REF-%'`) != 0 {
				t.Fatal("bonus or automatic promo persisted")
			}
			if e.qInt(`SELECT loyalty_pts FROM users WHERE telegram_id=?`, buyer) != 123 || e.qInt(`SELECT loyalty_pts FROM users WHERE telegram_id=?`, referrer) != 456 || e.qStr(`SELECT loyalty_level FROM users WHERE telegram_id=?`, buyer) != "silver" {
				t.Fatal("existing points changed")
			}
			balance := e.qStr(`SELECT printf('%.2f',balance_usd) FROM users WHERE telegram_id=?`, buyer)
			wantBalance := "25.00"
			if provider == storage.PaymentMethodBalance {
				wantBalance = "16.00"
			}
			if balance != wantBalance {
				t.Fatal("wrong balance", balance)
			}
			before = e.tg.count()
			e.bot.ProcessDigitalDeliveries(ctx)
			docs := documentCalls(e.tg.since(before))
			if len(docs) != 1 || docs[0].Params.Get("document") != "no-rewards-archive" || docs[0].Params.Get("chat_id") != strconv.FormatInt(buyer, 10) {
				t.Fatal("archive delivery lost", docs)
			}
			if e.qStr(`SELECT status FROM orders WHERE id=?`, orderID) != "delivered" {
				t.Fatal("order not delivered")
			}
		})
	}
}

func TestDisabledPurchaseRewardsHideBonusScreens(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.PurchaseRewardsEnabled = false })
	const buyer int64 = 791
	for _, data := range flattenData(e.bot.mainMenuKeyboard("ru", "Cart")) {
		if data == "ref:open" {
			t.Fatal("bonus menu shown")
		}
	}
	text := e.bot.formatProfileText("ru", &storage.User{FirstName: "Buyer", LoyaltyPts: 123, LoyaltyLevel: "gold", ReferralCode: sql.NullString{String: "OLD", Valid: true}}, 1)
	for _, word := range []string{"Уровень", "баллов", "Реферальный", "pts"} {
		if strings.Contains(text, word) {
			t.Fatal("bonus profile shown", text)
		}
	}
	e.cmd(buyer, "/start", "ru")
	before := e.qInt(`SELECT count(*) FROM users WHERE referral_code IS NOT NULL`)
	calls := e.cmd(buyer, "/referral", "ru")
	if strings.Contains(tgText(calls), "Реферальная программа") || e.qInt(`SELECT count(*) FROM users WHERE referral_code IS NOT NULL`) != before {
		t.Fatal("disabled referral opened or generated code")
	}
}
