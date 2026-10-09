package bot

import (
	"context"
	"fmt"
	"shop_bot/internal/config"
	"strings"
	"testing"
)

func TestAdminProductModificationLinkDetachAndAuthorization(t *testing.T) {
	e := newE2EEnvWithConfig(t, func(c *config.Config) { c.BotAdminOnly = true })
	command := fmt.Sprintf("/editproduct %d parent %d", e.prodSub, e.prodReg)
	e.cmd(777, command, "ru")
	parent, _ := e.bot.productGroups.Parent(context.Background(), e.prodSub)
	if parent != 0 {
		t.Fatal("unauthorized link")
	}
	e.cmd(e2eAdminID, command, "ru")
	parent, _ = e.bot.productGroups.Parent(context.Background(), e.prodSub)
	if parent != e.prodReg {
		t.Fatal("parent command failed")
	}
	text := tgText(e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d", e.prodSub), "ru"))
	if !strings.Contains(text, fmt.Sprintf("Основной товар (ID): %d", e.prodReg)) {
		t.Fatalf("parent not shown: %s", text)
	}
	text = tgText(e.cmd(e2eAdminID, "/listproduct", "ru"))
	if !strings.Contains(text, fmt.Sprintf("основной #%d", e.prodReg)) {
		t.Fatal("parent not in admin list")
	}
	for _, value := range []string{"-1", "oops", fmt.Sprint(e.prodSub), "999999"} {
		e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d parent %s", e.prodSub, value), "ru")
		parent, _ = e.bot.productGroups.Parent(context.Background(), e.prodSub)
		if parent != e.prodReg {
			t.Fatal("invalid link changed group")
		}
	}
	e.cmd(e2eAdminID, fmt.Sprintf("/editproduct %d parent 0", e.prodSub), "ru")
	parent, _ = e.bot.productGroups.Parent(context.Background(), e.prodSub)
	if parent != 0 {
		t.Fatal("detach failed")
	}
}
