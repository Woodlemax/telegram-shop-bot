package payment

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestYooKassaDescriptionIncludesOrderAndPreservesUnicodeLimit(t *testing.T) {
	_, id, api, client := newCheckoutFixture(t)
	title := strings.Repeat("Самолёт ✈ ", 30)
	if _, err := client().CreatePayment(context.Background(), id, 10001, title); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	for _, raw := range api.requests {
		var request struct {
			Body struct {
				Description string `json:"description"`
			}
		}
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			t.Fatal(err)
		}
		text := request.Body.Description
		if !strings.HasPrefix(text, "Заказ №") || !strings.Contains(text, "Самолёт") || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 128 {
			t.Fatalf("description: %s", text)
		}
	}
}
