package webapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestDigitalMiniAppPaymentRails(t *testing.T) {
	for _, rail := range []string{"stars", "crypto", "yookassa", "stripe", "ton", "nowpayments"} {
		t.Run(rail, func(t *testing.T) {
			f := newFixture(t)
			f.cart.rubRate = 92.5
			f.cart.tonRate = 5
			f.orders.tonRate = 5
			p := f.cart.products[1]
			p.IsDigital = true
			f.cart.products[1] = p
			f.request(t, http.MethodPost, "/api/cart", `{"product_id":1,"delta":1}`, true)
			response := f.request(t, http.MethodGet, "/api/cart", "", true)
			var cart map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &cart); err != nil {
				t.Fatal(err)
			}
			if cart["stars_only"] != false {
				t.Fatal("digital cart still marked Stars-only")
			}
			for _, key := range []string{"yookassa_enabled", "stripe_enabled", "ton_enabled", "nowpayments_enabled"} {
				if cart[key] != true {
					t.Fatalf("%s missing for digital purchase", key)
				}
			}
			response = f.request(t, http.MethodPost, "/api/checkout", fmt.Sprintf(`{"method":%q}`, rail), true)
			if response.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", rail, response.Code, response.Body.String())
			}
			if len(f.orders.created) != 1 {
				t.Fatal("checkout did not create exactly one order")
			}
		})
	}
}

func TestDigitalProductDoesNotExposeArchive(t *testing.T) {
	f := newFixture(t)
	p := f.cart.products[1]
	p.IsDigital = true
	p.DigitalContent = "private-file-id"
	encoded, err := json.Marshal(toProductJSON(&p))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"is_digital":true`) || strings.Contains(string(encoded), "private-file-id") {
		t.Fatalf("product payload: %s", encoded)
	}
}
