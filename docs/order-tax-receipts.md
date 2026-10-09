# My Tax receipts

New YooKassa checkout descriptions include the order number (128 Unicode characters maximum). Existing frozen requests retain their original description and idempotence key.

Admin workflow (private chat):
1. `/order ID` shows the buyer Telegram ID, username, RUB amount and YooKassa payment ID.
2. Create the actual receipt in My Tax and copy its HTTPS `lknpd.nalog.ru/api/v1/receipt/…` link.
3. `/receipt ID URL` attaches it to a paid YooKassa order, snapshots the buyer/payment, then sends a receipt button to that buyer.
4. `/order ID` shows the link and delivery status. The buyer can also open the receipt from the Mini App order.

The feature does not create receipts or register income with the tax authority. The administrator must check the receipt amount, date and items against the order.

Only the same link can be retried for an order. A link or payment already bound to another order cannot be reused. Successful Telegram delivery is recorded with message ID/time; repeating the command does not resend. Failed delivery leaves the receipt available in the Mini App and the same command retries it. A persisted two-minute lease prevents concurrent duplicate sends. A process crash after Telegram accepts the message but before saving the result may leave delivery unconfirmed: check before retrying. No buyer is contacted merely by deploying this feature.
