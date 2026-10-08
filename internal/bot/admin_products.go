package bot

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"shop_bot/internal/storage"
)

func (b *Bot) handleAddProduct(ctx context.Context, msg *tgbotapi.Message) {
	if !b.isAdmin(msg.From.ID) {
		return
	}
	_ = b.fsm.SetAddProductState(ctx, msg.From.ID, &storage.AddProductState{Step: storage.StepName, CreatedAt: time.Now()}, 30*time.Minute)
	b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "admin_add_product_name")))
}

func (b *Bot) handleAddProductStep(ctx context.Context, msg *tgbotapi.Message) bool {
	if msg.Text == "/cancel" {
		state, _ := b.fsm.GetAddProductState(ctx, msg.From.ID)
		_ = b.fsm.DelAddProductState(ctx, msg.From.ID)
		if state != nil {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(msg.From.LanguageCode, "admin_cancelled")))
			return true
		}
		return false
	}

	state, _ := b.fsm.GetAddProductState(ctx, msg.From.ID)
	if state == nil {
		return false
	}

	chatID := msg.Chat.ID
	lang := msg.From.LanguageCode
	switch state.Step {
	case storage.StepName:
		state.Name = msg.Text
		state.Step = storage.StepDescription
		_ = b.fsm.SetAddProductState(ctx, msg.From.ID, state, 30*time.Minute)
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_add_product_description")))
	case storage.StepDescription:
		state.Description = msg.Text
		state.Step = storage.StepPriceUSD
		_ = b.fsm.SetAddProductState(ctx, msg.From.ID, state, 30*time.Minute)
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_add_product_price")))
	case storage.StepPriceUSD:
		p, err := strconv.ParseFloat(strings.TrimSpace(msg.Text), 64)
		if err != nil || p < 0 || math.IsNaN(p) || math.IsInf(p, 0) {
			b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_invalid_price")))
			return true
		}
		state.PriceRUB = &p
		state.Step = storage.StepStock
		_ = b.fsm.SetAddProductState(ctx, msg.From.ID, state, 30*time.Minute)
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_add_product_stock")))
	case storage.StepStock:
		s, _ := strconv.Atoi(msg.Text)
		state.Stock = s
		state.Step = storage.StepPhoto
		_ = b.fsm.SetAddProductState(ctx, msg.From.ID, state, 30*time.Minute)
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_photo_prompt")))
	case storage.StepPhoto:
		b.handleWizardPhotoStep(ctx, msg, state)
	case storage.StepCategory:
		id, _ := strconv.ParseInt(msg.Text, 10, 64)
		state.CategoryID = id
		state.Step = storage.StepSubType
		_ = b.fsm.SetAddProductState(ctx, msg.From.ID, state, 30*time.Minute)
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_sub_type_prompt")))
	case storage.StepSubType:
		// "2" = 30-day Stars subscription, anything else = regular product.
		if strings.TrimSpace(msg.Text) == "2" {
			state.SubPeriodDays = 30
		}
		_ = b.fsm.SetAddProductState(ctx, msg.From.ID, state, 30*time.Minute)
		b.finishAddProduct(ctx, chatID, msg.From.ID, state.CategoryID, lang)
	}
	return true
}

func (b *Bot) finishAddProduct(ctx context.Context, chatID, userID, categoryID int64, lang string) {
	state, _ := b.fsm.GetAddProductState(ctx, userID)
	_ = b.fsm.DelAddProductState(ctx, userID)
	if state == nil {
		return
	}
	cover := ""
	if len(state.Photos) > 0 {
		cover = state.Photos[0]
	}
	p := &storage.Product{CategoryID: categoryID, Name: state.Name, Description: state.Description, PriceUSD: state.PriceUSD, PriceRUB: state.PriceRUB, Stock: state.Stock, PhotoURL: cover, IsActive: true, SubPeriodDays: state.SubPeriodDays}
	id, err := b.products.CreateProduct(ctx, p)
	if err != nil {
		b.loggerFor(ctx).Error("create product", "error", err)
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_product_create_failed")))
		return
	}
	for _, fileID := range state.Photos {
		if err := b.photos.Add(ctx, id, fileID); err != nil {
			b.loggerFor(ctx).Error("add product photo", "product_id", id, "error", err)
		}
	}
	b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_product_created")))
}

func (b *Bot) sendAdminProductDetails(chatID int64, product *storage.Product, lang string) {
	stockText := strconv.Itoa(product.Stock)
	if product.InfiniteStock {
		stockText = "∞"
	}
	toggleLabel := b.t(lang, "admin_btn_stock_off")
	if product.Stock <= 0 {
		toggleLabel = b.t(lang, "admin_btn_stock_on")
	}

	text := fmt.Sprintf(
		b.t(lang, "admin_product_details"),
		product.ID, product.Name, product.Description, productAmount(product), stockText, product.CategoryID, product.IsActive,
		product.ID, product.ID, product.ID, product.ID, product.ID, product.ID,
	)
	text = currencyText(text, product.PriceRUB != nil)
	settingState := func(on bool) string {
		if on {
			return "✅"
		}
		return "❌"
	}
	text += "\n" + fmt.Sprintf(b.t(lang, "admin_quantity_settings"), settingState(product.InfiniteStock), settingState(product.SingleInCart))
	infiniteLabel := b.t(lang, "admin_infinite_stock_on")
	if product.InfiniteStock {
		infiniteLabel = b.t(lang, "admin_infinite_stock_off")
	}
	singleLabel := b.t(lang, "admin_single_in_cart_on")
	if product.SingleInCart {
		singleLabel = b.t(lang, "admin_single_in_cart_off")
	}
	openLabel := b.t(lang, "open_price_admin_on")
	if product.OpenPrice {
		openLabel = b.t(lang, "open_price_admin_off")
		text += "\n" + b.t(lang, "open_price_hint")
	}

	linkText := product.TelegramURL
	if linkText == "" {
		linkText = b.t(lang, "admin_telegram_empty")
	}
	text += "\n" + fmt.Sprintf(b.t(lang, "admin_telegram_current"), linkText)
	text += "\n" + fmt.Sprintf(b.t(lang, "admin_telegram_usage"), product.ID, product.ID)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(infiniteLabel, fmt.Sprintf("admin:infinitestock:%d", product.ID))),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(singleLabel, fmt.Sprintf("admin:singleincart:%d", product.ID))),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(openLabel, fmt.Sprintf("admin:openprice:%d", product.ID))),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(toggleLabel, fmt.Sprintf("admin:togglestock:%d", product.ID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.t(lang, "admin_photo_btn"), fmt.Sprintf("admin:photos:%d", product.ID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.t(lang, "digital_admin_upload"), fmt.Sprintf("admin:archive:%d", product.ID)),
		),
	)
	if product.InfiniteStock {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard[:3], keyboard.InlineKeyboard[4:]...)
	}

	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.t(lang, "admin_telegram_edit"), fmt.Sprintf("admin:telegram:edit:%d", product.ID))))
	if product.TelegramURL != "" {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.t(lang, "admin_telegram_remove"), fmt.Sprintf("admin:telegram:remove:%d", product.ID))))
	}
	reply := tgbotapi.NewMessage(chatID, text)
	reply.ReplyMarkup = keyboard
	b.send(reply)
}

func (b *Bot) handleEditProduct(ctx context.Context, msg *tgbotapi.Message) {
	if !b.isAdmin(msg.From.ID) {
		return
	}
	lang := msg.From.LanguageCode
	id, err := strconv.ParseInt(strings.TrimSpace(msg.CommandArguments()), 10, 64)
	if err != nil {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_usage_editproduct")))
		return
	}

	product, err := b.products.GetProduct(ctx, id)
	if err != nil {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_product_not_found")))
		return
	}

	b.sendAdminProductDetails(msg.Chat.ID, product, lang)
}

func (b *Bot) handleEditProductField(ctx context.Context, msg *tgbotapi.Message, prodID int64, field, value string) {
	if !b.isAdmin(msg.From.ID) {
		return
	}
	lang := msg.From.LanguageCode
	if strings.TrimSpace(value) == "" {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_field_value_required")))
		return
	}

	product, err := b.products.GetProduct(ctx, prodID)
	if err != nil {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_product_not_found")))
		return
	}

	switch strings.ToLower(field) {
	case "telegram", "telegramlink":
		raw := value
		if strings.TrimSpace(raw) == "-" {
			raw = ""
		}
		link, err := storage.NormalizeTelegramURL(raw)
		if err != nil {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_telegram_invalid")))
			return
		}
		product.TelegramURL = link
	case "name":
		product.Name = value
	case "description":
		product.Description = value
	case "price":
		price, err := strconv.ParseFloat(value, 64)
		if err != nil {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_invalid_price")))
			return
		}
		if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_invalid_price")))
			return
		}
		product.PriceRUB = &price
		product.PriceUSD = 0
		product.OpenPrice = false
	case "openprice":
		on, err := strconv.ParseBool(value)
		if err != nil || product.SubPeriodDays > 0 {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "open_price_sub_error")))
			return
		}
		product.OpenPrice = on
		if on {
			zero := float64(0)
			product.PriceRUB = &zero
			product.PriceUSD = 0
			product.PriceStars = 0
		}
	case "infinitestock", "singleincart":
		on, err := strconv.ParseBool(value)
		if err != nil {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_invalid_bool")))
			return
		}
		if strings.EqualFold(field, "infinitestock") {
			product.InfiniteStock = on
		} else {
			product.SingleInCart = on
		}
	case "stock":
		stock, err := strconv.Atoi(value)
		if err != nil {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_invalid_stock")))
			return
		}
		product.Stock = stock
	case "category":
		categoryID, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_invalid_category_id")))
			return
		}
		product.CategoryID = categoryID
	case "active":
		active, err := strconv.ParseBool(value)
		if err != nil {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_invalid_bool")))
			return
		}
		product.IsActive = active
	default:
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_supported_fields")))
		return
	}

	if err := b.products.UpdateProduct(ctx, product); err != nil {
		b.loggerFor(ctx).Error("update product", "product_id", prodID, "error", err)
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_product_update_failed")))
		return
	}

	b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_product_updated")))
}

func (b *Bot) handleDeleteProduct(ctx context.Context, msg *tgbotapi.Message) {
	if !b.isAdmin(msg.From.ID) || msg.Chat.ID != msg.From.ID {
		return
	}
	lang := msg.From.LanguageCode
	id, err := strconv.ParseInt(strings.TrimSpace(msg.CommandArguments()), 10, 64)
	if err != nil || id <= 0 {
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_usage_deleteproduct")))
		return
	}
	if _, err := b.products.GetProduct(ctx, id); err != nil {
		if err == storage.ErrNotFound {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_product_not_found")))
		} else {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_product_delete_failed")))
		}
		return
	}
	if err := b.products.DeleteProduct(ctx, id); err != nil {
		b.loggerFor(ctx).Error("delete product", "product_id", id, "error", err)
		b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_product_delete_failed")))
		return
	}
	b.send(tgbotapi.NewMessage(msg.Chat.ID, b.t(lang, "admin_product_deleted")))
}

func (b *Bot) onAdminToggleStock(ctx context.Context, chatID int64, data, lang string) {
	productID, err := parseIDFromCallback(data, "admin:togglestock:")
	if err != nil {
		b.loggerFor(ctx).Error("parse admin:togglestock callback", "error", err)
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_product_parse_failed")))
		return
	}

	product, err := b.products.GetProduct(ctx, productID)
	if err != nil {
		b.loggerFor(ctx).Error("get product for stock toggle", "product_id", productID, "error", err)
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_product_not_found")))
		return
	}

	if product.Stock > 0 {
		product.Stock = 0
	} else {
		product.Stock = 1
		product.IsActive = true
	}

	if err := b.products.UpdateProduct(ctx, product); err != nil {
		b.loggerFor(ctx).Error("toggle product stock", "product_id", productID, "error", err)
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_product_update_failed")))
		return
	}

	b.sendAdminProductDetails(chatID, product, lang)
}

func (b *Bot) routeEditProduct(ctx context.Context, msg *tgbotapi.Message) {
	args := strings.Fields(msg.CommandArguments())
	if len(args) == 0 {
		return
	}
	id, _ := strconv.ParseInt(args[0], 10, 64)
	if len(args) == 1 {
		b.handleEditProduct(ctx, msg)
	} else {
		b.handleEditProductField(ctx, msg, id, args[1], strings.Join(args[2:], " "))
	}
}

func (b *Bot) onAdminQuantitySetting(ctx context.Context, chatID int64, data, lang string) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 {
		return
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return
	}
	p, err := b.products.GetProduct(ctx, id)
	if err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_product_not_found")))
		return
	}
	switch parts[1] {
	case "infinitestock":
		p.InfiniteStock = !p.InfiniteStock
	case "singleincart":
		p.SingleInCart = !p.SingleInCart
	default:
		return
	}
	if err := b.products.UpdateProduct(ctx, p); err != nil {
		b.send(tgbotapi.NewMessage(chatID, b.t(lang, "admin_product_update_failed")))
		return
	}
	b.sendAdminProductDetails(chatID, p, lang)
}
