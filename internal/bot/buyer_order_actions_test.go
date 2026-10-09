package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"shop_bot/internal/storage"
)

func buyerActionCallbacks(t *testing.T, calls []tgCall) []string {
	t.Helper()
	var result []string
	for _, call := range calls {
		if call.markup() == "" {
			continue
		}
		var kb struct {
			Rows [][]struct {
				Data string `json:"callback_data"`
			} `json:"inline_keyboard"`
		}
		if err := json.Unmarshal([]byte(call.markup()), &kb); err != nil {
			t.Fatal(err)
		}
		for _, row := range kb.Rows {
			for _, button := range row {
				if button.Data != "" {
					result = append(result, button.Data)
				}
			}
		}
	}
	return result
}
func buyerHasAction(t *testing.T, calls []tgCall, action string) bool {
	for _, value := range buyerActionCallbacks(t, calls) {
		if value == action {
			return true
		}
	}
	return false
}
func TestBuyerOrderHistoryResumeCancelAndPaymentGuards(t *testing.T) {
	e := newE2EEnv(t)
	ctx := context.Background()
	buyer := int64(1101)
	e.cmd(buyer, "/start", "ru")
	id := e.placeOrder(buyer, e.prodReg, "SAVE10")
	// A different current cart and changed catalog must not rewrite an order.
	e.cb(buyer, fmt.Sprintf("cart:add:%d", e.prodSub), "ru")
	cartBefore, err := e.bot.cart.Get(ctx, buyer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Conn().Exec(`UPDATE products SET name='Changed name',price_usd=99 WHERE id=?`, e.prodReg); err != nil {
		t.Fatal(err)
	}
	before, err := e.bot.order.GetOrder(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	history := e.cmd(buyer, "/orders", "ru")
	for _, prefix := range []string{"order:resume:", "order:cancelask:"} {
		if !buyerHasAction(t, history, fmt.Sprintf("%s%d", prefix, id)) {
			t.Fatalf("missing history action %s", prefix)
		}
	}
	for i := 0; i < 2; i++ {
		calls := e.cb(buyer, fmt.Sprintf("order:resume:%d", id), "ru")
		if !buyerHasAction(t, calls, fmt.Sprintf("pay:stars:%d", id)) || !buyerHasAction(t, calls, fmt.Sprintf("pay:crypto:%d", id)) {
			t.Fatal("resumed methods missing")
		}
		if !strings.Contains(tgText(calls), "Tee") || strings.Contains(tgText(calls), "Changed name") || !strings.Contains(tgText(calls), "9.00") {
			t.Fatalf("lost snapshot: %s", tgText(calls))
		}
	}
	calls := e.cb(buyer, fmt.Sprintf("pay:stars:%d", id), "ru")
	invoice := requireCall(t, calls, "sendInvoice", "")
	if invoice.Params.Get("payload") != strconv.FormatInt(id, 10) {
		t.Fatal("new order used")
	}
	var prices []struct {
		Amount int `json:"amount"`
	}
	if err := json.Unmarshal([]byte(invoice.Params.Get("prices")), &prices); err != nil {
		t.Fatal(err)
	}
	if len(prices) != 1 || prices[0].Amount != 450 {
		t.Fatalf("lost discount: %v", prices)
	}
	after, err := e.bot.order.GetOrder(ctx, id)
	before.CheckoutProvider = "stars"
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("resume changed order")
	}
	cartAfter, err := e.bot.cart.Get(ctx, buyer)
	if err != nil || !reflect.DeepEqual(cartBefore, cartAfter) {
		t.Fatal("resume changed cart")
	}
	if e.qInt("SELECT COUNT(*) FROM orders WHERE user_id=?", buyer) != 1 {
		t.Fatal("resume created order")
	}
	for _, prefix := range []string{"order:resume:", "order:cancelask:"} {
		if len(buyerActionCallbacks(t, e.cb(buyer+1, fmt.Sprintf("%s%d", prefix, id), "ru"))) != 0 {
			t.Fatal("foreign access")
		}
	}
	// Asking and declining cancel does not alter the pending order.
	calls = e.cb(buyer, fmt.Sprintf("order:cancelask:%d", id), "ru")
	if !buyerHasAction(t, calls, fmt.Sprintf("order:cancel:%d", id)) {
		t.Fatal("confirmation missing")
	}
	if e.qStr("SELECT status FROM orders WHERE id=?", id) != "pending" {
		t.Fatal("canceled before confirmation")
	}
	e.cb(buyer, "back:orders", "ru")
	e.cb(buyer, fmt.Sprintf("order:cancel:%d", id), "ru")
	if e.qStr("SELECT status FROM orders WHERE id=?", id) != "cancelled" {
		t.Fatal("cancel failed")
	}
	calls = e.cmd(buyer, "/orders", "ru")
	if buyerHasAction(t, calls, fmt.Sprintf("order:resume:%d", id)) {
		t.Fatal("cancelled action remains")
	}
	for _, state := range []string{"settled", "refunded", "partially_refunded", "needs_review", "cancelled"} {
		if _, err := e.db.Conn().Exec(`UPDATE orders SET status='pending',payment_state=? WHERE id=?`, state, id); err != nil {
			t.Fatal(err)
		}
		for _, prefix := range []string{"order:resume:", "order:cancelask:"} {
			calls = e.cb(buyer, fmt.Sprintf("%s%d", prefix, id), "ru")
			if len(buyerActionCallbacks(t, calls)) != 0 {
				t.Fatalf("unavailable %s action", state)
			}
		}
	}
}

func TestBuyerOrdersPagesFreeAndSubscriptionResume(t *testing.T) {
	e := newE2EEnv(t)
	ctx := context.Background()
	buyer := int64(1102)
	e.cmd(buyer, "/start", "ru")
	uploadDigital(e, "free-library-zip")
	store := storage.NewSQLOrderStore(e.db)
	var ids []int64
	for i := 0; i < 12; i++ {
		id, err := store.CreateOrder(ctx, &storage.Order{UserID: buyer, Status: "pending"}, []storage.OrderItem{{ProductID: e.prodReg, ProductName: "Free plane", Quantity: 1}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	calls := e.cmd(buyer, "/orders", "ru")
	if !buyerHasAction(t, calls, "orders:page:2") || buyerHasAction(t, calls, fmt.Sprintf("order:resume:%d", ids[0])) {
		t.Fatal("first page not bounded")
	}
	calls = e.cb(buyer, "orders:page:2", "ru")
	if !buyerHasAction(t, calls, "orders:page:1") || !buyerHasAction(t, calls, fmt.Sprintf("order:resume:%d", ids[0])) {
		t.Fatal("older order unavailable")
	}
	calls = e.cb(buyer, fmt.Sprintf("order:resume:%d", ids[0]), "ru")
	if !buyerHasAction(t, calls, fmt.Sprintf("order:free:%d", ids[0])) || buyerHasAction(t, calls, fmt.Sprintf("pay:stars:%d", ids[0])) {
		t.Fatal("zero order offered payment")
	}
	e.cb(buyer, fmt.Sprintf("order:free:%d", ids[0]), "ru")
	if e.qStr("SELECT payment_method FROM orders WHERE id=?", ids[0]) != "free" {
		t.Fatal("free order not fulfilled")
	}
	sub, err := store.CreateOrder(ctx, &storage.Order{UserID: buyer, Status: "pending", TotalUSD: 2, TotalStars: 100, SubscriptionProductID: e.prodSub, SubscriptionPeriodDays: 30}, []storage.OrderItem{{ProductID: e.prodSub, ProductName: "Saved subscription", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	calls = e.cb(buyer, fmt.Sprintf("order:resume:%d", sub), "ru")
	if !buyerHasAction(t, calls, fmt.Sprintf("pay:stars:%d", sub)) || buyerHasAction(t, calls, fmt.Sprintf("pay:crypto:%d", sub)) {
		t.Fatal("subscription methods wrong")
	}
	// Payment confirmed between asking and confirming cancellation wins.
	e.cb(buyer, fmt.Sprintf("order:cancelask:%d", sub), "ru")
	e.payWithStars(buyer, sub, "resume-sub-charge")
	e.cb(buyer, fmt.Sprintf("order:cancel:%d", sub), "ru")
	if e.qStr("SELECT status FROM orders WHERE id=?", sub) != "paid" {
		t.Fatal("payment overwritten by cancellation")
	}
}

func TestDigitalLibraryUniqueProductsLatestArchiveAndRemainingEntitlement(t *testing.T) {
	e := newE2EEnv(t)
	ctx := context.Background()
	buyer := int64(1103)
	e.cmd(buyer, "/start", "ru")
	uploadDigital(e, "shared-original-zip")
	paid := e.placeOrder(buyer, e.prodReg, "")
	e.payWithStars(buyer, paid, "unique-library-paid")
	products := storage.NewSQLProductStore(e.db)
	other, err := products.CreateProduct(ctx, &storage.Product{CategoryID: e.catID, Name: "Other plane", IsActive: true, Stock: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.bot.archives.Add(ctx, other, "shared-original-zip", "plane.zip", 80000000); err != nil {
		t.Fatal(err)
	}
	orders := storage.NewSQLOrderStore(e.db)
	grant := func(product int64) int64 {
		t.Helper()
		id, err := orders.CreateOrder(ctx, &storage.Order{UserID: buyer, Status: "pending"}, []storage.OrderItem{{ProductID: product, ProductName: "Saved plane", Quantity: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if err := orders.ConfirmFreeOrder(ctx, id, buyer); err != nil {
			t.Fatal(err)
		}
		return id
	}
	grant(other)
	var newest int64
	for i := 0; i < 55; i++ {
		newest = grant(e.prodReg)
	}
	if _, err := e.bot.archives.Add(ctx, e.prodReg, "latest-plane-zip", "latest.zip", 80000000); err != nil {
		t.Fatal(err)
	}
	files, err := e.bot.archives.Library(ctx, buyer)
	if err != nil || len(files) != 2 {
		t.Fatalf("duplicates or limit applied before grouping: %d %v", len(files), err)
	}
	if files[0].OrderID != newest || files[0].FileID != "latest-plane-zip" {
		t.Fatalf("latest entitlement: %+v", files[0])
	}
	calls := e.cmd(buyer, "/files", "ru")
	var downloadCount int
	for _, action := range buyerActionCallbacks(t, calls) {
		if strings.HasPrefix(action, "digital:download:") {
			downloadCount++
		}
	}
	if downloadCount != 2 {
		t.Fatalf("duplicate library buttons: %d", downloadCount)
	}
	calls = e.cb(buyer, fmt.Sprintf("digital:download:%d", files[0].ID), "ru")
	docs := documentCalls(calls)
	if len(docs) != 1 || docs[0].Params.Get("document") != "latest-plane-zip" {
		t.Fatal("library did not send latest archive")
	}
	if _, err := e.db.Conn().Exec(`UPDATE orders SET payment_state='refunded' WHERE id=?`, newest); err != nil {
		t.Fatal(err)
	}
	files, err = e.bot.archives.Library(ctx, buyer)
	if err != nil || len(files) != 2 || files[0].OrderID == newest {
		t.Fatal("refunded duplicate hid valid purchase")
	}
	if _, err := e.db.Conn().Exec(`UPDATE orders SET payment_state='refunded' WHERE user_id=? AND id IN (SELECT order_id FROM order_items WHERE product_id=?)`, buyer, e.prodReg); err != nil {
		t.Fatal(err)
	}
	files, err = e.bot.archives.Library(ctx, buyer)
	if err != nil || len(files) != 1 || files[0].ProductID != other {
		t.Fatal("revoked product remains")
	}
	foreign, err := e.bot.archives.Library(ctx, buyer+1)
	if err != nil || len(foreign) != 0 {
		t.Fatal("foreign library exposed")
	}
	if e.qInt(`SELECT COUNT(*) FROM digital_deliveries WHERE order_id IN (SELECT id FROM orders WHERE user_id=?)`, buyer) != 57 {
		t.Fatal("dedup deleted purchase delivery records")
	}
}
