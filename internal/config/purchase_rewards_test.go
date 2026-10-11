package config

import "testing"

func TestPurchaseRewardsDefaultDisabled(t *testing.T) {
	for _, value := range []string{"", "false", "true"} {
		cfg, err := LoadFromMap(map[string]string{"BOT_TOKEN": "123:abc", "ADMIN_IDS": "42", "PURCHASE_REWARDS_ENABLED": value})
		if err != nil || cfg.PurchaseRewardsEnabled != (value == "true") {
			t.Fatalf("rewards %q: %v", value, err)
		}
	}
	if _, err := LoadFromMap(map[string]string{"BOT_TOKEN": "123:abc", "PURCHASE_REWARDS_ENABLED": "invalid"}); err == nil {
		t.Fatal("invalid rewards flag accepted")
	}
}
