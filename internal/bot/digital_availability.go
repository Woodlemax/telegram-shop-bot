package bot

import "shop_bot/internal/storage"

func (b *Bot) productAvailabilityText(lang string, p *storage.Product) string {
	availability := b.productStockText(lang, p.Stock)
	if p.InfiniteStock {
		availability = b.t(lang, "product_infinite_stock")
	}
	if p.IsDigital {
		availability += "\n" + b.t(lang, "digital_product")
	}
	if p.SingleInCart {
		availability += "\n" + b.t(lang, "product_single_in_cart")
	}
	return availability
}
