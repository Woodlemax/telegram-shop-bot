package storage

import (
	"context"
	"errors"
	"testing"
)

func TestNormalizeTelegramProductLink(t *testing.T) {
	for raw, want := range map[string]string{"": "", " @PlaneModels ": "https://t.me/PlaneModels", "t.me/plane_models": "https://t.me/plane_models", "https://telegram.me/plane_models/": "https://t.me/plane_models", "https://t.me/+AbC_123-x": "https://t.me/+AbC_123-x", "https://t.me/joinchat/AbC_123-x": "https://t.me/joinchat/AbC_123-x"} {
		got, err := NormalizeTelegramURL(raw)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", raw, got, err)
		}
	}
	for _, raw := range []string{"javascript:alert(1)", "http://t.me/planes", "https://evil.example/planes", "https://t.me.evil.example/planes", "https://t.me@evil.example/planes", "https://user@t.me/planes", "https://t.me:443/planes", "https://t.me/planes?x=1", "https://t.me/planes/123", "https://t.me/+", "https://t.me/joinchat/", "https://t.me/planes\nhttps://evil.example"} {
		if _, err := NormalizeTelegramURL(raw); !errors.Is(err, ErrInvalidTelegramURL) {
			t.Fatalf("invalid %q: %v", raw, err)
		}
	}
}
func TestProductTelegramMigrationAndStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := migrationDBBefore(t, "028_product_telegram_url.sql")
	cat := seedCategory(t, db, "Planes", "✈️")
	res, err := db.Conn().Exec(`INSERT INTO products(category_id,name,description,price_usd,price_stars,stock,is_active) VALUES(?,'Plane','Description',10,500,5,1)`, cat)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if err := db.migrate(); err != nil {
		t.Fatal(err)
	}
	store := NewSQLProductStore(db)
	p, err := store.GetProduct(ctx, id)
	if err != nil || p.TelegramURL != "" || p.Description != "Description" || p.PriceUSD != 10 {
		t.Fatalf("legacy product changed: %+v %v", p, err)
	}
	p.TelegramURL = "@PlaneModels"
	if err := store.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	p, err = store.GetProduct(ctx, id)
	if err != nil || p.TelegramURL != "https://t.me/PlaneModels" {
		t.Fatalf("update not persisted: %+v %v", p, err)
	}
	lists, err := store.GetProductsByCategory(ctx, cat)
	if err != nil || lists[0].TelegramURL != p.TelegramURL {
		t.Fatal("category lost link", err)
	}
	lists, _, err = store.GetProductsByCategoryPaged(ctx, cat, 10, 0)
	if err != nil || lists[0].TelegramURL != p.TelegramURL {
		t.Fatal("paged category lost link", err)
	}
	lists, err = store.SearchProducts(ctx, "Plane")
	if err != nil || lists[0].TelegramURL != p.TelegramURL {
		t.Fatal("search lost link", err)
	}
	p.TelegramURL = "https://evil.example/"
	if err := store.UpdateProduct(ctx, p); !errors.Is(err, ErrInvalidTelegramURL) {
		t.Fatal(err)
	}
	saved, err := store.GetProduct(ctx, id)
	if err != nil || saved.TelegramURL != "https://t.me/PlaneModels" {
		t.Fatal("invalid update changed link", err)
	}
	saved.TelegramURL = ""
	if err := store.UpdateProduct(ctx, saved); err != nil {
		t.Fatal(err)
	}
	p, err = store.GetProduct(ctx, id)
	if err != nil || p.TelegramURL != "" {
		t.Fatal("link not removed", err)
	}
	newID, err := store.CreateProduct(ctx, &Product{CategoryID: cat, Name: "Invite plane", TelegramURL: "https://t.me/+AbC_123-x", Stock: 1, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err = store.GetProduct(ctx, newID)
	if err != nil || p.TelegramURL != "https://t.me/+AbC_123-x" {
		t.Fatal("create lost link", err)
	}
}
