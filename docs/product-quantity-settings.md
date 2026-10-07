# Product stock and cart quantity settings

Open `/editproduct ID` in the administrator chat. Two independent buttons
control each product:

- **Enable unlimited stock**: the product remains available at stock zero;
  paid and free orders do not decrease its stored stock. Turning it off restores
  the saved finite stock. Activity still controls whether the product is sold.
- **Limit to one unit**: repeated adds keep one unit in that buyer's cart.
  Quantity increase controls are disabled/hidden in the Mini App and bot.
  Enabling the setting reduces existing cart rows to one unit. Other buyers
  have their own carts; this does not prevent repeat purchases in later orders.

Commands also work:

```
/editproduct ID infinitestock true
/editproduct ID singleincart true
```

Use `false` to turn either setting off. Both settings can be combined or
used separately. Normal products retain finite stock and unrestricted cart
quantity by default. Existing digital ZIP products are migrated with both
settings enabled, preserving their previous behavior. Newly created digital
products and attaching a ZIP to a physical product enable both settings;
administrators can change them afterward. Replacing an existing ZIP preserves
these settings and the current stock.

The single-unit limit is checked on the server and within the order creation
transaction. Existing pending orders retain their quantities and prices.
Stock is checked again when a payment or free grant settles. Unlimited stock
is skipped in that transaction; finite stock uses an atomic availability guard.
If multiple units of a digital ZIP are allowed, the price is multiplied by
quantity and the same archive is delivered once per product/order.
