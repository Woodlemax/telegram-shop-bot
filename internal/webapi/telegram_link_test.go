package webapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"shop_bot/internal/storage"
)

func TestProductTelegramLinkOptionalAPI(t *testing.T) {
	f, db, id, _ := realOpenPriceFixture(t)
	ctx := context.Background()
	store := storage.NewSQLProductStore(db)
	request := func() map[string]any {
		return decodeJSON(t, f.request(t, http.MethodGet, fmt.Sprintf("/api/products/%d", id), "", true))["product"].(map[string]any)
	}
	if _, ok := request()["telegram_url"]; ok {
		t.Fatal("empty link field rendered")
	}
	p, err := store.GetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.TelegramURL = "@PlaneModels"
	if err := store.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	if request()["telegram_url"] != "https://t.me/PlaneModels" {
		t.Fatal("API link missing")
	}
	p.TelegramURL = "https://t.me/+Invite_123"
	if err := store.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	if request()["telegram_url"] != "https://t.me/+Invite_123" {
		t.Fatal("API invite missing")
	}
	p.TelegramURL = ""
	if err := store.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, ok := request()["telegram_url"]; ok {
		t.Fatal("cleared link remains")
	}
	if _, err := db.Conn().Exec(`UPDATE products SET telegram_url='javascript:alert(1)' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := request()["telegram_url"]; ok {
		t.Fatal("unsafe external data exposed")
	}
}
