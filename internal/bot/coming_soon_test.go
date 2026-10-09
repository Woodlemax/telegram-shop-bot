package bot

import (
	"fmt"
	"strings"
	"testing"
)

func TestAdminComingSoonCommandButtonAndAccessGuard(t *testing.T) {
	e := newE2EEnv(t)
	id := e.prodReg
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d stock 0", id), "ru")
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d comingsoon true", id), "ru")
	if e.qInt(`SELECT coming_soon FROM products WHERE id=?`, id) != 1 {
		t.Fatal("command did not enable preview")
	}
	calls := e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d", id), "ru")
	if !tgHasCall(calls, "Выключить «Скоро ожидается»") || strings.Contains(tgText(calls), "%!") {
		t.Fatal("attribute not shown in admin")
	}
	e.cb(9520, fmt.Sprintf("admin:comingsoon:%d", id), "ru")
	if e.qInt(`SELECT coming_soon FROM products WHERE id=?`, id) != 1 {
		t.Fatal("non-admin changed attribute")
	}
	e.cb(e2eAdminID, fmt.Sprintf("admin:comingsoon:%d", id), "ru")
	if e.qInt(`SELECT coming_soon FROM products WHERE id=?`, id) != 0 {
		t.Fatal("button did not disable attribute")
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d comingsoon invalid", id), "ru")
	if e.qInt(`SELECT coming_soon FROM products WHERE id=?`, id) != 0 {
		t.Fatal("invalid command changed attribute")
	}
}
