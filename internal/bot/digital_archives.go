package bot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"shop_bot/internal/service"
	"shop_bot/internal/storage"
)

func (b *Bot) handleSetArchive(ctx context.Context, msg *tgbotapi.Message) {
	if msg.From == nil || !b.isAdmin(msg.From.ID) {
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(msg.CommandArguments()), 10, 64)
	if err != nil || id <= 0 {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "digital_admin_usage")))
		return
	}
	b.beginArchiveUpload(ctx, msg.Chat.ID, msg.From.ID, id, msg.From.LanguageCode)
}

func (b *Bot) beginArchiveUpload(ctx context.Context, chatID, userID, productID int64, lang string) {
	// Archives may only be attached from the administrator's private chat.
	if chatID != userID {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "digital_private_only")))
		return
	}
	if err := b.archives.BeginUpload(ctx, userID, chatID, productID); err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "digital_admin_product_error")))
		return
	}
	_ = b.fsm.DelAddProductState(ctx, userID)
	_ = b.fsm.DelPromoState(ctx, userID)
	b.send(tgbotapi.NewMessage(chatID, fmt.Sprintf(b.t(lang, "digital_admin_prompt"), productID)))
}

func (b *Bot) handleArchiveUpload(ctx context.Context, msg *tgbotapi.Message) bool {
	if b.archives == nil || msg.From == nil || msg.Chat == nil || msg.Chat.ID != msg.From.ID || !b.isAdmin(msg.From.ID) {
		return false
	}
	id, err := b.archives.PendingUpload(ctx, msg.From.ID, msg.Chat.ID)
	if err != nil {
		b.loggerFor(ctx).Error("read archive upload state")
		return true
	}
	if id == 0 {
		return false
	}
	if msg.Command() == "cancel" {
		_ = b.archives.CancelUpload(ctx, msg.From.ID)
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "admin_cancelled")))
		return true
	}
	if msg.Command() != "" {
		return false
	}
	if msg.Document == nil {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "digital_admin_zip_only")))
		return true
	}
	d := msg.Document
	if int64(d.FileSize) > storage.MaxTelegramArchiveUploadBytes {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, "ZIP больше 50 МБ загрузите через админку: Товар → Архив → Выберите ZIP-архив. Он будет автоматически разбит на части, и покупатель получит все части. Текущий архив сохранён."))
		return true
	}
	version, err := b.archives.Add(ctx, id, d.FileID, d.FileName, int64(d.FileSize))
	if err != nil {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "digital_admin_zip_only")))
		return true
	}
	_ = b.archives.CancelUpload(ctx, msg.From.ID)
	if cache, ok := b.products.(interface {
		Invalidate(context.Context, ...int64)
	}); ok {
		cache.Invalidate(ctx, id)
	}
	b.send(tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf(b.t(msg.From.LanguageCode, "digital_admin_saved"), id, version)))
	return true
}

type deliveryHTTPClient struct {
	base tgbotapi.HTTPClient
	ctx  context.Context
}

func (c deliveryHTTPClient) Do(r *http.Request) (*http.Response, error) {
	return c.base.Do(r.Clone(c.ctx))
}

func (b *Bot) sendDigitalDocument(ctx context.Context, d *storage.DigitalDelivery, lang string) (int, error) {
	parts, err := b.archives.Parts(ctx, d)
	if err != nil {
		return 0, err
	}
	message := 0
	for _, p := range parts {
		previous, err := b.archives.PreparePart(ctx, d, p.Number)
		if err != nil {
			return message, err
		}
		if previous > 0 {
			message = previous
			continue
		}
		message, err = b.sendArchivePart(ctx, d, p, len(parts), lang)
		if err != nil {
			return message, err
		}
		if err = b.archives.FinishPart(ctx, d, p.Number, message); err != nil {
			return message, err
		}
	}
	return message, nil
}

func (b *Bot) sendArchivePart(ctx context.Context, d *storage.DigitalDelivery, p storage.ArchivePart, total int, lang string) (int, error) {
	timeout := 30 * time.Second
	var document tgbotapi.RequestFileData = tgbotapi.FileID(p.FileID)
	if path, local := service.LocalArchivePath(b.AdminMediaDirectory(), p.FileID); local {
		file, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		defer file.Close()
		stat, err := file.Stat()
		if err != nil {
			return 0, err
		}
		size := p.SizeBytes
		if size == 0 {
			size = stat.Size()
		}
		if !stat.Mode().IsRegular() || size <= 0 || size > storage.MaxTelegramArchiveUploadBytes || p.OffsetBytes < 0 || p.OffsetBytes > stat.Size()-size {
			return 0, storage.ErrDigitalArchiveNotReady
		}
		document = tgbotapi.FileReader{Name: p.FileName, Reader: io.NewSectionReader(file, p.OffsetBytes, size)}
		timeout = 90 * time.Second
	} else if strings.HasPrefix(p.FileID, "local:") {
		return 0, storage.ErrDigitalArchiveNotReady
	}
	sendCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	api := *b.api
	api.Client = sanitizedTelegramClient{client: deliveryHTTPClient{b.api.Client, sendCtx}}
	doc := tgbotapi.NewDocument(d.UserID, document)
	name := []rune(d.ProductName)
	if len(name) > 300 {
		name = name[:300]
	}
	doc.Caption = fmt.Sprintf(b.t(lang, "digital_delivery_caption"), string(name), d.ArchiveID, d.OrderID)
	if total > 1 {
		if lang == "ru" {
			doc.Caption += fmt.Sprintf("\nЧасть %d из %d. Сохраните все части в одну папку и откройте файл .001 через 7-Zip.", p.Number, total)
		} else {
			doc.Caption += fmt.Sprintf("\nPart %d of %d. Save all parts in one folder and open the .001 file with 7-Zip.", p.Number, total)
		}
	}
	if !b.adminOnly() {
		doc.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.t(lang, "digital_download_again"), fmt.Sprintf("digital:download:%d", d.ID))))
	}
	msg, err := api.Send(doc)
	if err == nil && strings.HasPrefix(p.FileID, "local:") && msg.Document != nil && msg.Document.FileID != "" {
		var cacheErr error
		if total > 1 {
			cacheErr = b.archives.RememberPartFile(ctx, d.ArchiveID, p, msg.Document.FileID)
		} else {
			cacheErr = b.archives.RememberTelegramFile(ctx, d.ArchiveID, p.FileID, msg.Document.FileID)
		}
		if cacheErr != nil {
			b.logger.Warn("archive Telegram file cache failed", "archive_id", d.ArchiveID)
		}
	}
	return msg.MessageID, err
}

// The durable queue is populated when the order is created. Claim only admits
// settled orders, so delivery survives a crash immediately after payment.
func (b *Bot) ProcessDigitalDeliveries(ctx context.Context) {
	if b.archives == nil {
		return
	}
	for i := 0; i < 10 && ctx.Err() == nil; i++ {
		d, err := b.archives.Claim(ctx)
		if err != nil {
			b.logger.Error("digital delivery claim failed")
			return
		}
		if d == nil {
			return
		}
		messageID, sendErr := b.sendDigitalDocument(ctx, d, b.userLang(ctx, d.UserID))
		// Save the result even during shutdown; an abandoned lease otherwise
		// becomes retryable on restart. Telegram has no sendDocument idempotency key.
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = b.archives.Finish(finishCtx, d, messageID, sendErr == nil)
		cancel()
		if err != nil {
			b.logger.Error("digital delivery result failed", "delivery_id", d.ID)
		}
		if sendErr != nil {
			b.logger.Warn("digital delivery will retry", "delivery_id", d.ID)
		}
	}
}

func (b *Bot) RunDigitalDeliveries(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		b.ProcessDigitalDeliveries(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) sendDigitalLibrary(ctx context.Context, chatID, userID int64, lang string) {
	if chatID != userID {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "digital_private_only")))
		return
	}
	items, err := b.archives.Library(ctx, userID)
	if err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "error_load_orders")))
		return
	}
	if len(items) == 0 {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "digital_library_empty")))
		return
	}
	kb := StyledKeyboard{}
	for _, d := range items {
		name := []rune(d.ProductName)
		if len(name) > 35 {
			name = name[:35]
		}
		kb = append(kb, []StyledButton{Btn(fmt.Sprintf("📦 %s · v%d", string(name), d.ArchiveID), fmt.Sprintf("digital:download:%d", d.ID))})
	}
	b.sendOrEditStyled(chatID, 0, b.t(lang, "digital_library_title"), "", kb)
}

func (b *Bot) onDigitalDownload(ctx context.Context, cbID string, chatID, userID int64, data, lang string) {
	id, err := parseIDFromCallback(data, "digital:download:")
	if err != nil || chatID != userID {
		b.alert(cbID, b.t(lang, "digital_no_access"))
		return
	}
	d, err := b.archives.Owned(ctx, userID, id)
	if err != nil {
		b.alert(cbID, b.t(lang, "digital_no_access"))
		return
	}
	b.ack(cbID)
	if _, err := b.sendDigitalDocument(ctx, d, lang); err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "digital_send_error")))
	}
}
