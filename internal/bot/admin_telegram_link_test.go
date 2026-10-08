package bot

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAdminProductTelegramOptionalButtonEditing(t *testing.T) {
	e := newE2EEnv(t)
	ctx := context.Background()
	id := e.prodReg
	link := func() string { return e.qStr(`SELECT telegram_url FROM products WHERE id=?`, id) }
	keyboardURL := func() string {
		p, err := e.bot.catalog.GetProduct(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range e.bot.productKeyboard(p, false, 0, "ru") {
			for _, btn := range row {
				if btn.URL != "" {
					return btn.URL
				}
			}
		}
		return ""
	}
	if keyboardURL() != "" {
		t.Fatal("optional button shown with no link")
	}
	calls := e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d", id), "ru")
	if !buyerHasAction(t, calls, fmt.Sprintf("admin:telegram:edit:%d", id)) {
		t.Fatal("admin editor missing")
	}
	e.cb(1707, fmt.Sprintf("admin:telegram:edit:%d", id), "ru")
	e.text(1707, "@PlaneModels", "ru")
	e.cmd(1707, fmt.Sprintf("/editproduct %d telegram @PlaneModels", id), "ru")
	if link() != "" {
		t.Fatal("non-admin set link")
	}
	e.cb(e2eAdminID, fmt.Sprintf("admin:telegram:edit:%d", id), "ru")
	e.text(e2eAdminID, "https://evil.example/", "ru")
	if link() != "" {
		t.Fatal("invalid link stored")
	}
	e.text(e2eAdminID, "@PlaneModels", "ru")
	if link() != "https://t.me/PlaneModels" || keyboardURL() != link() {
		t.Fatal("bot link not saved/rendered")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d description New description", id), "ru")
	if link() != "https://t.me/PlaneModels" {
		t.Fatal("unrelated edit erased link")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d telegram https://t.me/+Invite_123", id), "ru")
	if keyboardURL() != "https://t.me/+Invite_123" {
		t.Fatal("invite replacement failed")
	}
	calls = e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d", id), "ru")
	if !buyerHasAction(t, calls, fmt.Sprintf("admin:telegram:remove:%d", id)) {
		t.Fatal("remove button missing")
	}
	for _, cancel := range []string{"command", "button", "expired", "other"} {
		e.cb(e2eAdminID, fmt.Sprintf("admin:telegram:edit:%d", id), "ru")
		switch cancel {
		case "command":
			e.cmd(e2eAdminID, "/cancel", "ru")
		case "button":
			e.cb(e2eAdminID, fmt.Sprintf("admin:telegram:cancel:%d", id), "ru")
		case "expired":
			e.bot.telegramInput.Store(e2eAdminID, productTelegramState{id, time.Now().Add(-time.Second)})
		case "other":
			e.cmd(e2eAdminID, "/catalog", "ru")
		}
		e.text(e2eAdminID, "@DifferentModels", "ru")
		if link() != "https://t.me/+Invite_123" {
			t.Fatalf("cancel failed: %s", cancel)
		}
	}
	e.cb(e2eAdminID, fmt.Sprintf("admin:telegram:remove:%d", id), "ru")
	if link() != "" || keyboardURL() != "" {
		t.Fatal("remove did not hide button")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d telegram @PlaneModels", id), "ru")
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d telegram -", id), "ru")
	if link() != "" {
		t.Fatal("command did not clear link")
	}
	// Starting a link dialog replaces a pending RUB-rate input.
	e.cb(e2eAdminID, "admin:rubrate:edit", "ru")
	e.cb(e2eAdminID, fmt.Sprintf("admin:telegram:edit:%d", id), "ru")
	e.text(e2eAdminID, "@PlaneModels", "ru")
	if link() != "https://t.me/PlaneModels" {
		t.Fatal("rate dialog intercepted link")
	}
	// Stored data from outside the administrator UI is never exposed as an unsafe URL button.
	p, err := e.bot.catalog.GetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.TelegramURL = "javascript:alert(1)"
	for _, row := range e.bot.productKeyboard(p, false, 0, "ru") {
		for _, btn := range row {
			if btn.URL != "" {
				t.Fatal("unsafe URL rendered")
			}
		}
	}
}
