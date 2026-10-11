package bot

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/storage"
)

func TestMultipartArchiveDeliveryResumesAndReassembles(t *testing.T) {
	e := adminTestEnv(t)
	ctx := context.Background()
	const buyer int64 = 781
	uploadDigital(e, "old-archive")
	e.cmd(buyer, "/start", "ru")
	order := e.placeOrder(buyer, e.prodReg, "")
	e.payWithStars(buyer, order, "multipart-charge")
	e.bot.cfg.BotAdminOnly = true
	source, err := os.Create(filepath.Join(t.TempDir(), "plane.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	z := zip.NewWriter(source)
	entry, err := z.CreateHeader(&zip.FileHeader{Name: "model.stl", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.CopyN(entry, adminZeros{}, 80<<20); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	source.Seek(0, 0)
	expected := sha256.New()
	io.Copy(expected, source)
	source.Seek(0, 0)
	if _, err = e.bot.AdminUpload(ctx, e2eAdminID, e.prodReg, "archive", "plane.zip", source); err != nil {
		t.Fatal(err)
	}
	current, err := e.bot.archives.Latest(ctx, e.prodReg)
	if err != nil || current.Parts != 2 {
		t.Fatal("missing archive parts", err)
	}
	var mu sync.Mutex
	var names, ids, captions []string
	reassembled := sha256.New()
	failSecond := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test","username":"e2e_bot"}}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/sendDocument") {
			w.WriteHeader(500)
			return
		}
		r.ParseMultipartForm(1 << 20)
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		id := r.Form.Get("document")
		if f, h, err := r.FormFile("document"); err == nil {
			defer f.Close()
			if h.Size > storage.MaxTelegramArchiveUploadBytes {
				t.Error("part exceeds Telegram limit")
			}
			if strings.HasSuffix(h.Filename, ".002") && failSecond {
				failSecond = false
				io.WriteString(w, `{"ok":false,"error_code":500,"description":"temporary failure"}`)
				return
			}
			if _, err = io.Copy(reassembled, f); err != nil {
				t.Error(err)
			}
			names = append(names, h.Filename)
			id = fmt.Sprintf("cached-part-%d", len(names))
		}
		ids = append(ids, id)
		captions = append(captions, r.Form.Get("caption"))
		if r.Form.Get("reply_markup") != "" {
			t.Error("download link issued")
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": len(ids), "document": map[string]any{"file_id": id}}})
	}))
	defer srv.Close()
	api, err := tgbotapi.NewBotAPIWithAPIEndpoint(e2eBotToken, srv.URL+"/bot%s/%s")
	if err != nil {
		t.Fatal(err)
	}
	e.bot.api = api
	e.bot.ProcessDigitalDeliveries(ctx)
	if e.qStr("SELECT status FROM orders WHERE id=?", order) != "paid" || e.qInt("SELECT count(*) FROM digital_delivery_parts") != 1 {
		t.Fatal("partial delivery marked complete")
	}
	deliveryID := e.qInt("SELECT id FROM digital_deliveries WHERE order_id=?", order)
	if err = e.bot.archives.RequestOrderDownload(ctx, buyer, order, e.prodReg); err != nil {
		t.Fatal(err)
	}
	if e.qInt("SELECT count(*) FROM digital_delivery_parts") != 1 {
		t.Fatal("incomplete download lost progress")
	}
	e.bot.ProcessDigitalDeliveries(ctx)
	mu.Lock()
	if len(names) != 2 || names[0] != "plane.zip.001" || names[1] != "plane.zip.002" || !bytes.Equal(reassembled.Sum(nil), expected.Sum(nil)) {
		t.Error("multipart ZIP bytes or names differ")
	}
	if len(captions) != 2 || !strings.Contains(captions[0], "Часть 1 из 2") || !strings.Contains(captions[1], "7-Zip") {
		t.Error("missing reassembly instructions")
	}
	mu.Unlock()
	if e.qStr("SELECT status FROM orders WHERE id=?", order) != "delivered" {
		t.Fatal("complete archive not delivered")
	}
	if err = e.bot.archives.RequestOrderDownload(ctx, buyer, order, e.prodReg); err != nil {
		t.Fatal(err)
	}
	if e.qInt("SELECT count(*) FROM digital_delivery_parts") != 0 {
		t.Fatal("repeat download not reset")
	}
	e.bot.ProcessDigitalDeliveries(ctx)
	mu.Lock()
	if len(ids) != 4 || ids[2] != "cached-part-1" || ids[3] != "cached-part-2" || len(names) != 2 {
		t.Error("repeat download failed to resend all cached parts")
	}
	mu.Unlock()
	// Replacing a bundle with a single ZIP must not reuse any old part or progress.
	var small bytes.Buffer
	single := zip.NewWriter(&small)
	f, _ := single.Create("new.stl")
	io.WriteString(f, "updated")
	single.Close()
	if _, err = e.bot.AdminUpload(ctx, e2eAdminID, e.prodReg, "archive", "updated.zip", bytes.NewReader(small.Bytes())); err != nil {
		t.Fatal(err)
	}
	owned, err := e.bot.archives.Owned(ctx, buyer, deliveryID)
	if err != nil || owned.ArchiveID == current.ID {
		t.Fatal("prior purchase not updated", err)
	}
	if err = e.bot.archives.RequestOrderDownload(ctx, buyer, order, e.prodReg); err != nil {
		t.Fatal(err)
	}
	e.bot.ProcessDigitalDeliveries(ctx)
	mu.Lock()
	if len(names) != 3 || names[2] != "updated.zip" || len(ids) != 5 {
		t.Error("replacement did not send only new archive")
	}
	mu.Unlock()
	// Only a verified purchase can receive parts.
	if _, err = e.bot.archives.Owned(ctx, buyer+1, deliveryID); err == nil {
		t.Fatal("stranger accessed multipart archive")
	}
}

func TestLargeTelegramUploadRequestsAutomaticBrowserSplit(t *testing.T) {
	e := adminTestEnv(t)
	ctx := context.Background()
	uploadDigital(e, "existing")
	e.bot.cfg.AdminMiniAppOnly = true
	adminWriteTest(t, e, "archive-telegram", e.prodReg, map[string]any{})
	calls := e.do(tgbotapi.Update{Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: e2eAdminID, Type: "private"}, From: &tgbotapi.User{ID: e2eAdminID, LanguageCode: "ru"}, Document: &tgbotapi.Document{FileID: "too-large", FileName: "plane.zip", FileSize: 80_000_000}}})
	if !strings.Contains(tgText(calls), "автоматически разбит") || e.qInt("SELECT count(*) FROM digital_archives") != 1 {
		t.Fatal("large Telegram document replaced archive instead of directing to splitter")
	}
	if id, _ := e.bot.archives.PendingUpload(ctx, e2eAdminID, e2eAdminID); id != e.prodReg {
		t.Fatal("pending upload lost")
	}
}
