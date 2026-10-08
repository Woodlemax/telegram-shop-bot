package config

import (
	"strings"
	"testing"
)

func TestShopInformationConfig(t *testing.T) {
	values := map[string]string{"BOT_TOKEN": "123:abc", "ADMIN_IDS": "42"}
	cfg, err := LoadFromMap(values)
	if err != nil || cfg.ShopOfferURL != "" || cfg.ShopContactsText != "" {
		t.Fatal("blank information changed defaults", err)
	}
	values["SHOP_OFFER_URL"] = "https://shop.example/offer"
	values["SHOP_CONTACTS_TEXT"] = `Seller\nhello@example.test`
	cfg, err = LoadFromMap(values)
	if err != nil || cfg.ShopOfferURL != values["SHOP_OFFER_URL"] || cfg.ShopContactsText != "Seller\nhello@example.test" {
		t.Fatal("offer/contacts configuration lost content", err)
	}
	for _, invalid := range []string{"javascript:alert(1)", "http://shop.example/offer", "https://", "https://user:password@shop.example/offer"} {
		values["SHOP_OFFER_URL"] = invalid
		if _, err := LoadFromMap(values); err == nil {
			t.Fatal("invalid offer URL accepted")
		}
	}
	values["SHOP_OFFER_URL"] = ""
	values["SHOP_CONTACTS_TEXT"] = strings.Repeat("я", 3001)
	if _, err := LoadFromMap(values); err == nil {
		t.Fatal("contacts exceed Telegram message allowance")
	}
}
