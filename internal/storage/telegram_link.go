package storage

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var ErrInvalidTelegramURL = errors.New("invalid Telegram group or channel link")
var telegramUsername = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)
var telegramInvite = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// NormalizeTelegramURL accepts public names and HTTPS group/channel invitation links.
// No remote requests are needed; an empty string means no optional button.
func NormalizeTelegramURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "@") {
		raw = "https://t.me/" + strings.TrimPrefix(raw, "@")
	} else if strings.HasPrefix(raw, "t.me/") || strings.HasPrefix(raw, "telegram.me/") {
		raw = "https://" + raw
	}
	if len(raw) > 256 {
		return "", ErrInvalidTelegramURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return "", ErrInvalidTelegramURL
	}
	host := strings.ToLower(u.Host)
	if host != "t.me" && host != "telegram.me" {
		return "", ErrInvalidTelegramURL
	}
	path := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/")
	valid := telegramUsername.MatchString(path) && !strings.EqualFold(path, "joinchat")
	if strings.HasPrefix(path, "+") {
		valid = telegramInvite.MatchString(path[1:])
	}
	if strings.HasPrefix(path, "joinchat/") {
		valid = telegramInvite.MatchString(strings.TrimPrefix(path, "joinchat/"))
	}
	if !valid || u.RawPath != "" {
		return "", ErrInvalidTelegramURL
	}
	return "https://t.me/" + path, nil
}

var ErrInvalidAuthorSale = errors.New("author sale requires a personal Telegram username, fixed RUB price and no subscription")

// NormalizeAuthorTelegramURL accepts public usernames only, not channel invitations or posts.
func NormalizeAuthorTelegramURL(raw string) (string, error) {
	link, err := NormalizeTelegramURL(raw)
	if err != nil {
		return "", ErrInvalidAuthorSale
	}
	if link != "" && !telegramUsername.MatchString(strings.TrimPrefix(link, "https://t.me/")) {
		return "", ErrInvalidAuthorSale
	}
	return link, nil
}

func validateProduct(p *Product) error {
	link, err := NormalizeTelegramURL(p.TelegramURL)
	if err != nil {
		return err
	}
	author, err := NormalizeAuthorTelegramURL(p.AuthorTelegramURL)
	if err != nil {
		return err
	}
	p.AuthorTelegramURL = author
	if p.IsAuthorSale() {
		if p.PriceRUB == nil || p.OpenPrice || p.SubPeriodDays > 0 {
			return ErrInvalidAuthorSale
		}
		p.PriceUSD = 0
		p.PriceStars = 0
	}
	if err := validateRUBProduct(p); err != nil {
		return err
	}
	p.TelegramURL = link
	return nil
}
