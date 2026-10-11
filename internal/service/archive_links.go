package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var localArchiveID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func LocalArchivePath(dir, fileID string) (string, bool) {
	id, ok := strings.CutPrefix(fileID, "local:")
	if !ok || !localArchiveID.MatchString(id) {
		return "", false
	}
	return filepath.Join(dir, id+".zip"), true
}

func archiveLinkSignature(token string, user, delivery, expires int64) string {
	key := hmac.New(sha256.New, []byte("PrintWingArchiveLinks"))
	key.Write([]byte(token))
	mac := hmac.New(sha256.New, key.Sum(nil))
	fmt.Fprintf(mac, "%d:%d:%d", user, delivery, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func ArchiveDownloadURL(appURL, token string, user, delivery int64, now time.Time) string {
	u, err := url.Parse(appURL)
	if err != nil {
		return ""
	}
	u.Path = "/api/archive-download"
	u.RawQuery = ""
	u.Fragment = ""
	expires := now.Add(24 * time.Hour).Unix()
	q := url.Values{"user": {strconv.FormatInt(user, 10)}, "delivery": {strconv.FormatInt(delivery, 10)}, "expires": {strconv.FormatInt(expires, 10)}, "signature": {archiveLinkSignature(token, user, delivery, expires)}}
	u.RawQuery = q.Encode()
	return u.String()
}

func ValidArchiveDownload(token string, q url.Values, now time.Time) (int64, int64, bool) {
	user, e1 := strconv.ParseInt(q.Get("user"), 10, 64)
	delivery, e2 := strconv.ParseInt(q.Get("delivery"), 10, 64)
	expires, e3 := strconv.ParseInt(q.Get("expires"), 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || user <= 0 || delivery <= 0 || expires <= now.Unix() || expires > now.Add(25*time.Hour).Unix() {
		return 0, 0, false
	}
	got, err := hex.DecodeString(q.Get("signature"))
	want, _ := hex.DecodeString(archiveLinkSignature(token, user, delivery, expires))
	return user, delivery, err == nil && hmac.Equal(got, want)
}
