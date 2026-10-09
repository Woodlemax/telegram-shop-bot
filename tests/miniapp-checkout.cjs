// Checkout consumes the cart before Telegram reports an invoice result.
// Exercise the real Mini App with the same cancelled invoice shown in the video.
const assert = require('node:assert/strict');
const {fixture, drain, dict} = require('./miniapp-open-price.cjs');
const title = id => dict.webapp_order_number.replace('%d', id);
async function start(options = {}) {
  const f = await fixture([[1, 100, 1], [2, 200, 1]], options);
  f.nodes['cart-btn'].click(); await drain();
  const promo = f.all().find(n => n.tagName === 'input' && n.placeholder === dict.webapp_promo_placeholder);
  promo.value = 'SAVE10'; promo.oninput();
  for (let i = 0; i < 4; i++) await f.tick();
  const pay = f.button(dict.webapp_pay_stars);
  pay.click(); pay.click(); await drain();
  assert.equal(f.checkouts(), 1, 'Double click created another order');
  assert.equal(f.cart().items.length, 0, 'Fixture must consume the cart like CreateFromCart');
  assert.equal(f.nodes.title.textContent, title(1), 'Checkout left the consumed cart on screen');
  assert.equal(f.nodes['cart-badge'].className, 'hidden', 'Cart badge still counts ordered items');
  assert(!f.all().some(n => n.className === 'cart-row'), 'Stale cart rows survived checkout');
  assert.equal(f.orders.get(1).total_rub, 270, 'Committed promo total changed');
  return f;
}
async function cancelledThenRetry() {
  const f = await start();
  f.finishInvoice('cancelled'); await drain();
  assert.equal(f.nodes.title.textContent, title(1));
  assert.equal(f.orders.get(1).status, 'pending');
  assert.equal(f.alerts.length, 0, 'Cancellation produced an empty-cart error');
  f.button(dict.webapp_order_pay_continue).click(); await drain();
  const pay = f.button(dict.webapp_pay_stars);
  pay.click(); pay.click(); await drain();
  assert.equal(f.invoices.length, 2, 'Cancelled invoice could not be reopened');
  assert.equal(f.checkouts(), 1, 'Retry created an order from the empty cart');
  assert.deepEqual(f.requests.filter(r => r.pay), [{pay: 1, method: 'stars'}]);
  assert.equal(f.orders.size, 1, 'Retry created a duplicate order');
  assert.equal(f.orders.get(1).total_rub, 270, 'Retry lost the saved discount');
  f.finishInvoice('paid'); await drain();
  assert.equal(f.nodes.title.textContent, title(1));
  assert(!f.all().some(n => n.textContent === dict.webapp_order_pay_continue), 'Paid order still offers another payment');
}
async function invoiceResults() {
  for (const status of ['failed', 'pending', 'paid']) {
    const f = await start(); f.finishInvoice(status); await drain();
    assert.equal(f.nodes.title.textContent, title(1));
    assert.equal(f.checkouts(), 1);
    assert.equal(f.alerts.length, 0);
    assert.equal(f.orders.get(1).status, status === 'paid' ? 'paid' : 'pending');
    if (status !== 'paid') f.button(dict.webapp_order_pay_continue);
  }
}
async function navigationAndExternalPayment() {
  const f = await start(); f.nodes['cart-btn'].click(); await drain();
  assert.equal(f.nodes.title.textContent, dict.webapp_cart);
  assert(f.all().some(n => n.textContent === dict.webapp_cart_empty));
  f.finishInvoice('cancelled'); await drain();
  assert.equal(f.nodes.title.textContent, dict.webapp_cart, 'Late invoice callback replaced the current screen');
  const external = await start({externalPayment: true});
  assert.equal(external.invoices.length, 1);
  external.button(dict.webapp_order_pay_continue);
}
(async () => {
  await cancelledThenRetry(); await invoiceResults(); await navigationAndExternalPayment();
  console.log('Mini App checkout journeys passed: cancellation, same-order retry, frozen promo totals, paid/failed/pending invoices, badge, navigation and external payment.');
})().catch(err => { console.error(err); process.exitCode = 1; });
