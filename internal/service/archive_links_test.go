package service

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestArchiveDownloadLinksExpiryTamperingAndPath(t *testing.T) {
	now := time.Now()
	link := ArchiveDownloadURL("https://shop.example/app/?old=1", "secret", 42, 3, now)
	u, _ := url.Parse(link)
	if u.Path != "/api/archive-download" || strings.Contains(link, "secret") {
		t.Fatal("bad or secret-bearing URL")
	}
	if _, _, ok := ValidArchiveDownload("secret", u.Query(), now); !ok {
		t.Fatal("valid link rejected")
	}
	for _, later := range []time.Time{now.Add(24 * time.Hour), now.Add(48 * time.Hour)} {
		if _, _, ok := ValidArchiveDownload("secret", u.Query(), later); ok {
			t.Fatal("expired link accepted")
		}
	}
	if _, _, ok := ValidArchiveDownload("other-key", u.Query(), now); ok {
		t.Fatal("wrong key accepted")
	}
	for _, id := range []string{"local:../x", "local:" + strings.Repeat("a", 31), "telegram_file_id"} {
		if _, ok := LocalArchivePath("/files", id); ok {
			t.Fatal("unsafe file path", id)
		}
	}
}
