package config

import "testing"

func TestBotAdminOnlyConfig(t *testing.T) {
	values := map[string]string{"BOT_TOKEN": "123:abc", "ADMIN_IDS": "42"}
	for _, value := range []string{"", "false", "true"} {
		values["BOT_ADMIN_ONLY"] = value
		cfg, err := LoadFromMap(values)
		if err != nil || cfg.BotAdminOnly != (value == "true") {
			t.Fatalf("mode %q: %v", value, err)
		}
	}
	values["BOT_ADMIN_ONLY"] = "invalid"
	if _, err := LoadFromMap(values); err == nil {
		t.Fatal("invalid bot mode accepted")
	}
}
