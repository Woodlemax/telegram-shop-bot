package bot

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"shop_bot/internal/config"
	"shop_bot/internal/service"
	"shop_bot/internal/storage"
)

func adminJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func adminWriteTest(t *testing.T, e *e2eEnv, resource string, id int64, value any) any {
	t.Helper()
	v, err := e.bot.AdminWrite(context.Background(), e2eAdminID, resource, id, adminJSON(t, value))
	if err != nil {
		t.Fatalf("%s: %v", resource, err)
	}
	return v
}
func adminTestEnv(t *testing.T) *e2eEnv {
	return newE2EEnvWithConfig(t, func(c *config.Config) { c.WebAppURL = "https://shop.example/app/"; c.USDToRUBRate = 100 })
}

func TestAdminAppEntryAndLegacyCommandsCannotMutate(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.WebAppURL = "https://shop.example/app/"; c.AdminMiniAppOnly = true })
	for _, command := range []string{"/admin", "/help", "/deleteproduct 1", "/rubrate 80"} {
		calls := e.cmd(e2eAdminID, command, "ru")
		found := false
		for _, call := range calls {
			if call.Method == "sendMessage" && strings.Contains(call.markup(), "https://shop.example/admin/") {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s missing admin entry", command)
		}
	}
	if e.qInt("SELECT count(*) FROM products WHERE deleted_at IS NULL") != 2 {
		t.Fatal("legacy command changed catalog")
	}
	for _, call := range e.cmd(55, "/admin", "ru") {
		if strings.Contains(call.markup(), "/admin/") {
			t.Fatal("buyer received admin entry")
		}
	}
	for _, call := range e.cb(55, "admin:products", "ru") {
		if strings.Contains(call.markup(), "/admin/") {
			t.Fatal("buyer callback received admin entry")
		}
	}
}

func TestAdminAppProductTransactionsPhotosAndAuthorization(t *testing.T) {
	e := adminTestEnv(t)
	ctx := context.Background()
	price := 1500.0
	p := storage.Product{Name: "Самолёт", CategoryID: e.catID, PriceRUB: &price, Stock: 5, IsActive: true, InfiniteStock: true, SingleInCart: true}
	data := adminWriteTest(t, e, "products", 0, map[string]any{"product": p}).(map[string]any)
	created := data["product"].(*storage.Product)
	id := created.ID
	if created.PriceRUB == nil || *created.PriceRUB != 1500 || !created.InfiniteStock || !created.SingleInCart {
		t.Fatal("metadata lost")
	}
	for _, resource := range []string{"bootstrap", "products", "orders", "settings", "export", "payment-reviews"} {
		if _, err := e.bot.AdminRead(ctx, 55, resource, 0, url.Values{}); err == nil {
			t.Fatalf("buyer read %s", resource)
		}
	}
	if _, err := e.bot.AdminWrite(ctx, 55, "product-delete", id, adminJSON(t, map[string]any{})); err == nil {
		t.Fatal("buyer deleted product")
	}
	if _, err := e.bot.AdminUpload(ctx, 55, id, "archive", "x.zip", strings.NewReader("x")); err == nil {
		t.Fatal("buyer uploaded archive")
	}
	created.Name = "Изменён"
	revision := data["revision"].(string)
	updated := adminWriteTest(t, e, "products", id, map[string]any{"product": created, "revision": revision}).(map[string]any)
	if _, err := e.bot.AdminWrite(ctx, e2eAdminID, "products", id, adminJSON(t, map[string]any{"product": created, "revision": revision})); !errors.Is(err, storage.ErrAdminEditConflict) {
		t.Fatalf("stale edit accepted: %v", err)
	}
	before := e.qInt("SELECT count(*) FROM products")
	bad := p
	bad.Name = "Invalid child"
	bad.ID = 0
	if _, err := e.bot.AdminWrite(ctx, e2eAdminID, "products", 0, adminJSON(t, map[string]any{"product": bad, "parent_id": 99999})); err == nil {
		t.Fatal("invalid parent accepted")
	}
	if e.qInt("SELECT count(*) FROM products") != before {
		t.Fatal("failed group creation did not roll back")
	}
	bad = *updated["product"].(*storage.Product)
	bad.Name = "Must roll back"
	if _, err := e.bot.AdminWrite(ctx, e2eAdminID, "products", id, adminJSON(t, map[string]any{"product": bad, "revision": updated["revision"], "parent_id": 99999})); err == nil {
		t.Fatal("invalid parent update accepted")
	}
	if e.qStr("SELECT name FROM products WHERE id=?", id) != "Изменён" {
		t.Fatal("failed group update changed product")
	}
	refs := []string{"https://example.com/one.png", "https://example.com/two.png"}
	photos := adminWriteTest(t, e, "photos", id, map[string]any{"photos": refs}).(map[string]any)
	if photos["product"].(*storage.Product).PhotoURL != refs[0] {
		t.Fatal("cover not selected")
	}
	adminWriteTest(t, e, "photos", id, map[string]any{"photos": []string{refs[1], refs[0]}})
	if e.qStr("SELECT photo_url FROM products WHERE id=?", id) != refs[1] {
		t.Fatal("cover not reordered")
	}
	for _, invalid := range [][]string{{refs[0], refs[0]}, {"javascript:alert(1)"}} {
		if _, err := e.bot.AdminWrite(ctx, e2eAdminID, "photos", id, adminJSON(t, map[string]any{"photos": invalid})); err == nil {
			t.Fatal("invalid photo accepted")
		}
	}
	var pngBytes bytes.Buffer
	_ = png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 5, 5)))
	uploaded, err := e.bot.AdminUpload(ctx, e2eAdminID, id, "photo", "image.png", bytes.NewReader(pngBytes.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	list := uploaded.(map[string]any)["photos"].([]string)
	if len(list) != 3 || !strings.Contains(list[2], "/api/media/") {
		t.Fatal("uploaded photo missing")
	}
	if e.qInt("SELECT count(*) FROM admin_app_audit WHERE admin_id=?", e2eAdminID) < 4 {
		t.Fatal("audit missing")
	}
	if _, err = e.db.Conn().Exec("INSERT INTO cart_items(user_id,product_id,quantity) VALUES(?,?,1)", e2eAdminID, id); err != nil {
		t.Fatal(err)
	}
	adminWriteTest(t, e, "product-delete", id, map[string]any{})
	if e.qInt("SELECT count(*) FROM cart_items WHERE product_id=?", id) != 0 {
		t.Fatal("deleted product left in cart")
	}
}

type adminZeros struct{}

func (adminZeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestAdminAppEightyMegabyteArchiveAndPriorPurchases(t *testing.T) {
	e := adminTestEnv(t)
	ctx := context.Background()
	buyer := int64(781)
	uploadDigital(e, "old-telegram-file")
	e.cmd(buyer, "/start", "ru")
	order := e.placeOrder(buyer, e.prodReg, "")
	e.payWithStars(buyer, order, "admin-local-archive-charge")
	deliveryID := e.qInt("SELECT id FROM digital_deliveries WHERE order_id=?", order)
	path := filepath.Join(t.TempDir(), "plane.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zipWriter := zip.NewWriter(f)
	entry, err := zipWriter.CreateHeader(&zip.FileHeader{Name: "model.stl", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.CopyN(entry, adminZeros{}, 80<<20); err != nil {
		t.Fatal(err)
	}
	if err = zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err = e.bot.AdminUpload(ctx, e2eAdminID, e.prodReg, "archive", "plane.zip", file); err != nil {
		t.Fatal(err)
	}
	owned, err := e.bot.archives.Owned(ctx, buyer, deliveryID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(owned.FileID, "local:") || owned.FileName != "plane.zip" {
		t.Fatalf("prior buyer archive not updated: %+v", owned)
	}
	stored, isLocal := service.LocalArchivePath(e.bot.AdminMediaDirectory(), owned.FileID)
	if !isLocal {
		t.Fatal("not local")
	}
	stat, err := os.Stat(stored)
	if err != nil || stat.Size() < 80<<20 {
		t.Fatal("archive bytes lost")
	}
	for _, invalid := range []struct{ kind, name, body string }{{"archive", "bad.zip", "not zip"}, {"photo", "script.svg", "<svg onload='x'/>"}} {
		if _, err = e.bot.AdminUpload(ctx, e2eAdminID, e.prodReg, invalid.kind, invalid.name, strings.NewReader(invalid.body)); err == nil {
			t.Fatal("invalid upload accepted")
		}
	}
	files, _ := filepath.Glob(filepath.Join(e.bot.AdminMediaDirectory(), "upload-*"))
	if len(files) > 0 {
		t.Fatal("temporary uploads retained")
	}
	beforeCalls := e.tg.count()
	if err = e.bot.archives.RequestOrderDownload(ctx, buyer, order, e.prodReg); err != nil {
		t.Fatal(err)
	}
	e.bot.ProcessDigitalDeliveries(ctx)
	calls := e.tg.since(beforeCalls)
	found := false
	for _, call := range calls {
		if call.Method == "sendMessage" && strings.Contains(call.markup(), "archive-download") {
			found = true
		}
	}
	if !found || len(documentCalls(calls)) > 0 {
		t.Fatal("local archive not delivered by signed link")
	}
}

func TestAdminAppFinancialConfirmationsAreActorBoundAndSingleUse(t *testing.T) {
	e := adminTestEnv(t)
	ctx := context.Background()
	buyer := int64(51)
	e.cmd(buyer, "/start", "ru")
	e.bot.cfg.AdminIDs = append(e.bot.cfg.AdminIDs, 9002)
	result := adminWriteTest(t, e, "balance-preview", 0, map[string]any{"user_id": buyer, "delta_rub": 100, "reason": "test"}).(map[string]any)
	token := result["token"].(string)
	body := adminJSON(t, map[string]any{"token": token})
	if _, err := e.bot.AdminWrite(ctx, 9002, "confirm", 0, body); err == nil {
		t.Fatal("another admin used confirmation")
	}
	adminWriteTest(t, e, "rub-rate", 0, map[string]any{"rate": 200})
	adminWriteTest(t, e, "confirm", 0, map[string]any{"token": token})
	balance, err := e.bot.balances.GetBalance(ctx, buyer)
	if err != nil || balance != 1 {
		t.Fatalf("confirmed amount changed after rate change: %v %v", balance, err)
	}
	if _, err = e.bot.AdminWrite(ctx, e2eAdminID, "confirm", 0, body); err == nil {
		t.Fatal("confirmation replayed")
	}
	next := adminWriteTest(t, e, "balance-preview", 0, map[string]any{"user_id": buyer, "delta_rub": 200}).(map[string]any)["token"].(string)
	c := e.bot.adminConfirmations[next]
	c.Expires = time.Now().Add(-time.Second)
	e.bot.adminConfirmations[next] = c
	if _, err = e.bot.AdminWrite(ctx, e2eAdminID, "confirm", 0, adminJSON(t, map[string]any{"token": next})); err == nil {
		t.Fatal("expired confirmation accepted")
	}
}

func TestAdminAppSectionsAndPromoScopes(t *testing.T) {
	e := adminTestEnv(t)
	ctx := context.Background()
	created := adminWriteTest(t, e, "categories", 0, storage.Category{Name: "Скрытая категория", IsActive: false}).(map[string]any)["id"].(int64)
	data, err := e.bot.AdminRead(ctx, e2eAdminID, "categories", 0, url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range data.(map[string]any)["categories"].([]storage.Category) {
		if c.ID == created && !c.IsActive {
			found = true
		}
	}
	if !found {
		t.Fatal("inactive category unavailable to administrator")
	}
	for _, p := range []storage.PromoCode{{Code: "all", Discount: 10}, {Code: "category", Discount: 20, CategoryID: &e.catID}, {Code: "products", Discount: 30, ProductIDs: []int64{e.prodReg}}} {
		result := adminWriteTest(t, e, "promos", 0, p).(map[string]any)
		id := result["id"].(int64)
		if e.qStr("SELECT code FROM promo_codes WHERE id=?", id) != strings.ToUpper(p.Code) {
			t.Fatal("promo not normalized")
		}
		if p.ProductIDs != nil && e.qStr("SELECT product_ids FROM promo_codes WHERE id=?", id) != "["+strconv.FormatInt(e.prodReg, 10)+"]" {
			t.Fatal("product scope lost")
		}
		adminWriteTest(t, e, "promo-deactivate", id, map[string]any{})
	}
	for _, resource := range []string{"bootstrap", "products", "categories", "orders", "promos", "reviews", "payment-reviews", "analytics", "settings", "export"} {
		if _, err := e.bot.AdminRead(ctx, e2eAdminID, resource, 0, url.Values{}); err != nil {
			t.Fatalf("section %s: %v", resource, err)
		}
	}
}

func TestAdminAppInventorySupportsModificationEditor(t *testing.T) {
	e := adminTestEnv(t)
	ctx := context.Background()
	price := 500.0
	makeProduct := func(name string, parent int64) map[string]any {
		t.Helper()
		return adminWriteTest(t, e, "products", 0, map[string]any{"product": storage.Product{
			Name: name, Description: "Описание модели", CategoryID: e.catID,
			PriceRUB: &price, Stock: 0, IsActive: false, ComingSoon: true,
			InfiniteStock: true, SingleInCart: true, TelegramURL: "https://t.me/plane_group",
		}, "parent_id": parent}).(map[string]any)
	}
	main := makeProduct("Основная модель", 0)["product"].(*storage.Product)
	childData := makeProduct("Модификация", main.ID)
	child := childData["product"].(*storage.Product)
	adminWriteTest(t, e, "photos", main.ID, map[string]any{"photos": []string{"https://example.com/cover.png"}})
	if _, err := e.db.Conn().Exec(`UPDATE products SET is_digital=1,digital_content='private-file-id' WHERE id=?`, main.ID); err != nil {
		t.Fatal(err)
	}
	data, err := e.bot.AdminRead(ctx, e2eAdminID, "products", 0, url.Values{"category": {strconv.FormatInt(e.catID, 10)}})
	if err != nil {
		t.Fatal(err)
	}
	list := data.(map[string]any)
	parents := list["parent_ids"].(map[int64]int64)
	counts := list["modification_counts"].(map[int64]int)
	if parents[child.ID] != main.ID || counts[main.ID] != 1 {
		t.Fatal("group metadata missing")
	}
	found := false
	for _, item := range list["products"].([]storage.Product) {
		if item.ID != main.ID {
			continue
		}
		found = true
		if item.CategoryID != e.catID || item.PriceRUB == nil || *item.PriceRUB != 500 || item.PhotoURL != "https://example.com/cover.png" || item.Description != "Описание модели" || !item.IsDigital || item.IsActive || !item.ComingSoon || !item.InfiniteStock || !item.SingleInCart || item.TelegramURL != "https://t.me/plane_group" {
			t.Fatalf("inventory omitted editor fields: %+v", item)
		}
		if item.DigitalContent != "" {
			t.Fatal("private content exposed in inventory")
		}
	}
	if !found {
		t.Fatal("main model missing from category-filtered inventory")
	}
	// Editing an already linked child must preserve its parent after reload/save.
	child.Name = "Обновлённая модификация"
	adminWriteTest(t, e, "products", child.ID, map[string]any{"product": child, "parent_id": main.ID, "revision": childData["revision"]})
	if parent, err := e.bot.productGroups.Parent(ctx, child.ID); err != nil || parent != main.ID {
		t.Fatal("existing link lost on save", parent, err)
	}
	// Selecting another real main model moves the child, selecting zero detaches it.
	other := makeProduct("Вторая основная модель", 0)["product"].(*storage.Product)
	for _, parent := range []int64{other.ID, 0} {
		card, err := e.bot.adminProduct(ctx, child.ID)
		if err != nil {
			t.Fatal(err)
		}
		current := card.(map[string]any)
		adminWriteTest(t, e, "products", child.ID, map[string]any{"product": current["product"], "parent_id": parent, "revision": current["revision"]})
		if saved, err := e.bot.productGroups.Parent(ctx, child.ID); err != nil || saved != parent {
			t.Fatal("parent selection not saved", saved, err)
		}
	}
}

func TestAdminAppRefundReceiptAndReviewUseExistingLedger(t *testing.T) {
	e := adminTestEnv(t)
	order := seedRefundOrder(t, e, storage.PaymentMethodStars, refundStarsCharge)
	pending := adminWriteTest(t, e, "refund-preview", order, map[string]any{"amount_minor": refundFullStars}).(map[string]any)
	if e.qInt("SELECT count(*) FROM refunds WHERE order_id=?", order) != 0 {
		t.Fatal("preview changed ledger")
	}
	for _, call := range e.tg.since(0) {
		if call.Method == "refundStarPayment" {
			t.Fatal("preview refunded money")
		}
	}
	adminWriteTest(t, e, "confirm", 0, map[string]any{"token": pending["token"]})
	if e.qStr("SELECT payment_state FROM orders WHERE id=?", order) != "refunded" {
		t.Fatal("refund not reflected in ledger")
	}
	count := 0
	for _, call := range e.tg.since(0) {
		if call.Method == "refundStarPayment" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("refund provider called more than once")
	}
	review := seedDuplicateRefundedCase(t, e)
	preview := adminWriteTest(t, e, "review-preview", 0, map[string]any{"provider": "stars", "order_id": review, "action": "settle"}).(map[string]any)
	adminWriteTest(t, e, "confirm", 0, map[string]any{"token": preview["token"]})
	if e.qStr("SELECT payment_state FROM orders WHERE id=?", review) != "settled" {
		t.Fatal("review did not restore settled state")
	}
	e.cmd(refundBuyer, "/start", "ru")
	paid := seedRefundOrder(t, e, storage.PaymentMethodYooKassa, refundYooPayment)
	link := "https://lknpd.nalog.ru/api/v1/receipt/test/test-receipt/print"
	before := e.tg.count()
	adminWriteTest(t, e, "receipt", paid, map[string]any{"url": link})
	if e.qStr("SELECT delivery_status FROM order_tax_receipts WHERE order_id=?", paid) != "sent" {
		t.Fatal("receipt not delivered")
	}
	after := e.tg.count()
	if after <= before {
		t.Fatal("receipt message missing")
	}
	adminWriteTest(t, e, "receipt", paid, map[string]any{"url": link})
	if e.tg.count() != after {
		t.Fatal("receipt sent twice")
	}
	if strings.Contains(e.bot.redactAdminMessage("https://api.telegram.org/bot"+e2eBotToken+"/refund"), e2eBotToken) {
		t.Fatal("error disclosed token")
	}
}
