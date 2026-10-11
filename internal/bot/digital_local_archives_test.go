package bot

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestLocalArchiveDocumentsPreserveBytesAndReuseTelegramFile(t *testing.T) {
	e := adminTestEnv(t)
	ctx := context.Background()
	const buyer int64 = 781
	uploadDigital(e, "old-telegram-archive")
	e.cmd(buyer, "/start", "ru")
	order := e.placeOrder(buyer, e.prodReg, "")
	e.payWithStars(buyer, order, "local-document-charge")
	e.bot.cfg.BotAdminOnly = true
	zipBytes := func(content string) []byte {
		var body bytes.Buffer
		z := zip.NewWriter(&body)
		f, err := z.Create("model.stl")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(f, content)
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		return body.Bytes()
	}
	first := zipBytes("first model bytes")
	if _, err := e.bot.AdminUpload(ctx, e2eAdminID, e.prodReg, "archive", "original-name.zip", bytes.NewReader(first)); err != nil {
		t.Fatal(err)
	}
	type sentDocument struct {
		body                   []byte
		name, id, chat, markup string
	}
	var storedCalls []sentDocument
	var mu sync.Mutex
	snapshot := func() []sentDocument {
		mu.Lock()
		defer mu.Unlock()
		return append([]sentDocument(nil), storedCalls...)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"id": 1, "is_bot": true, "first_name": "Test", "username": "e2e_bot"}})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/sendDocument") {
			w.WriteHeader(500)
			return
		}
		_ = r.ParseMultipartForm(1 << 20)
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		_ = r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		call := sentDocument{id: r.Form.Get("document"), chat: r.Form.Get("chat_id"), markup: r.Form.Get("reply_markup")}
		if f, h, err := r.FormFile("document"); err == nil {
			call.name = h.Filename
			call.body, _ = io.ReadAll(f)
			_ = f.Close()
			call.id = fmt.Sprintf("registered-archive-%d", len(storedCalls)+1)
		}
		storedCalls = append(storedCalls, call)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": len(storedCalls), "document": map[string]any{"file_id": call.id, "file_name": call.name}}})
	}))
	defer srv.Close()
	api, err := tgbotapi.NewBotAPIWithAPIEndpoint(e2eBotToken, srv.URL+"/bot%s/%s")
	if err != nil {
		t.Fatal(err)
	}
	e.bot.api = api
	e.bot.ProcessDigitalDeliveries(ctx)
	calls := snapshot()
	if len(calls) != 1 || !bytes.Equal(calls[0].body, first) || calls[0].name != "original-name.zip" || calls[0].chat != "781" || calls[0].markup != "" {
		t.Fatal("wrong document upload", calls)
	}
	archiveID := e.qInt("SELECT max(id) FROM digital_archives")
	if e.qStr("SELECT file_id FROM digital_archives WHERE id=?", archiveID) != calls[0].id {
		t.Fatal("Telegram ID not saved")
	}
	if e.qStr("SELECT status FROM orders WHERE id=?", order) != "delivered" {
		t.Fatal("delivery not recorded")
	}
	if err = e.bot.archives.RequestOrderDownload(ctx, buyer, order, e.prodReg); err != nil {
		t.Fatal(err)
	}
	e.bot.ProcessDigitalDeliveries(ctx)
	calls = snapshot()
	if len(calls) != 2 || len(calls[1].body) != 0 || calls[1].id != calls[0].id {
		t.Fatal("repeat did not reuse Telegram file")
	}
	second := zipBytes("new model bytes")
	if _, err = e.bot.AdminUpload(ctx, e2eAdminID, e.prodReg, "archive", "new-name.zip", bytes.NewReader(second)); err != nil {
		t.Fatal(err)
	}
	if err = e.bot.archives.RequestOrderDownload(ctx, buyer, order, e.prodReg); err != nil {
		t.Fatal(err)
	}
	e.bot.ProcessDigitalDeliveries(ctx)
	calls = snapshot()
	if len(calls) != 3 || !bytes.Equal(calls[2].body, second) || calls[2].name != "new-name.zip" || calls[2].id == calls[0].id {
		t.Fatal("archive replacement reused old bytes")
	}
	if len(calls[2].markup) > 0 {
		t.Fatal("download link issued")
	}
}
