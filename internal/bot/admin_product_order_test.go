package bot

import (
	"context"
	"errors"
	"slices"
	"testing"

	"shop_bot/internal/storage"
)

func TestAdminProductDisplayOrderAcrossCatalogsAndGroups(t *testing.T) {
	e := adminTestEnv(t)
	ctx := context.Background()
	var err error
	e.catID, err = storage.NewSQLProductStore(e.db).CreateCategory(ctx, &storage.Category{Name: "Sort test category", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string, parent int64) int64 {
		zero := 0.0
		p := storage.Product{Name: "Sort test " + name, CategoryID: e.catID, PriceRUB: &zero, Stock: 1, IsActive: true}
		data := adminWriteTest(t, e, "products", 0, map[string]any{"product": p, "parent_id": parent}).(map[string]any)
		return data["product"].(*storage.Product).ID
	}
	a, b := create("A", 0), create("B", 0)
	c, d := create("C", a), create("D", a)
	// The legacy catalog requires a non-NULL photo even for text-only fixtures.
	if _, err := e.db.Conn().Exec("UPDATE products SET photo_url='' WHERE category_id=?", e.catID); err != nil {
		t.Fatal(err)
	}
	store := storage.NewSQLProductStore(e.db)
	ids := func(products []storage.Product) []int64 {
		out := make([]int64, 0, len(products))
		for _, p := range products {
			if p.CategoryID == e.catID {
				out = append(out, p.ID)
			}
		}
		return out
	}
	before, _, err := store.GetProductsByCategoryPaged(ctx, e.catID, 10, 0)
	if err != nil || !slices.Equal(ids(before), []int64{a, b, c, d}) {
		t.Fatalf("default order: %v %v", ids(before), err)
	}
	update := func(id int64, order int) {
		data, err := e.bot.adminProduct(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		card := data.(map[string]any)
		p := card["product"].(*storage.Product)
		p.SortOrder = order
		raw := adminJSON(t, map[string]any{"product": p, "parent_id": card["parent_id"], "revision": card["revision"]})
		if _, err = e.bot.AdminWrite(ctx, 55, "products", id, raw); err == nil {
			t.Fatal("buyer changed order")
		}
		saved, err := e.bot.AdminWrite(ctx, e2eAdminID, "products", id, raw)
		if err != nil || saved.(map[string]any)["product"].(*storage.Product).SortOrder != order {
			t.Fatal("order not saved", err)
		}
		if _, err = e.bot.AdminWrite(ctx, e2eAdminID, "products", id, raw); !errors.Is(err, storage.ErrAdminEditConflict) {
			t.Fatal("stale edit accepted", err)
		}
	}
	update(b, -5)
	update(c, 10)
	update(d, -20)
	page1, total, err := store.GetProductsByCategoryPaged(ctx, e.catID, 2, 0)
	if err != nil || total != 4 || !slices.Equal(ids(page1), []int64{d, b}) {
		t.Fatal("first bot page", ids(page1), total, err)
	}
	page2, _, err := store.GetProductsByCategoryPaged(ctx, e.catID, 2, 2)
	if err != nil || !slices.Equal(ids(page2), []int64{a, c}) {
		t.Fatal("second bot page", ids(page2), err)
	}
	all, err := store.GetProductsByCategory(ctx, e.catID)
	if err != nil || !slices.Equal(ids(all), []int64{d, b, a, c}) {
		t.Fatal("unpaged catalog", ids(all), err)
	}
	search, err := store.SearchProducts(ctx, "Sort test")
	if err != nil || !slices.Equal(ids(search), []int64{d, b, a, c}) {
		t.Fatal("search order", ids(search), err)
	}
	admin, _, err := store.ListProductsAdmin(ctx, 100, 0)
	if err != nil || !slices.Equal(ids(admin), []int64{d, b, a, c}) {
		t.Fatal("admin order", ids(admin), err)
	}
	legacy, err := storage.NewCatalogStore(e.db.Conn()).GetProductsByCategory(ctx, e.catID, 10, 0)
	if err != nil || !slices.Equal(ids(legacy), []int64{d, b, a, c}) {
		t.Fatal("legacy catalog", ids(legacy), err)
	}
	roots, total, err := e.bot.productGroups.ListRoots(ctx, e.catID, 1, 0)
	if err != nil || total != 2 || len(roots) != 1 || roots[0].ID != b {
		t.Fatal("Mini App root page", roots, total, err)
	}
	roots, _, err = e.bot.productGroups.ListRoots(ctx, e.catID, 1, 1)
	if err != nil || len(roots) != 1 || roots[0].ID != a || roots[0].ModificationCount != 2 {
		t.Fatal("group split", roots, err)
	}
	mods, total, err := e.bot.productGroups.ListModifications(ctx, a, 10, 0)
	if err != nil || total != 2 || !slices.Equal(mods, []int64{d, c}) {
		t.Fatal("modification order", mods, total, err)
	}
	// Equal ranks retain a stable ID tie-break, including pagination.
	update(d, 10)
	mods, _, err = e.bot.productGroups.ListModifications(ctx, a, 10, 0)
	if err != nil || !slices.Equal(mods, []int64{c, d}) {
		t.Fatal("equal ranks", mods, err)
	}
}
