package config

import "testing"

func TestStarsOnlyConfigAndFixedUSDQuote(t *testing.T) {
	values := map[string]string{"BOT_TOKEN": "123:abc", "ADMIN_IDS": "42", "USD_TO_RUB_RATE": "100", "STARS_ONLY_PAYMENTS": "true"}
	cfg, err := LoadFromMap(values)
	if err != nil || !cfg.StarsOnlyPayments || cfg.USDToRUBRate != 100 || cfg.USDToStarsRate != 50 {
		t.Fatalf("Stars-only quote: %+v %v", cfg, err)
	}
	values["STARS_ONLY_PAYMENTS"] = "false"
	cfg, err = LoadFromMap(values)
	if err != nil || cfg.StarsOnlyPayments {
		t.Fatal("policy cannot be disabled")
	}
	values["STARS_ONLY_PAYMENTS"] = "invalid"
	if _, err := LoadFromMap(values); err == nil {
		t.Fatal("invalid payment policy accepted")
	}
}
