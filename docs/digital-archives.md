# Digital ZIP products

Administrators attach ZIP documents to regular products. The bot stores the
Telegram `file_id`; it never downloads the archive or reuploads its bytes.
An 80 MB document is therefore delivered by reusing the file already held by
Telegram. Real delivery of the owner's archive must still be checked in Telegram.

## Attach or replace an archive

1. Open the administrator's **private** chat with the bot.
2. Open `/admin`, select the product, and press **Upload / replace ZIP**.
   Alternatively send `/setarchive <product_id>`.
3. Send the `.zip` as a document within 15 minutes. `/cancel` cancels the upload.
4. The product becomes digital and payable with Telegram Stars. Subscription
   products cannot have ZIP archives. Each digital product is ordered once per
   cart, and completed digital sales do not reduce its stock.

The upload records the filename and size declared by Telegram. It checks the
`.zip` extension, not the compressed contents; upload a ZIP you have verified.
There is no public download URL or file identifier in the catalog API.

## Payment and delivery

A delivery row is created in the **same transaction as the order**. A worker runs
every five seconds and admits only orders with a settled Stars payment and a
successful Stars ledger entry. Unpaid, canceled, quarantined, refunded and
partially refunded orders do not grant downloads. The buyer's Telegram ID is the
recipient; callbacks cannot request another user's files or a group destination.

The worker sends `sendDocument` with the stored `file_id`. Successful sends store
the message ID. An entirely digital order is then marked delivered; mixed orders
retain their existing physical fulfillment workflow.

Failures use durable backoff from one minute up to one hour. Two-minute leases
prevent concurrent workers claiming the same task and recover after restart.
Payment callback replays do not create a second task. Telegram does not provide
an idempotency key for `sendDocument`: if Telegram accepts a document but the
response or result commit is lost, recovery can send it again. It does not charge
the buyer again. Operational errors suppress Telegram request details/tokens.

## Downloads and updates

The delivered document has **Download again**. Buyers can also use `/files` or
**My files** in the bot's order history (the latest 50 purchase entries).

Replacing an archive updates its reference on **all existing purchases**,
including paid and delivered orders. A previous buyer downloads the latest ZIP
without another payment. An upload does not broadcast a new document to previous
buyers or modify the file already present in their chat. Failed/in-flight initial
deliveries are returned to the queue for the new version. Old archive metadata
is retained for history.

## Validation

Tests use the real order/payment storage with an imitation Telegram API:
80 MB document metadata, administrative permissions, ZIP validation, expiration
and cancellation, no delivery before payment, Stars confirmation, no stock
decrement, payment replay, updated access for previous buyers, ownership checks,
revoked access, send failure, exclusive claims and expired-lease recovery.
Mini App tests cover Stars-only checkout and keeping archive identifiers private.

Real Stars charges are not used in automated checks.
