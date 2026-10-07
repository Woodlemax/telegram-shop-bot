package webapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestDigitalMiniAppStarsOnly(t *testing.T) {
	f := newFixture(t)
	p := f.cart.products[1]
	p.IsDigital = true
	f.cart.products[1] = p
	f.request(t, http.MethodPost, "/api/cart", `{"product_id":1,"delta":1}`, true)
	response := f.request(t, http.MethodGet, "/api/cart", "", true)
	var cart map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &cart); err != nil {
		t.Fatal(err)
	}
	if cart["stars_only"] != true {
		t.Fatal("digital cart not marked Stars-only")
	}
	for _, key := range []string{"yookassa_enabled", "stripe_enabled", "ton_enabled", "nowpayments_enabled"} {
		if cart[key] != false {
			t.Fatalf("%s offered for digital purchase", key)
		}
	}
	for _, rail := range []string{"crypto", "yookassa", "stripe", "ton", "nowpayments"} {
		response = f.request(t, http.MethodPost, "/api/checkout", fmt.Sprintf(`{"method":%q}`, rail), true)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "digital_stars_only") {
			t.Fatalf("%s: %d %s", rail, response.Code, response.Body.String())
		}
	}
	if len(f.orders.created) != 0 || f.tg.endpoint != "" {
		t.Fatal("rejected rail created an order or invoice")
	}
	response = f.request(t, http.MethodPost, "/api/checkout", `{"method":"stars"}`, true)
	if response.Code != http.StatusOK {
		t.Fatalf("Stars: %d %s", response.Code, response.Body.String())
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
