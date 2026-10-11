package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/service"
	"shop_bot/internal/storage"
)

type onboardingRewardsUsers struct{}

func (onboardingRewardsUsers) GetNewUsersWithoutOrders(context.Context, time.Duration, time.Duration) ([]storage.User, error) {
	return []storage.User{{TelegramID: 41, FirstName: "Ann", LanguageCode: "ru", ReferralCode: sql.NullString{String: "old-code", Valid: true}}, {TelegramID: 42, FirstName: "Bob", LanguageCode: "en"}}, nil
}

func TestOnboardingWithoutRewardsHasNoBonusAdvertising(t *testing.T) {
	i18n, err := service.NewI18nService("../locales")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var messages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test"}}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			w.WriteHeader(500)
			return
		}
		r.ParseForm()
		mu.Lock()
		messages = append(messages, r.Form.Get("text"))
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1}})
	}))
	defer srv.Close()
	api, err := tgbotapi.NewBotAPIWithAPIEndpoint("123:test", srv.URL+"/bot%s/%s")
	if err != nil {
		t.Fatal(err)
	}
	worker := NewOnboardingWorker(api, onboardingRewardsUsers{}, i18n, "testbot", time.Hour)
	worker.SetReferralEnabled(false)
	worker.runOnboarding(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 2 {
		t.Fatal("onboarding failed", messages)
	}
	for i, lang := range []string{"ru", "en"} {
		name := []string{"Ann", "Bob"}[i]
		if messages[i] != fmt.Sprintf(i18n.T(lang, "onboarding_greeting_plain"), name) || strings.Contains(messages[i], "old-code") {
			t.Fatal("bonus advertising sent", messages[i])
		}
	}
	for _, lang := range []string{"ru", "en", "es", "de", "zh"} {
		if text := i18n.T(lang, "onboarding_greeting_plain"); text == "onboarding_greeting_plain" || !strings.Contains(text, "%s") {
			t.Fatal("missing neutral greeting", lang)
		}
	}
}
