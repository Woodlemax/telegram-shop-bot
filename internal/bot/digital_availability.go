package bot

import "shop_bot/internal/storage"

func (b *Bot) productAvailabilityText(lang string, p *storage.Product) string {
	if p.IsDigital {
		return b.t(lang, "digital_product")
	}
	return b.productStockText(lang, p.Stock)
}
