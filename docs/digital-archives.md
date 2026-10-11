# Digital ZIP products

Administrators upload ZIPs through the admin Mini App (up to 512 MiB). Archives
larger than 50,000,000 bytes are split into ordered binary volumes of at most
49,000,000 bytes: `model.zip.001`, `model.zip.002`, etc. The original ZIP remains
in persistent `admin-media`; uploads stream byte ranges without extracting or
recompressing model files. All bytes are preserved.

## Attach or replace an archive

1. Open `/admin`, select a product and open **Archive**.
2. Choose the original ZIP and confirm replacement. Large ZIPs split automatically.
3. Small ZIPs (up to 50 MB) can alternatively be attached through the Telegram
   button, in the administrator's private chat within 15 minutes; `/cancel`
   cancels this pending upload. Legacy `/setarchive` applies only with the legacy
   admin interface enabled. Newly attached large Telegram documents are rejected
   with instructions to use the browser splitter, preserving the existing archive.
4. The product becomes digital. Replacement updates all previous purchases as
   one atomic version, including every part. Subscription products cannot have ZIPs.

Already stored large Telegram documents remain reusable by `file_id`, without
reupload. The cloud Bot API cannot download them for splitting: reattach the
original ZIP through the admin Mini App to convert it into parts. Browser uploads
are checked as nonempty ZIPs. There is no public download URL or file identifier
in the catalog API.

## Payment and delivery

A delivery row is created in the **same transaction as the order**. A worker runs
every five seconds and admits only orders with a settled payment and a
successful ledger entry matching the order's provider and payment identifier.
Supported methods are Stars, CryptoBot, YooKassa, Stripe, TON, NOWPayments and
internal balance. Provider availability, configured exchange rates and positive
balance determine which payment buttons appear. No provider keys are enabled
automatically by attaching a ZIP. Unpaid, canceled, quarantined, refunded and
partially refunded orders do not grant downloads. The buyer's Telegram ID is the
recipient; callbacks cannot request another user's files or a group destination.

Zero-total orders can receive a separately recorded free grant through
**Get without payment**. They have no money receipt; the same worker and download
ownership checks apply. See [open prices](open-prices.md).

The worker sends every part as `sendDocument`, streaming a bounded byte range on
first upload and storing the returned Telegram `file_id` for reuse. Each successful
part stores its message ID in durable progress. Leases renew before each upload;
a retry skips acknowledged parts, while a fresh download resets part progress.
Every caption states the part number and instructs the buyer to save all parts in
one folder and open `.001` with 7-Zip. A delivery completes only after all parts. An entirely digital order is then marked delivered; mixed orders
retain their existing physical fulfillment workflow.

Failures use durable backoff from one minute up to one hour. Two-minute leases
prevent concurrent workers claiming the same task and recover after restart.
Payment callback replays do not create a second task. Telegram does not provide
an idempotency key for `sendDocument`: if Telegram accepts a document but the
response or result commit is lost, recovery can send it again. It does not charge
the buyer again. Operational errors suppress Telegram request details/tokens.

## Downloads and updates

The delivered document has **Download again**. Buyers can also use `/files` or
**My files** in the bot's order history (up to 50 distinct products).
The library shows each product once even after repeat purchases; the newest
eligible purchase represents its button and points to the latest archive.
Grouping happens after entitlement checks and before the limit. A refunded
repeat purchase does not hide an older valid purchase. All order/delivery
records remain intact for history and per-order downloads.

In the Mini App, **My orders** is below the catalog categories. Open an order
and press **Download** next to an available ZIP. The bot sends the current archive
to the buyer's private chat, where it can be saved. This works for both paid and
free orders and is not limited to the latest 50 purchases. Requests while a file
is queued or sending reuse the same delivery task; requesting a previously sent
file queues it again without changing payments or stock.

Replacing an archive updates its reference on **all existing purchases**,
including paid and delivered orders. A previous buyer downloads the latest ZIP
without another payment. An upload does not broadcast a new document to previous
buyers or modify the file already present in their chat. Failed/in-flight initial
deliveries are returned to the queue for the new version. Old archive metadata
is retained for history.

## Validation

Tests use the real order/payment storage with an imitation Telegram API:
legacy 80 MB Telegram document metadata, an 80 MiB browser ZIP split and reassembled
with a matching SHA-256, second-part failure/resume, cached multipart downloads,
replacement by a single ZIP, administrative permissions, ZIP validation, expiration
and cancellation, no delivery before payment, confirmation through every supported
payment method, incorrect amounts and payment identifiers, balance checkout, no stock
decrement, payment replay, updated access for previous buyers, ownership checks,
revoked access, send failure, exclusive claims and expired-lease recovery.
Mini App tests cover checkout with every configured provider and keeping archive identifiers private.

Real payments are not used in automated checks.

## Telegram payment requirements

Telegram requires Stars for digital goods sold inside bots and Mini Apps:
https://core.telegram.org/bots/payments-stars#faq. The example configuration enables
`STARS_ONLY_PAYMENTS=true`, enforcing Stars for all positive checkouts. Other
configured methods remain available when that switch is false. Recurring
subscription products retain their Stars-only payment path.
