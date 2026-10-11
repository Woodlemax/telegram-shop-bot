package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"shop_bot/internal/storage"
	"strings"
	"testing"
)

func TestAdminAppFileReferenceAuthorizationAndPrivacy(t *testing.T) {
	e := adminTestEnv(t)
	ctx := context.Background()
	id := e.prodReg
	if _, err := e.bot.AdminUpload(ctx, 55, id, "reference", "CAN_1.STL", strings.NewReader("original")); err == nil {
		t.Fatal("buyer changed reference")
	}
	result, err := e.bot.AdminUpload(ctx, e2eAdminID, id, "reference", "CAN_1.STL", strings.NewReader("original"))
	if err != nil {
		t.Fatal(err)
	}
	ref := result.(*storage.ProductFileRequirement)
	raw, _ := json.Marshal(result)
	sum := sha256.Sum256([]byte("original"))
	if strings.Contains(string(raw), hex.EncodeToString(sum[:])) {
		t.Fatal("admin API exposed reference hash")
	}
	if files, err := os.ReadDir(e.bot.AdminMediaDirectory()); err == nil && len(files) != 0 {
		t.Fatal("reference file persisted in public media")
	}
	result = adminWriteTest(t, e, "file-check", id, map[string]any{"enabled": true, "description": "Attach original CAN_1.STL", "revision": ref.Revision})
	ref = result.(*storage.ProductFileRequirement)
	if _, err = e.bot.AdminWrite(ctx, 55, "file-check", id, adminJSON(t, map[string]any{"enabled": false, "description": "", "revision": ref.Revision})); err == nil {
		t.Fatal("buyer disabled check")
	}
	if _, err = e.bot.AdminRead(ctx, 55, "file-check", id, url.Values{}); err == nil {
		t.Fatal("buyer read admin requirement")
	}
	card, _ := e.bot.adminProduct(ctx, id)
	data := card.(map[string]any)
	p := data["product"].(*storage.Product)
	p.AuthorTelegramURL = "https://author.example/buy"
	_, err = e.bot.AdminWrite(ctx, e2eAdminID, "products", id, adminJSON(t, map[string]any{"product": p, "revision": data["revision"], "parent_id": 0}))
	if !errors.Is(err, storage.ErrInvalidFileRequirement) {
		t.Fatal("protected offer became an external purchase", err)
	}
	if _, err = e.bot.AdminUpload(ctx, e2eAdminID, id, "reference", "empty.stl", strings.NewReader("")); err == nil {
		t.Fatal("empty reference accepted")
	}
}
