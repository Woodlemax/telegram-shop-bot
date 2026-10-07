# Mini App order history

Use **My orders** in the catalog or the package button in the header. History
shows the buyer's orders, newest first, with ten orders per page. Open an order
to see its saved product names and quantities, total, date, current status and
payment method. Refresh updates payment, delivery and refund statuses.

Order amounts and names come from the order snapshots. Renaming a product,
changing a price or changing the exchange quote does not rewrite history.
Legacy orders with no RUB total retain their original USD/Stars amounts.
Free orders are included alongside pending, paid, delivered and canceled orders.
Refund and payment-review states are shown explicitly.

`GET /api/orders?page=1` and `GET /api/orders/<id>` require signed Telegram
Mini App authentication. Identity is taken only from the authenticated user;
another buyer's order returns the same 404 as a missing order. The responses
contain only buyer-visible fields and never expose provider payment identifiers,
archive file IDs or other customers' details. Paging bounds database queries and
item loading. History performs no mutations and creates no payments.

The bot's existing **My orders** button and `/orders` command remain available.
