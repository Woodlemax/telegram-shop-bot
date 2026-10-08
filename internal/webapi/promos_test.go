package webapi

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"shop_bot/internal/storage"
	"testing"
	"time"
)

func TestPromoAPIRestrictions(t *testing.T) {
	for _, name := range []string{"valid", "missing", "inactive", "expired", "exhausted", "personal_other", "used", "pending", "wrong_category"} {
		t.Run(name, func(t *testing.T) {
			f, db, _, fixed := realOpenPriceFixture(t)
			ctx := context.Background()
			promos := storage.NewSQLPromoStore(db)
			f.server.deps.Promos = promos
			f.server.deps.StarsOnlyPayments = true
			p := &storage.PromoCode{Code: "AUDIT10", Discount: 10, MaxUses: 0}
			var cat int64
			if err := db.Conn().QueryRow(`SELECT category_id FROM products WHERE id=?`, fixed).Scan(&cat); err != nil {
				t.Fatal(err)
			}
			if name == "expired" {
				old := time.Now().Add(-24 * time.Hour)
				p.ExpiresAt = &old
			}
			if name == "exhausted" {
				p.MaxUses = 1
			}
			if name == "wrong_category" {
				other, err := storage.NewSQLProductStore(db).CreateCategory(ctx, &storage.Category{Name: "Other", IsActive: true})
				if err != nil {
					t.Fatal(err)
				}
				p.CategoryID = &other
			}
			id, err := promos.CreatePromo(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			if name == "inactive" {
				if err := promos.DeactivatePromo(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			if name == "exhausted" {
				if _, err := db.Conn().Exec(`UPDATE promo_codes SET used_count=1 WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
			}
			if name == "personal_other" {
				if _, err := db.Conn().Exec(`UPDATE promo_codes SET bound_user_id=43 WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
			}
			if name == "pending" || name == "used" {
				status := storage.OrderStatusPending
				if name == "used" {
					status = storage.OrderStatusPaid
				}
				oid, err := storage.NewSQLOrderStore(db).CreateOrder(ctx, &storage.Order{UserID: 42, Status: status, PromoCode: p.Code}, nil)
				if err != nil {
					t.Fatal(err)
				}
				if name == "used" {
					if err := promos.UsePromo(ctx, id, 42, oid); err != nil {
						t.Fatal(err)
					}
				}
			}
			code := " audit10 "
			if name == "missing" {
				code = "NO_SUCH_CODE"
			}
			add := f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, fixed), true)
			if add.Code != 200 {
				t.Fatal(add.Body.String())
			}
			cart := decodeJSON(t, f.request(t, http.MethodGet, "/api/cart", "", true))
			stars := int64(cart["total_stars"].(float64))
			var beforeOrders, beforeUses int
			db.Conn().QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&beforeOrders)
			db.Conn().QueryRow(`SELECT used_count FROM promo_codes WHERE id=?`, id).Scan(&beforeUses)
			preview := f.request(t, http.MethodPost, "/api/cart/promo", fmt.Sprintf(`{"promo":%q}`, code), true)
			var afterOrders, afterUses int
			db.Conn().QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&afterOrders)
			db.Conn().QueryRow(`SELECT used_count FROM promo_codes WHERE id=?`, id).Scan(&afterUses)
			if beforeOrders != afterOrders || beforeUses != afterUses {
				t.Fatal("preview consumed promo or created order")
			}
			r := f.request(t, http.MethodPost, "/api/checkout", fmt.Sprintf(`{"method":"stars","promo":%q}`, code), true)
			if name == "valid" {
				if preview.Code != 200 || preview.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("preview status/cache", preview.Code)
				}
				data := decodeJSON(t, preview)
				if data["total_rub"] != float64(180) || data["total_stars"] != float64(stars*90/100) || data["original_total_rub"] != float64(200) {
					t.Fatal("preview totals differ", data)
				}
				if r.Code != 200 {
					t.Fatal(r.Body.String())
				}
				var rub float64
				var total int64
				if err := db.Conn().QueryRow(`SELECT total_rub,total_stars FROM orders ORDER BY id DESC LIMIT 1`).Scan(&rub, &total); err != nil {
					t.Fatal(err)
				}
				if rub != 180 || total != stars*90/100 {
					t.Fatalf("wrong discount RUB=%v Stars=%v", rub, total)
				}
				return
			}
			want := "promo_not_found"
			if name == "used" {
				want = "promo_already_used"
			}
			if name == "pending" {
				want = "promo_pending_order"
			}
			if name == "wrong_category" {
				want = "promo_category_mismatch"
			}
			if preview.Code != 400 || decodeJSON(t, preview)["error"] != want {
				t.Fatalf("preview restriction: %d %s", preview.Code, preview.Body.String())
			}
			if r.Code != 400 || decodeJSON(t, r)["error"] != want {
				t.Fatalf("status=%d body=%s expected=%s", r.Code, r.Body.String(), want)
			}
			var size int
			if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM cart_items WHERE user_id=42`).Scan(&size); err != nil {
				t.Fatal(err)
			}
			if size != 1 {
				t.Fatal("rejected promo changed cart")
			}
		})
	}
}
func TestPromoFreeGrant(t *testing.T) {
	f, db, _, fixed := realOpenPriceFixture(t)
	ctx := context.Background()
	promos := storage.NewSQLPromoStore(db)
	f.server.deps.Promos = promos
	id, err := promos.CreatePromo(ctx, &storage.PromoCode{Code: "FREE100", Discount: 100, MaxUses: 1})
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, fixed), true)
	r := f.request(t, http.MethodPost, "/api/checkout", `{"method":"free","promo":"FREE100"}`, true)
	if r.Code != 200 || decodeJSON(t, r)["free"] != true {
		t.Fatalf("free grant: %d %s", r.Code, r.Body.String())
	}
	used, err := promos.HasUserUsedPromo(ctx, id, 42)
	if err != nil || !used {
		t.Fatal("free promo use not recorded", err)
	}
	var count int
	db.Conn().QueryRow(`SELECT COUNT(*) FROM free_order_grants`).Scan(&count)
	if count != 1 {
		t.Fatal("missing free grant")
	}
}
func TestPromoCategoryDiscountScope(t *testing.T) {
	f, db, _, fixed := realOpenPriceFixture(t)
	ctx := context.Background()
	store := storage.NewSQLProductStore(db)
	product, err := store.GetProduct(ctx, fixed)
	if err != nil {
		t.Fatal(err)
	}
	othercat, err := store.CreateCategory(ctx, &storage.Category{Name: "Other", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	price := float64(200)
	other, err := store.CreateProduct(ctx, &storage.Product{CategoryID: othercat, Name: "Other item", PriceRUB: &price, IsActive: true, Stock: 2})
	if err != nil {
		t.Fatal(err)
	}
	promos := storage.NewSQLPromoStore(db)
	f.server.deps.Promos = promos
	_, err = promos.CreatePromo(ctx, &storage.PromoCode{Code: "CATEGORY10", Discount: 10, CategoryID: &product.CategoryID})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{fixed, other} {
		f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, id), true)
	}
	r := f.request(t, http.MethodPost, "/api/checkout", `{"method":"stars","promo":"CATEGORY10"}`, true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var rub float64
	if err := db.Conn().QueryRow(`SELECT total_rub FROM orders ORDER BY id DESC LIMIT 1`).Scan(&rub); err != nil {
		t.Fatal(err)
	}
	if math.Abs(rub-380) > .001 {
		t.Fatalf("category promo discounted unrelated item: total=%v want 380", rub)
	}
}

func TestPromoPreviewAuthBodyAndExpiryRevalidation(t *testing.T) {
	f, db, _, fixed := realOpenPriceFixture(t)
	ctx := context.Background()
	promos := storage.NewSQLPromoStore(db)
	f.server.deps.Promos = promos
	if f.request(t, http.MethodPost, "/api/cart/promo", `{"promo":"CODE"}`, false).Code != 401 {
		t.Fatal("unauthorized preview")
	}
	if f.request(t, http.MethodPost, "/api/cart/promo", `{`, true).Code != 400 {
		t.Fatal("invalid body")
	}
	f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, fixed), true)
	id, err := promos.CreatePromo(ctx, &storage.PromoCode{Code: "SHORT10", Discount: 10})
	if err != nil {
		t.Fatal(err)
	}
	preview := f.request(t, http.MethodPost, "/api/cart/promo", `{"promo":"SHORT10"}`, true)
	if preview.Code != 200 {
		t.Fatal(preview.Body.String())
	}
	if err := promos.DeactivatePromo(ctx, id); err != nil {
		t.Fatal(err)
	}
	r := f.request(t, http.MethodPost, "/api/checkout", `{"method":"stars","promo":"SHORT10"}`, true)
	if r.Code != 400 {
		t.Fatal("checkout used stale preview")
	}
	var orders int
	db.Conn().QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&orders)
	if orders != 0 {
		t.Fatal("invalid promo created order")
	}
}
func TestPromoFullCategoryCannotGrantUnrelatedItemsFree(t *testing.T) {
	f, db, open, fixed := realOpenPriceFixture(t)
	ctx := context.Background()
	store := storage.NewSQLProductStore(db)
	other, err := store.CreateCategory(ctx, &storage.Category{Name: "Other", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn().Exec(`UPDATE products SET category_id=? WHERE id=?`, other, fixed); err != nil {
		t.Fatal(err)
	}
	product, err := store.GetProduct(ctx, open)
	if err != nil {
		t.Fatal(err)
	}
	promos := storage.NewSQLPromoStore(db)
	f.server.deps.Promos = promos
	_, err = promos.CreatePromo(ctx, &storage.PromoCode{Code: "CATEGORY100", Discount: 100, CategoryID: &product.CategoryID})
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"price":100}`, open), true)
	f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, fixed), true)
	preview := f.request(t, http.MethodPost, "/api/cart/promo", `{"promo":"CATEGORY100"}`, true)
	if preview.Code != 200 {
		t.Fatal(preview.Body.String())
	}
	data := decodeJSON(t, preview)
	if data["total_rub"] != float64(200) || data["free_checkout"] != false {
		t.Fatal("unrelated items discounted", data)
	}
	r := f.request(t, http.MethodPost, "/api/checkout", `{"method":"free","promo":"CATEGORY100"}`, true)
	if r.Code != 400 {
		t.Fatal("mixed category accepted free checkout")
	}
	var n int
	db.Conn().QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&n)
	if n != 0 {
		t.Fatal("rejected free attempt created order")
	}
}
func TestPromoFullDiscountStarsRequestUsesFreeGrant(t *testing.T) {
	f, db, _, fixed := realOpenPriceFixture(t)
	ctx := context.Background()
	promos := storage.NewSQLPromoStore(db)
	f.server.deps.Promos = promos
	_, err := promos.CreatePromo(ctx, &storage.PromoCode{Code: "FREE100", Discount: 100})
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, http.MethodPost, "/api/cart", fmt.Sprintf(`{"product_id":%d,"delta":1}`, fixed), true)
	preview := f.request(t, http.MethodPost, "/api/cart/promo", `{"promo":"FREE100"}`, true)
	if preview.Code != 200 || decodeJSON(t, preview)["free_checkout"] != true {
		t.Fatal("full preview did not allow free fulfillment")
	}
	before := f.tg.endpoint
	r := f.request(t, http.MethodPost, "/api/checkout", `{"method":"stars","promo":"FREE100"}`, true)
	if r.Code != 200 || decodeJSON(t, r)["free"] != true {
		t.Fatal("zero-star request did not use free grant", r.Body.String())
	}
	if f.tg.endpoint != before {
		t.Fatal("free order created Telegram invoice")
	}
}
