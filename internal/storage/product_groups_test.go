package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestProductGroupsPaginationValidationAndUnavailableParent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	products := NewSQLProductStore(db)
	groups := NewProductGroupStore(db.Conn())
	cat, err := products.CreateCategory(ctx, &Category{Name: "Planes", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	secondCat, err := products.CreateCategory(ctx, &Category{Name: "Other", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(name string, category int64, stock int, active, soon bool) int64 {
		id, err := products.CreateProduct(ctx, &Product{Name: name, CategoryID: category, PriceUSD: 1, Stock: stock, IsActive: active, ComingSoon: soon})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	main := seed("Main", cat, 10, true, false)
	another := seed("Another", cat, 10, true, false)
	children := []int64{}
	for i := 0; i < 23; i++ {
		id := seed(fmt.Sprintf("Mod %d", i), cat, 1, true, false)
		if err = groups.SetParent(ctx, id, main); err != nil {
			t.Fatal(err)
		}
		children = append(children, id)
	}
	soon := seed("Soon mod", cat, 0, true, true)
	if err = groups.SetParent(ctx, soon, main); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{seed("Inactive", cat, 1, false, false), seed("Unavailable", cat, 0, true, false)} {
		if err = groups.SetParent(ctx, id, main); err != nil {
			t.Fatal(err)
		}
	}
	page, total, err := groups.ListRoots(ctx, cat, 1, 0)
	if err != nil || total != 2 || len(page) != 1 || page[0].ID != main || page[0].ModificationCount != 24 {
		t.Fatalf("root page: %+v %d %v", page, total, err)
	}
	page, total, err = groups.ListRoots(ctx, cat, 1, 1)
	if err != nil || total != 2 || len(page) != 1 || page[0].ID != another {
		t.Fatalf("second root: %+v %d %v", page, total, err)
	}
	mods, total, err := groups.ListModifications(ctx, main, 20, 0)
	if err != nil || total != 24 || len(mods) != 20 {
		t.Fatalf("mods page: %v %d %v", mods, total, err)
	}
	mods, total, err = groups.ListModifications(ctx, main, 20, 20)
	if err != nil || total != 24 || len(mods) != 4 || mods[3] != soon {
		t.Fatalf("mods second page: %v %d %v", mods, total, err)
	}
	foreign := seed("Foreign", secondCat, 1, true, false)
	for _, pair := range [][2]int64{{main, main}, {another, children[0]}, {main, another}, {foreign, main}, {children[0], 999999}} {
		if err = groups.SetParent(ctx, pair[0], pair[1]); !errors.Is(err, ErrInvalidProductGroup) {
			t.Fatalf("invalid link accepted %v: %v", pair, err)
		}
	}
	if err = groups.SetParent(ctx, children[0], 0); err != nil {
		t.Fatal(err)
	}
	parent, err := groups.Parent(ctx, children[0])
	if err != nil || parent != 0 {
		t.Fatal("detach failed")
	}
	if err = groups.SetParent(ctx, children[0], another); err != nil {
		t.Fatal(err)
	}
	// Moving a parent to another category must not hide the child products.
	p, _ := products.GetProduct(ctx, main)
	p.CategoryID = secondCat
	if err = products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	roots, total, err := groups.ListRoots(ctx, cat, 100, 0)
	if err != nil || total != 24 {
		t.Fatalf("orphan visibility: %d %v %+v", total, err, roots)
	}
	p.CategoryID = cat
	p.Stock = 0
	if err = products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, total, err = groups.ListRoots(ctx, cat, 100, 0)
	if err != nil || total != 24 {
		t.Fatal("unavailable main hides modifications")
	}
	p.Stock = 10
	if err = products.UpdateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err = products.DeleteProduct(ctx, main); err != nil {
		t.Fatal(err)
	}
	_, total, err = groups.ListRoots(ctx, cat, 100, 0)
	if err != nil || total != 24 {
		t.Fatal("deleted main hides modifications")
	}
}
