package webapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"shop_bot/internal/service"
	"shop_bot/internal/storage"
)

type testAdminBackend struct{ calls int }

func (b *testAdminBackend) AdminRead(context.Context, int64, string, int64, url.Values) (any, error) {
	b.calls++
	return map[string]bool{"ok": true}, nil
}
func (b *testAdminBackend) AdminWrite(context.Context, int64, string, int64, json.RawMessage) (any, error) {
	b.calls++
	return map[string]bool{"ok": true}, nil
}
func (b *testAdminBackend) AdminUpload(context.Context, int64, int64, string, string, io.Reader) (any, error) {
	b.calls++
	return map[string]bool{"ok": true}, nil
}

func adminAuthFor(t *testing.T, uid int64, when time.Time) string {
	t.Helper()
	user, _ := json.Marshal(WebAppUser{ID: uid, FirstName: "Test"})
	return "tma " + SignInitData(testBotToken, url.Values{"auth_date": {strconv.FormatInt(when.Unix(), 10)}, "user": {string(user)}})
}

func TestAdminAPIAuthorizationCoversEverySurface(t *testing.T) {
	backend := &testAdminBackend{}
	s := New(Deps{Auth: NewAuthenticator(testBotToken, DefaultAuthTTL), AdminIDs: []int64{9001}, Admin: backend}, nil)
	for _, endpoint := range []struct{ method, path string }{{"GET", "/api/admin/bootstrap"}, {"GET", "/api/admin/products/1"}, {"POST", "/api/admin/products/1"}, {"POST", "/api/admin/confirm"}, {"POST", "/api/admin/products/1/upload/archive"}} {
		for _, credential := range []struct {
			header string
			want   int
		}{{"", 401}, {"tma user=%7B%22id%22%3A9001%7D&hash=bad", 401}, {adminAuthFor(t, 9002, time.Now()), 403}, {adminAuthFor(t, 9001, time.Now().Add(-2*time.Hour)), 401}, {adminAuthFor(t, 9001, time.Now()), 200}} {
			r := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{}`))
			r.Header.Set("Authorization", credential.header)
			w := httptest.NewRecorder()
			before := backend.calls
			s.Handler().ServeHTTP(w, r)
			if w.Code != credential.want {
				t.Fatalf("%s %s: %d %s", endpoint.method, endpoint.path, w.Code, w.Body.String())
			}
			if credential.want != 200 && backend.calls != before {
				t.Fatal("unauthorized request reached admin backend")
			}
		}
	}
	// An empty administrator list fails closed, including a genuinely signed user.
	s.deps.AdminIDs = nil
	r := httptest.NewRequest("GET", "/api/admin/bootstrap", nil)
	r.Header.Set("Authorization", adminAuthFor(t, 9001, time.Now()))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("empty admin list granted access")
	}
}

type testOwnedArchives struct {
	owner    int64
	delivery *storage.DigitalDelivery
	revoked  bool
}

func (a *testOwnedArchives) Owned(_ context.Context, user, id int64) (*storage.DigitalDelivery, error) {
	if a.revoked || user != a.owner || id != a.delivery.ID {
		return nil, storage.ErrNotFound
	}
	return a.delivery, nil
}
func (*testOwnedArchives) ForOrder(context.Context, int64, int64) ([]storage.DigitalDelivery, error) {
	return nil, nil
}
func (*testOwnedArchives) RequestOrderDownload(context.Context, int64, int64, int64) error {
	return nil
}

func TestLocalArchiveDownloadRequiresSignatureAndLiveEntitlement(t *testing.T) {
	dir := t.TempDir()
	fileID := "local:" + strings.Repeat("a", 32)
	path, _ := service.LocalArchivePath(dir, fileID)
	if err := os.WriteFile(path, []byte("original zip bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	owned := &testOwnedArchives{owner: 42, delivery: &storage.DigitalDelivery{ID: 3, FileID: fileID, FileName: "Самолёт.zip"}}
	s := New(Deps{ArchiveDownloadKey: "test-key", AdminMediaDir: dir, Archives: owned}, nil)
	link := service.ArchiveDownloadURL("https://example.test/app/", "test-key", 42, 3, time.Now())
	parsed, _ := url.Parse(link)
	get := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/archive-download?"+query, nil)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := get(parsed.RawQuery)
	if w.Code != 200 || w.Body.String() != "original zip bytes" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal(w.Code, w.Body.String())
	}
	q := parsed.Query()
	q.Set("user", "43")
	if get(q.Encode()).Code != 403 {
		t.Fatal("changed buyer accepted")
	}
	owned.revoked = true
	if get(parsed.RawQuery).Code != 403 {
		t.Fatal("refund/revocation did not close archive access")
	}
	owned.revoked = false
	owned.delivery.FileID = "local:../../secret"
	if get(parsed.RawQuery).Code != 404 {
		t.Fatal("archive path traversal accepted")
	}
	for _, name := range []string{filepath.Base(path), "../shop.db", "%2e%2e%2fshop.db"} {
		r := httptest.NewRequest(http.MethodGet, "/api/media/"+name, nil)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code == 200 {
			t.Fatal("private file served as image", name)
		}
	}
}
