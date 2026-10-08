package storage

import (
	"context"
	"testing"
)

func TestQuantitySettingsMigrateExistingProducts(t *testing.T) {
	const migration = "026_product_quantity_settings.sql"
	db := migrationDBBefore(t, migration)
	for _, query := range []string{
		`INSERT INTO categories(id,name,is_active) VALUES(1,'Planes',1)`,
		`INSERT INTO products(id,category_id,name,price_usd,stock,is_digital,is_active) VALUES(1,1,'Physical',1,5,0,1),(2,1,'ZIP',1,0,1,1)`,
		`INSERT INTO cart_items(user_id,product_id,quantity) VALUES(42,1,3),(42,2,3)`,
	} {
		if _, err := db.Conn().Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	applyMigrationsFrom(t, db, migration)
	products := NewSQLProductStore(db)
	physical, err := products.GetProduct(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	digital, err := products.GetProduct(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if physical.InfiniteStock || physical.SingleInCart || !digital.InfiniteStock || !digital.SingleInCart {
		t.Fatalf("wrong migration flags: %+v %+v", physical, digital)
	}
	var physicalQty, digitalQty int
	if err := db.Conn().QueryRow(`SELECT quantity FROM cart_items WHERE product_id=1`).Scan(&physicalQty); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn().QueryRow(`SELECT quantity FROM cart_items WHERE product_id=2`).Scan(&digitalQty); err != nil {
		t.Fatal(err)
	}
	if physicalQty != 3 || digitalQty != 1 {
		t.Fatalf("quantities: %d %d", physicalQty, digitalQty)
	}

	// Unlimited products remain discoverable at zero stock in both catalog paths.
	paged, count, err := products.GetProductsByCategoryPaged(context.Background(), 1, 10, 0)
	if err != nil || count != 2 || len(paged) != 2 {
		t.Fatalf("paged: %v %d %v", paged, count, err)
	}
	search, err := products.SearchProducts(context.Background(), "ZIP")
	if err != nil || len(search) != 1 || !search[0].InfiniteStock || !search[0].SingleInCart {
		t.Fatalf("search: %v %v", search, err)
	}
}
