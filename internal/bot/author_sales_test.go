package bot

import (
	"fmt"
	"strings"
	"testing"
)

func TestAdminAuthorSaleCommandsAndPermissions(t *testing.T) {
	e := newE2EEnv(t)
	id := e.prodReg
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d price 1500", id), "ru")
	e.cmd(9520, fmt.Sprintf("/editproduct %d author @plane_author", id), "ru")
	if e.qStr(`SELECT author_telegram_url FROM products WHERE id=?`, id) != "" {
		t.Fatal("non-admin configured author")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d author @plane_author", id), "ru")
	if e.qStr(`SELECT author_telegram_url FROM products WHERE id=?`, id) != "https://t.me/plane_author" {
		t.Fatal("author not saved")
	}
	calls := e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d", id), "ru")
	if !strings.Contains(tgText(calls), "Покупка у автора: https://t.me/plane_author") || strings.Contains(tgText(calls), "%!") {
		t.Fatal("author settings missing")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d author https://t.me/+invite", id), "ru")
	if e.qStr(`SELECT author_telegram_url FROM products WHERE id=?`, id) != "https://t.me/plane_author" {
		t.Fatal("invalid author overwrote link")
	}
	e.cb(e2eAdminID, fmt.Sprintf("admin:openprice:%d", id), "ru")
	if e.qInt(`SELECT open_price FROM products WHERE id=?`, id) != 0 {
		t.Fatal("open price enabled for author")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d author -", id), "ru")
	if e.qStr(`SELECT author_telegram_url FROM products WHERE id=?`, id) != "" {
		t.Fatal("author mode not disabled")
	}
}
