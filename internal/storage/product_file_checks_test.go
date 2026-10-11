package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func fileCheckDigest(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
func TestProductFileChecksVersionsOwnershipAndPaidDownloads(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	products := NewSQLProductStore(db)
	cart := NewSQLCartStore(db)
	orders := NewSQLOrderStore(db)
	checks := NewProductFileCheckStore(db.Conn())
	archives := NewDigitalArchiveStore(db)
	category, err := products.CreateCategory(ctx, &Category{Name: "Planes", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	zero := float64(0)
	id, err := products.CreateProduct(ctx, &Product{CategoryID: category, Name: "Protected add-on", PriceRUB: &zero, IsDigital: true, InfiniteStock: true, SingleInCart: true, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = archives.Add(ctx, id, "download-zip", "addon.zip", 100); err != nil {
		t.Fatal(err)
	}
	if err = cart.AddItem(ctx, 42, id); err != nil {
		t.Fatal(err)
	}
	if _, err = checks.Save(ctx, id, true, "Original file", 0); !errors.Is(err, ErrInvalidFileRequirement) {
		t.Fatal("enabled without reference", err)
	}
	ref, err := checks.ReplaceReference(ctx, id, "CAN_1.STL", fileCheckDigest("original"), 8, 0)
	if err != nil {
		t.Fatal(err)
	}
	ref, err = checks.Save(ctx, id, true, "Attach the original CAN_1.STL", ref.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = cart.AddItem(ctx, 42, id); !errors.Is(err, ErrFileVerificationRequired) {
		t.Fatal("unverified add", err)
	}
	if err = cart.UpdateQuantity(ctx, 42, id, 1); !errors.Is(err, ErrFileVerificationRequired) {
		t.Fatal("unverified quantity", err)
	}
	create := func() (int64, error) {
		return orders.CreateOrder(ctx, &Order{UserID: 42, Status: OrderStatusPending}, []OrderItem{{ProductID: id, ProductName: "Add-on", Quantity: 1}})
	}
	if _, err = create(); !errors.Is(err, ErrFileVerificationRequired) {
		t.Fatal("order bypass", err)
	}
	if err = checks.Verify(ctx, 42, id, ref.Version, fileCheckDigest("modified"), 8); !errors.Is(err, ErrFileVerificationMismatch) {
		t.Fatal("wrong file", err)
	}
	if err = checks.Verify(ctx, 42, id, ref.Version, fileCheckDigest("original"), 8); err != nil {
		t.Fatal(err)
	}
	status, err := checks.Status(ctx, 43, id)
	if err != nil || status.Verified {
		t.Fatal("another user unlocked", status, err)
	}
	encoded, _ := json.Marshal(ref)
	if strings.Contains(string(encoded), fileCheckDigest("original")) || strings.Contains(string(encoded), "sha256") {
		t.Fatal("reference hash exposed")
	}
	if err = cart.AddItem(ctx, 42, id); err != nil {
		t.Fatal(err)
	}
	paid, err := create()
	if err != nil {
		t.Fatal(err)
	}
	if err = orders.ConfirmFreeOrder(ctx, paid, 42); err != nil {
		t.Fatal(err)
	}
	pending, err := create()
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := checks.ReplaceReference(ctx, id, "renamed.stl", fileCheckDigest("original"), 8, ref.Revision)
	if err != nil || renamed.Version != ref.Version {
		t.Fatal("rename invalidated reference", renamed, err)
	}
	if _, err = checks.Save(ctx, id, false, "", ref.Revision); !errors.Is(err, ErrAdminEditConflict) {
		t.Fatal("stale admin revision accepted", err)
	}
	updated, err := checks.ReplaceReference(ctx, id, "CAN_1.STL", fileCheckDigest("new-file"), 8, renamed.Revision)
	if err != nil || updated.Version != ref.Version+1 {
		t.Fatal(updated, err)
	}
	status, err = checks.Status(ctx, 42, id)
	if err != nil || status.Verified {
		t.Fatal("old approval survived", status, err)
	}
	if err = checks.Verify(ctx, 42, id, ref.Version, fileCheckDigest("original"), 8); !errors.Is(err, ErrFileVerificationMismatch) {
		t.Fatal("old upload race", err)
	}
	if err = orders.ClaimCheckoutProvider(ctx, pending, PaymentMethodStars); !errors.Is(err, ErrFileVerificationRequired) {
		t.Fatal("resumed invoice bypass", err)
	}
	if err = orders.ConfirmFreeOrder(ctx, pending, 42); !errors.Is(err, ErrFileVerificationRequired) {
		t.Fatal("resumed free order bypass", err)
	}
	files, err := archives.ForOrder(ctx, 42, paid)
	if err != nil || len(files) != 1 || files[0].ProductID != id {
		t.Fatal("paid download revoked", files, err)
	}
	if err = checks.Verify(ctx, 42, id, updated.Version, fileCheckDigest("new-file"), 8); err != nil {
		t.Fatal(err)
	}
	if err = orders.ConfirmFreeOrder(ctx, pending, 42); err != nil {
		t.Fatal("reverified pending order blocked", err)
	}
	updated, err = checks.Save(ctx, id, false, "", updated.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = cart.AddItem(ctx, 43, id); err != nil {
		t.Fatal("disabled check still blocks", err)
	}
	if _, err = checks.Save(ctx, id, true, "", updated.Revision); !errors.Is(err, ErrInvalidFileRequirement) {
		t.Fatal("enabled without instructions", err)
	}
}
