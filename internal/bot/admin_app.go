package bot

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"shop_bot/internal/storage"
)

type adminAppError struct {
	status  int
	message string
}

func (e adminAppError) Error() string    { return e.message }
func (e adminAppError) AdminStatus() int { return e.status }
func adminInvalid(message string) error  { return adminAppError{400, message} }
func adminConflict(message string) error { return adminAppError{409, message} }

type adminConfirmation struct {
	Actor, OrderID, AmountMinor, UserID int64
	Action, PaymentID, Reason           string
	DeltaUSD                            float64
	Resolution                          storage.PaymentReviewResolution
	Expires                             time.Time
}

func (b *Bot) adminStore() *storage.SQLProductStore { return storage.NewSQLProductStore(b.adminDB) }
func (b *Bot) invalidateAdminProduct(ctx context.Context, id int64) {
	if cache, ok := b.products.(interface {
		Invalidate(context.Context, ...int64)
	}); ok {
		cache.Invalidate(ctx, id)
	}
}

func (b *Bot) adminProduct(ctx context.Context, id int64) (any, error) {
	p, err := b.adminStore().GetProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	parent, err := b.productGroups.Parent(ctx, id)
	if err != nil {
		return nil, err
	}
	extra, err := b.photos.List(ctx, id)
	if err != nil {
		return nil, err
	}
	photos := make([]string, 0)
	seen := map[string]bool{}
	for _, photo := range append([]storage.ProductPhoto{{FileID: p.PhotoURL}}, extra...) {
		if photo.FileID != "" && !seen[photo.FileID] {
			photos = append(photos, photo.FileID)
			seen[photo.FileID] = true
		}
	}
	archive, err := b.archives.Latest(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return map[string]any{"product": p, "parent_id": parent, "revision": storage.ProductRevision(p, parent), "photos": photos, "archive": archive}, nil
}

func adminPage(q url.Values) int {
	n, _ := strconv.Atoi(q.Get("page"))
	if n < 1 {
		n = 1
	}
	if n > 10000 {
		n = 10000
	}
	return n
}

func (b *Bot) AdminRead(ctx context.Context, actor int64, resource string, id int64, q url.Values) (any, error) {
	if !b.isAdmin(actor) {
		return nil, adminAppError{403, "Доступ разрешён только администратору."}
	}
	switch resource {
	case "file-check":
		return storage.NewProductFileCheckStore(b.adminDB.Conn()).Requirement(ctx, id)
	case "bootstrap":
		categories, err := b.adminStore().ListCategoriesAdmin(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"categories": categories, "rub_rate": b.currentRUBRate(), "stars_rate": b.exchange.GetUSDToStarsRate(), "max_photos": storage.MaxProductPhotos, "max_archive_mb": storage.MaxAdminArchiveBytes >> 20}, nil
	case "products":
		if id > 0 {
			return b.adminProduct(ctx, id)
		}
		page := adminPage(q)
		const perPage = 40
		// Administrative search includes inactive, empty-stock and external offers.
		all := make([]storage.Product, 0)
		store := b.adminStore()
		query := strings.ToLower(strings.TrimSpace(q.Get("q")))
		cat, _ := strconv.ParseInt(q.Get("category"), 10, 64)
		for offset := 0; ; offset += 100 {
			items, total, err := store.ListProductsAdmin(ctx, 100, offset)
			if err != nil {
				return nil, err
			}
			for _, p := range items {
				if query != "" && !strings.Contains(strings.ToLower(p.Name), query) && strconv.FormatInt(p.ID, 10) != query {
					continue
				}
				if cat > 0 && p.CategoryID != cat {
					continue
				}
				all = append(all, p)
			}
			if offset+100 >= total {
				break
			}
		}
		total := len(all)
		start := (page - 1) * perPage
		if start > total {
			start = total
		}
		end := start + perPage
		if end > total {
			end = total
		}
		parents, counts, err := store.AdminProductGroups(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"products": all[start:end], "total": total, "page": page, "per_page": perPage, "parent_ids": parents, "modification_counts": counts}, nil
	case "categories":
		items, err := b.adminStore().ListCategoriesAdmin(ctx)
		return map[string]any{"categories": items}, err
	case "orders":
		if id > 0 {
			o, err := b.order.GetOrder(ctx, id)
			if err != nil {
				return nil, err
			}
			u, _ := b.users.GetByTelegramID(ctx, o.UserID)
			refunds, err := b.payLedger.ListRefunds(ctx, id)
			if err != nil {
				return nil, err
			}
			return map[string]any{"order": o, "buyer": u, "refunds": refunds, "can_refund": refundableOrder(o) && !refundIsManualRail(o.PaymentMethod)}, nil
		}
		orders, err := b.order.GetAllOrders(ctx, q.Get("status"))
		if err != nil {
			return nil, err
		}
		out := make([]storage.Order, 0)
		search := strings.TrimSpace(q.Get("q"))
		for _, o := range orders {
			if search != "" && search != strconv.FormatInt(o.ID, 10) && search != strconv.FormatInt(o.UserID, 10) {
				continue
			}
			out = append(out, o)
		}
		page := adminPage(q)
		total := len(out)
		start := (page - 1) * 40
		if start > total {
			start = total
		}
		end := start + 40
		if end > total {
			end = total
		}
		return map[string]any{"orders": out[start:end], "total": total, "page": page, "per_page": 40}, nil
	case "export":
		var args []string
		if q.Get("from") != "" {
			args = append(args, q.Get("from"))
		}
		if q.Get("to") != "" {
			if len(args) == 0 {
				return nil, adminInvalid("Укажите начальную дату.")
			}
			args = append(args, q.Get("to"))
		}
		from, to, bad := parseExportRange(args)
		if bad != "" {
			return nil, adminInvalid("Проверьте даты экспорта.")
		}
		orders, err := b.order.GetAllOrders(ctx, "")
		if err != nil {
			return nil, err
		}
		orders = filterOrdersByDate(orders, from, to)
		var buf bytes.Buffer
		writer := csv.NewWriter(&buf)
		_ = writer.Write([]string{"order_id", "telegram_id", "status", "total_rub", "total_stars", "total_usd", "payment_method", "payment_id", "promo_code", "created_at", "tax_receipt"})
		cell := func(s string) string {
			if len(s) > 0 && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
				return "'" + s
			}
			return s
		}
		for _, o := range orders {
			receipt := ""
			if o.TaxReceipt != nil {
				receipt = o.TaxReceipt.URL
			}
			_ = writer.Write([]string{strconv.FormatInt(o.ID, 10), strconv.FormatInt(o.UserID, 10), o.Status, fmt.Sprintf("%.2f", o.TotalRUB), strconv.Itoa(o.TotalStars), fmt.Sprintf("%.2f", o.TotalUSD), o.PaymentMethod, cell(o.PaymentID), cell(o.PromoCode), o.CreatedAt.Format(time.RFC3339), cell(receipt)})
		}
		writer.Flush()
		return map[string]any{"csv": buf.String(), "count": len(orders)}, writer.Error()
	case "promos":
		items, err := b.promos.ListPromos(ctx)
		return map[string]any{"promos": items}, err
	case "reviews":
		items, err := b.reviews.ListRecent(ctx, 100)
		return map[string]any{"reviews": items}, err
	case "payment-reviews":
		items, err := b.listAllPaymentReviews(ctx)
		out := make([]map[string]any, 0)
		for _, item := range items {
			out = append(out, map[string]any{"case": item, "actions": payReviewActions(item), "reason": payReviewReasons(item)})
		}
		return map[string]any{"cases": out}, err
	case "settings":
		styles, err := b.uiSettings.ListButtonStyles(ctx)
		if err != nil {
			return nil, err
		}
		buttons := make([]map[string]string, 0)
		for _, key := range AllButtonKeys {
			buttons = append(buttons, map[string]string{"key": key, "label": ButtonKeyLabel(key), "style": styles[key]})
		}
		return map[string]any{"rub_rate": b.currentRUBRate(), "stars_rate": b.exchange.GetUSDToStarsRate(), "buttons": buttons, "payment_status": b.formatPayStatus("ru"), "purchase_rewards_enabled": b.purchaseRewardsEnabled()}, nil
	case "user":
		u, err := b.users.GetByTelegramID(ctx, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"user": u, "balance_rub": u.BalanceUSD * b.currentRUBRate()}, nil
	case "analytics":
		days, _ := strconv.Atoi(q.Get("days"))
		if days != 7 && days != 30 && days != 90 {
			days = 30
		}
		summary, err := b.analytics.GetRevenueSummary(ctx)
		if err != nil {
			return nil, err
		}
		daily, err := b.analytics.GetRevenueByDays(ctx, days)
		if err != nil {
			return nil, err
		}
		products, err := b.analytics.GetTopProducts(ctx, 10)
		if err != nil {
			return nil, err
		}
		buyers, err := b.analytics.TopBuyers(ctx, 10)
		if err != nil {
			return nil, err
		}
		promos, err := b.analytics.PromoUsage(ctx)
		if err != nil {
			return nil, err
		}
		methods, err := b.analytics.GetPaymentMethodStats(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"summary": summary, "daily": daily, "products": products, "buyers": buyers, "promos": promos, "methods": methods, "days": days, "rub_rate": b.currentRUBRate()}, nil
	}
	return nil, adminAppError{404, "Раздел не найден."}
}

func decodeAdmin(raw json.RawMessage, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return adminInvalid("Проверьте заполненные поля.")
	}
	return nil
}

func (b *Bot) AdminWrite(ctx context.Context, actor int64, resource string, id int64, raw json.RawMessage) (result any, err error) {
	if !b.isAdmin(actor) {
		return nil, adminAppError{403, "Доступ разрешён только администратору."}
	}
	b.adminAppMu.Lock()
	defer b.adminAppMu.Unlock()
	defer func() {
		if err == nil && resource != "refund-preview" && resource != "review-preview" && resource != "balance-preview" {
			if _, e := b.adminDB.Conn().ExecContext(ctx, `INSERT INTO admin_app_audit(admin_id,action,target_id) VALUES(?,?,?)`, actor, resource, id); e != nil {
				b.logger.Error("admin audit save failed")
			}
		}
	}()
	ok := map[string]any{"ok": true}
	switch resource {
	case "archive-telegram":
		if err = b.archives.BeginUpload(ctx, actor, actor, id); err != nil {
			return nil, err
		}
		return map[string]any{"message": fmt.Sprintf("Отправьте ZIP до 50 МБ документом в личный чат бота для товара #%d. Большой ZIP загрузите через админку — он будет разбит на части. Ожидание — 15 минут. Новый архив будет доступен прежним покупателям. Для отмены: /cancel.", id), "chat_url": (&url.URL{Scheme: "https", Host: "t.me", Path: "/" + b.api.Self.UserName}).String()}, nil
	case "archive-upload-cancel":
		return ok, b.archives.CancelUpload(ctx, actor)
	case "file-check":
		var input struct {
			Enabled     bool   `json:"enabled"`
			Description string `json:"description"`
			Revision    int64  `json:"revision"`
		}
		if err = decodeAdmin(raw, &input); err != nil {
			return nil, err
		}
		return storage.NewProductFileCheckStore(b.adminDB.Conn()).Save(ctx, id, input.Enabled, input.Description, input.Revision)
	case "products":
		var input struct {
			Product  storage.Product `json:"product"`
			ParentID int64           `json:"parent_id"`
			Revision string          `json:"revision"`
		}
		if err = decodeAdmin(raw, &input); err != nil {
			return nil, err
		}
		p := input.Product
		p.ID = id
		p.PriceUSD = 0
		p.PriceStars = 0
		if p.PriceRUB != nil && *p.PriceRUB > 1000000000 {
			return nil, storage.ErrInvalidMoney
		}
		if p.SubPeriodDays > 0 && p.PriceRUB != nil {
			p.PriceStars = b.exchange.ConvertUSDToStars(b.exchange.ConvertRUBToUSD(*p.PriceRUB))
		}
		id, err = b.adminStore().SaveAdminProduct(ctx, &p, input.ParentID, input.Revision)
		if err != nil {
			return nil, err
		}
		b.invalidateAdminProduct(ctx, id)
		return b.adminProduct(ctx, id)
	case "product-delete":
		if id <= 0 {
			return nil, storage.ErrNotFound
		}
		err = b.products.DeleteProduct(ctx, id)
		return ok, err
	case "photos":
		var input struct {
			Photos []string `json:"photos"`
		}
		if err = decodeAdmin(raw, &input); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, ref := range input.Photos {
			if !validAdminPhoto(ref) || seen[ref] {
				return nil, adminInvalid("Проверьте фотографии: повторения и некорректные ссылки недопустимы.")
			}
			seen[ref] = true
		}
		err = b.adminStore().SetAdminPhotos(ctx, id, input.Photos)
		if err != nil {
			return nil, err
		}
		b.invalidateAdminProduct(ctx, id)
		return b.adminProduct(ctx, id)
	case "categories":
		var c storage.Category
		if err = decodeAdmin(raw, &c); err != nil {
			return nil, err
		}
		c.ID = id
		if strings.TrimSpace(c.Name) == "" || len([]rune(c.Name)) > 200 || len([]rune(c.Emoji)) > 20 || len(c.CustomEmojiID) > 64 {
			return nil, adminInvalid("Укажите название категории; эмодзи — необязательно.")
		}
		if id == 0 {
			id, err = b.products.CreateCategory(ctx, &c)
		} else {
			if _, err = b.products.GetCategory(ctx, id); err == nil {
				err = b.products.UpdateCategory(ctx, &c)
			}
		}
		return map[string]any{"id": id}, err
	case "category-delete":
		if _, err = b.products.GetCategory(ctx, id); err != nil {
			return nil, err
		}
		err = b.products.DeleteCategory(ctx, id)
		if err != nil {
			return nil, adminInvalid("Сначала перенесите или удалите товары этой категории.")
		}
		return ok, nil
	case "promos":
		var p storage.PromoCode
		if err = decodeAdmin(raw, &p); err != nil {
			return nil, err
		}
		p.ID = 0
		p.UsedCount = 0
		p.IsActive = true
		p.Code = strings.ToUpper(strings.TrimSpace(p.Code))
		if err = storage.ValidatePromo(&p); err != nil {
			return nil, adminInvalid("Проверьте код, скидку, срок действия и выбранные товары.")
		}
		if p.ExpiresAt != nil && !p.ExpiresAt.After(time.Now()) {
			return nil, adminInvalid("Срок действия должен быть в будущем.")
		}
		if p.CategoryID != nil {
			if _, err = b.products.GetCategory(ctx, *p.CategoryID); err != nil {
				return nil, err
			}
		}
		for _, pid := range p.ProductIDs {
			if _, err = b.products.GetProduct(ctx, pid); err != nil {
				return nil, err
			}
		}
		id, err = b.promos.CreatePromo(ctx, &p)
		return map[string]any{"id": id}, err
	case "promo-deactivate":
		err = b.promos.DeactivatePromo(ctx, id)
		return ok, err
	case "review-delete":
		err = b.reviews.Delete(ctx, id)
		return ok, err
	case "delivered":
		o, e := b.order.SetDelivered(ctx, id)
		if e != nil {
			return nil, e
		}
		b.sendReviewInvite(ctx, o)
		b.notifyAdmins(ctx, AdminEventOrderDelivered, fmt.Sprintf(b.t("en", "admin_order_delivered"), o.ID, o.UserID))
		b.announceAdminDelivery(o)
		return ok, nil
	case "receipt":
		var input struct {
			URL string `json:"url"`
		}
		if err = decodeAdmin(raw, &input); err != nil {
			return nil, err
		}
		message, e := b.attachAdminReceipt(ctx, actor, id, input.URL)
		return map[string]string{"message": message}, e
	case "rub-rate":
		var input struct {
			Rate float64 `json:"rate"`
		}
		if err = decodeAdmin(raw, &input); err != nil {
			return nil, err
		}
		err = b.exchange.UpdateRUBRate(ctx, input.Rate)
		return ok, err
	case "button-style":
		var input struct {
			Key   string `json:"key"`
			Style string `json:"style"`
		}
		if err = decodeAdmin(raw, &input); err != nil {
			return nil, err
		}
		known := false
		for _, k := range AllButtonKeys {
			if k == input.Key {
				known = true
			}
		}
		if !known || (input.Style != "" && input.Style != "primary" && input.Style != "success" && input.Style != "danger") {
			return nil, adminInvalid("Неизвестная кнопка или цвет.")
		}
		err = b.uiSettings.SetButtonStyle(ctx, input.Key, input.Style)
		if err == nil {
			b.uiStyles.Store(input.Key, input.Style)
		}
		return ok, err
	case "refund-preview":
		return b.adminRefundPreview(ctx, actor, id, raw)
	case "review-preview":
		return b.adminReviewPreview(ctx, actor, raw)
	case "balance-preview":
		var input struct {
			UserID   int64   `json:"user_id"`
			DeltaRUB float64 `json:"delta_rub"`
			Reason   string  `json:"reason"`
		}
		if err = decodeAdmin(raw, &input); err != nil {
			return nil, err
		}
		if input.UserID <= 0 || input.DeltaRUB == 0 || math.Abs(input.DeltaRUB) > 1000000000 || b.currentRUBRate() <= 0 || len([]rune(input.Reason)) > 200 {
			return nil, storage.ErrInvalidMoney
		}
		old, e := b.balances.GetBalance(ctx, input.UserID)
		if e != nil {
			return nil, e
		}
		delta := math.Round(input.DeltaRUB/b.currentRUBRate()*100) / 100
		if delta == 0 || old+delta < 0 {
			return nil, adminInvalid("Недостаточно средств или слишком маленькая сумма.")
		}
		token := b.newAdminConfirmation(adminConfirmation{Actor: actor, Action: "balance", UserID: input.UserID, DeltaUSD: delta, Reason: input.Reason})
		return map[string]any{"token": token, "message": fmt.Sprintf("Пользователь %d. Баланс: %.2f ₽ → %.2f ₽. Причина: %s", input.UserID, old*b.currentRUBRate(), (old+delta)*b.currentRUBRate(), input.Reason)}, nil
	case "confirm":
		var input struct {
			Token string `json:"token"`
		}
		if err = decodeAdmin(raw, &input); err != nil {
			return nil, err
		}
		confirmation, exists := b.adminConfirmations[input.Token]
		if !exists || confirmation.Actor != actor || time.Now().After(confirmation.Expires) {
			return nil, adminConflict("Подтверждение устарело. Сформируйте его заново.")
		}
		delete(b.adminConfirmations, input.Token)
		return b.confirmAdminAction(ctx, actor, input.Token, confirmation)
	}
	return nil, adminAppError{404, "Действие не найдено."}
}

func (b *Bot) newAdminConfirmation(c adminConfirmation) string {
	if b.adminConfirmations == nil {
		b.adminConfirmations = map[string]adminConfirmation{}
	}
	for token, old := range b.adminConfirmations {
		if time.Now().After(old.Expires) {
			delete(b.adminConfirmations, token)
		}
	}
	c.Expires = time.Now().Add(3 * time.Minute)
	token := uuid.NewString()
	b.adminConfirmations[token] = c
	return token
}
