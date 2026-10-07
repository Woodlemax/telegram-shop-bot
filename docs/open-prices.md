# Ruble prices and optional payment

Product administration uses RUB. Existing USD orders retain their committed
amounts. Configure a positive `USD_TO_RUB_RATE` for conversion from the RUB
catalog to Stars, USD/USDT, TON and internal USD balance. The quote is fixed in
configuration; checkout does not fetch exchange rates from the internet.

Existing products with a null `price_rub` are legacy USD products. A deployment
may convert them once with `ROUND(price_usd * <confirmed quote>, 2)`. Do not
update order snapshots during this conversion. New product prices are stored
in `price_rub`; changing a conversion quote does not change these ruble prices.

## Enable an open price

Open `/editproduct ID` in the administrator chat and press **Enable open price
(from 0 RUB)**. Alternatively use `/editproduct ID openprice true`.
The initial price becomes 0 RUB. A buyer can enter a whole amount from 0 to
1,000,000 RUB in the Mini App product/cart or with **Choose your price** in the
bot. The chosen amount belongs to that buyer's cart and is frozen on the order;
it does not change the catalog price or another buyer's choice.

To return to a fixed price, use `/editproduct ID price <rubles>`.
Disabling open price alone leaves the current catalog price at 0. Subscriptions
cannot use open prices. A fixed-price product cannot be overridden by a custom
price request.

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
An authorized 100% promotion can also create a zero-total order; it consumes
the promotion's usage.

Tests use real SQLite storage for ruble snapshots, whole-number validation,
free and paid bot/Mini App checkout, archive delivery, updates, ownership,
revoked access, and mixed-cart protection. No real payments are made.
