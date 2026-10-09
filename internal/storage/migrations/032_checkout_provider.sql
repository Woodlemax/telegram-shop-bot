-- Reserve a rail before issuing any externally payable invoice. Closing a
-- payment page or losing an API response does not invalidate that invoice.
ALTER TABLE orders ADD COLUMN checkout_provider TEXT NOT NULL DEFAULT ''
 CHECK(checkout_provider IN ('','stars','yookassa','crypto','stripe','ton','nowpayments','balance'));
UPDATE orders SET checkout_provider='yookassa'
 WHERE EXISTS(SELECT 1 FROM yookassa_checkout_intents i WHERE i.order_id=orders.id);
UPDATE orders SET checkout_provider=payment_method
 WHERE checkout_provider='' AND payment_method IN ('stars','yookassa','crypto','stripe','ton','nowpayments','balance');
CREATE TRIGGER orders_checkout_provider_immutable
 BEFORE UPDATE OF checkout_provider ON orders
 WHEN OLD.checkout_provider<>'' AND NEW.checkout_provider<>OLD.checkout_provider
 BEGIN SELECT RAISE(ABORT,'checkout provider already reserved'); END;
