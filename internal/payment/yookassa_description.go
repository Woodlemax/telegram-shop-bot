package payment

import (
	"fmt"
	"strings"
)

// Freeze this text in the checkout intent, so retries retain the original body.
func yookassaOrderDescription(orderID int64, items string) string {
	text := fmt.Sprintf("Заказ №%d — %s", orderID, strings.TrimSpace(items))
	chars := []rune(text)
	if len(chars) > 128 {
		text = string(chars[:127]) + "…"
	}
	return text
}
