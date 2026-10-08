# Ruble prices and optional payment

Product administration uses RUB. Existing USD orders retain their committed
amounts. Configure a positive `USD_TO_RUB_RATE` for conversion from the RUB
catalog to Stars, USD/USDT, TON and internal USD balance. The quote starts from the environment default and can be saved by an
administrator; checkout does not fetch exchange rates from the internet.

Existing products with a null `price_rub` are legacy USD products. A deployment
may convert them once with `ROUND(price_usd * <confirmed quote>, 2)`. Do not
update order snapshots during this conversion. New product prices are stored
in `price_rub`; changing a conversion quote does not change these ruble prices.

## Change the RUB exchange rate

In the bot's private administrator chat, `/rubrate` shows the current rate;
`/rubrate 110` sets 110 rubles per 1 USD. A decimal such as `/rubrate 92,5`
is also accepted. Values must be between 1 and 1,000,000 with at most four
decimal places, using a dot or comma. Only configured administrators can use
these commands in private. The admin/help panel and current-rate replies have
no rate buttons. Old rate buttons show the command instructions and cannot
start a bare-number input dialog.

The rate is persisted in SQLite and restored at startup before services run.
It overrides `USD_TO_RUB_RATE`; the environment value is the default until the
first admin save. Save failures leave the active rate unchanged. Bot catalog,
cart, checkout and Mini App use the same exchange service without restarting.
A cart calculation uses one fixed rate snapshot even during an admin change.
Reopen a product or refresh the cart to see updated prices in an open screen.

Stored RUB product prices, selected open amounts, existing orders and their
invoice amounts remain unchanged. New orders use the new rate. With
`USD_TO_STARS_RATE=50`, a rate of 100 means 100 RUB / 50 Stars; a rate of 200
means 100 RUB / 25 Stars. Stars-only checkout and zero-total grants are retained.

## Enable an open price

Open `/editproduct ID` in the administrator chat and press **Enable open price
(from 0 RUB)**. Alternatively use `/editproduct ID openprice true`.
The initial price becomes 0 RUB. A buyer can enter a whole amount from 0 to
1,000,000 RUB in the Mini App product/cart or with **Choose your price** in the
bot. The chosen amount belongs to that buyer's cart and is frozen on the order;
it does not change the catalog price or another buyer's choice.

The Mini App product card and cart preview RUB / Stars immediately while the
buyer enters a valid amount, using the current rate snapshot returned by the
product or cart API. At 100 RUB per USD and 50 Stars per USD, entering 100 shows
100 RUB / 50 Stars; zero remains 0 RUB / 0 Stars. Positive amounts use the same
integer conversion and one-Star minimum as checkout. The short Mini App hint
is **Open price: from 0 RUB**; there is no **Apply price** button.

For a product outside the cart, typing changes the preview; **Add to cart**
saves that amount. Once added, editing the product or cart amount saves
automatically after a 300 ms input pause, or immediately on change/blur.
Cart item prices, RUB / Stars totals and free/paid checkout controls update
without remounting the input or losing focus. Writes are serialized and newer
drafts survive older responses. Navigation, quantity changes, removal and
checkout wait for the latest valid price. Invalid input and failed saves cannot
reach checkout; the buyer can correct the amount or retry the action. Checkout
blocks duplicate clicks and input until its request finishes.

Reopening a product shows its saved buyer-specific cart amount. The Node check
`node tests/miniapp-open-price.cjs` covers previews, autosaves, racing edits,
focus preservation, navigation, quantity/removal, save failures and checkout.

## Buyers use the Mini App

`BOT_ADMIN_ONLY=true` keeps bot commands and callbacks for private
administration. In private chats, `/start` sends the WoodleWing welcome and an
**Open shop** Mini App button to both buyers and admins. `/admin` (or `/help`)
opens the administrator panel for admins. Other buyer messages receive a short
Mini App entry message with the same launch button.
Buyer catalog, cart, order, payment, file, profile, referral and review commands
and old callbacks are stubbed, including for admins acting as buyers. Inline
catalog search returns no results. The public command list only advertises
start/help, and the Shop menu button remains available. Admin product/category,
archive, RUB rate, order/refund, promotion and analytics commands/dialogs stay
available only to configured administrators in their private chat.

Mini App REST APIs, Stars pre-checkout/settlement, payment reconciliation,
notifications and automatic/repeat ZIP delivery remain active. Disabled bot
review invites and repeat-download buttons are omitted; buyers request downloads
from Mini App orders. The example environment enables this mode. Omitting the
flag or setting it to false retains the earlier buyer bot interface.

To return to a fixed price, use `/editproduct ID price <rubles>`.
Disabling open price alone leaves the current catalog price at 0. Subscriptions
cannot use open prices. A fixed-price product cannot be overridden by a custom
price request.

## Welcome information buttons

Configure `SHOP_OFFER_URL` with the HTTPS address of the owner's actual offer
and `SHOP_CONTACTS_TEXT` with the seller's contact details copied from that
offer. Contacts are plain text, up to 3000 characters; literal `\n` can be used
for line breaks in environment configuration. Blank settings hide the related
button rather than publishing placeholder terms or contacts.

The private `/start` welcome keeps **Open shop** in its first row, with
**Offer** and **Contacts** underneath when configured. Offer opens its published
URL; Contacts edits the message to show the supplied contact text with **Back**
to the same welcome. These read-only information callbacks are available to
buyers and admins even in admin-only bot mode. Buyer purchasing callbacks
remain disabled, and information views do not change the cart, orders or rates.
The owner's supplied Russian offer, edition 01.10.2026, is bundled at
`/app/offer.html`, with its source in `docs/public-offer.ru.md`. The local shop
links to that public page and displays the seller contacts from section 7.

## Get without payment

If every item makes the total zero, checkout shows **Get without payment**.
The server rechecks the stored order totals, ownership and state. The grant is
recorded separately from real payment receipts; no invoice, balance debit,
cashback or referral reward is created. Physical stock is reserved atomically.
An entirely digital order is fulfilled by the same durable ZIP worker used
for paid purchases. File ownership checks, retries, repeat downloads and access
to replacement archives also apply to free orders.

A mixed cart with any positive-price item must be paid. A free request for a
positive, foreign, canceled, reviewed or subscription order is denied.
An authorized promotion can also create a zero-total order; it consumes
the promotion's usage. Category discounts cover only matching items: a
100% code cannot grant unrelated paid items. The Mini App checks a code as
it is entered and updates RUB/Stars totals before checkout. See
[promo code administration and buyer behavior](promocodes.md).

Tests use real SQLite storage for ruble snapshots, whole-number validation,
free and paid bot/Mini App checkout, archive delivery, updates, ownership,
revoked access, and mixed-cart protection. No real payments are made.

## Temporary Stars-only checkout

Set `STARS_ONLY_PAYMENTS=true`, `USD_TO_RUB_RATE=100` and
`USD_TO_STARS_RATE=50`: 1 USD = 100 RUB = 50 Stars. Whole-ruble open prices
convert through the same quote in the bot and Mini App; for example 100 RUB
charges 50 Stars. Positive amounts have at least one Star, with existing integer
rounding. Product prices already stored in RUB are kept; old order currency
amounts and captured payments are not rewritten.

Both interfaces show prices as RUB / Stars in catalog, cart, checkout and order
history. Legacy USD catalog prices are displayed as rubles without updating the
stored product or order. Only Stars is offered for a positive checkout and for
resuming an existing order. CryptoBot, TON, NOWPayments, YooKassa, Stripe and
internal balance are hidden and rejected through direct API requests and stale
bot callbacks too. Zero-total orders keep free fulfillment. Existing payment
receipts, reconciliation and refund workflows remain available.

The example .env enables this mode. An omitted flag preserves compatibility;
setting it to false restores configured methods. Provider credentials are kept
in the existing private .env rather than removed to turn payments off.
