# Mini App order history

Open **My orders** below the catalog categories. Orders are shown ten per page.
Select an order to see its saved items, quantities, date, total, payment method
and current order/payment status. Each buyer can access only their own orders.

Names and amounts come from saved order snapshots. Legacy orders without a RUB
total retain their original USD/Stars amounts. Free orders are included alongside
pending, paid, delivered and canceled orders; refunds and payment review have
explicit status labels. **Refresh** reloads the current status from the database.

The history GET endpoints use signed Telegram authentication and return the
same 404 for a foreign or missing order. Responses omit provider payment IDs,
Telegram archive file IDs and other buyers' details; private responses are not
cached. Paging bounds database queries and item loading. Reading history creates
no payments or mutations. The bot's **My orders** and `/orders` remain available.

## Continue payment

An order awaiting payment offers **Continue payment**. Choose an available
method on the next screen. Stars opens the Telegram invoice; external methods
open their payment link. Return to the shop and press **Refresh** to check an
external payment. Closing a Stars invoice leaves the order available for retry.

This pays the existing order with its original number, saved amounts, item names
and discount. It neither creates another order nor changes or clears the current
cart. Current catalog prices, exchange rates and new promo codes do not alter the
order. The server rejects payment requests for another buyer's order and for
orders already paid, canceled, refunded or awaiting payment review.

Methods depend on the shop's configured providers and amounts saved on the order:
Stars, CryptoBot, YooKassa, Stripe, TON and NOWPayments. Stripe requires at least
50 USD cents. A missing saved RUB or TON amount cannot be recalculated from a
new exchange rate to enable that method. Subscription snapshots allow only Stars.
An entirely zero-total non-subscription order offers **Get without payment**;
the same order receives its free grant and existing ZIP delivery entitlement.

## Cancel or download

Unpaid orders offer **Cancel order** with confirmation. The stored owner and
pending payment are checked atomically, so a payment confirmed concurrently
cannot be overwritten by cancellation. Paid or granted-free orders require
their existing fulfillment/refund workflows instead of this button.

Available digital items offer **Download** with the latest ZIP's filename.
The bot sends it to the buyer's private chat. Replacing a product archive updates
previous purchases too. See [digital archives](digital-archives.md).

## Orders in the bot

Send `/orders` or use **My orders** in the bot menu. History is paged ten orders
at a time. Pending orders with a pending payment offer **Continue payment #N**
and **Cancel order #N**. Cancel opens a confirmation; Back leaves the order
unchanged. A payment confirmed before cancellation cannot be overwritten.

Continue payment lists methods for that same order's saved amounts, including
internal balance when available. It does not create a purchase, clear the current
cart, recalculate the discount or use current catalog prices. Subscription terms
remain frozen and Stars-only; zero-total orders offer free fulfillment. Payment
callbacks check ownership and payment status again, so old buttons cannot pay a
canceled, paid or reviewed order.

**My files** shows one latest archive per eligible product even if bought more
than once. Refund of one purchase preserves access through another valid purchase.
