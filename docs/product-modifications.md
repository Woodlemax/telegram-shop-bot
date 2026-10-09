# Product modification groups

Private admin commands:
- `/editproduct MODIFICATION_ID parent MAIN_ID` attaches a product to the main model.
- `/editproduct MODIFICATION_ID parent 0` detaches it.
- `/editproduct ID` and `/listproduct` show the current parent ID.

Groups are one level deep, have no fixed limit on the number of main models, and initially collapse in the Mini App. The main card still opens its own product. Expanding Modifications loads separate child cards lazily, 20 at a time with Show more. Each group expands independently; expansion and the catalog page are retained when returning from a product card during the session. Prices, files, purchase rules and coming-soon labels remain independent for each item.

Catalog pages count visible main products and standalone products, not children. Inactive/deleted and unavailable non-coming-soon children stay hidden. Products must be in the same category when grouped. A child cannot have children or be its own parent. Database triggers enforce those rules. If a main product is hidden, deleted or changes category, its visible modifications become standalone cards; existing paid orders and digital entitlements do not change.

The API adds modification_count to grouped catalog products and an authenticated paged GET /api/products/ID/modifications endpoint. Existing storage/catalog interfaces remain compatible; production injects the group store explicitly.
