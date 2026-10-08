// Buyer journeys run against the real Mini App script; no payments or dependencies.
const assert = require('node:assert/strict');
const {fixture, drain, dict} = require('./miniapp-open-price.cjs');
const field = f => f.all().find(n => n.tagName === 'input' && n.placeholder === dict.webapp_promo_placeholder);
const total = f => f.all().find(n => n.className === 'cart-total').textContent;
const status = f => f.all().find(n => n.className === 'promo-status').textContent;
const payments = f => f.all().filter(n => n.tagName === 'button' && n.className === 'btn primary');
function type(f, code) { const input = field(f); input.value = code; input.oninput(); }
async function settle(f) { for (let i = 0; i < 4; i++) await f.tick(); }
async function cart(initial) {const f = await fixture(initial); f.nodes['cart-btn'].click(); await drain(); return f;}
async function discounts() {
 const f = await cart([[1,100,1],[2,200,1]]), input = field(f);
 type(f,' save10 '); assert.equal(payments(f).length,0,'Checkout available before promo validation'); await settle(f);
 assert.equal(field(f),input,'Preview remounted promo input'); assert.equal(total(f),dict.webapp_total+': 270.00 ₽ / 135 ⭐');
 assert(status(f).includes('SAVE10'));
 const previewCount=f.requests.filter(r=>r.preview).length;
 field(f).onchange(); field(f).onblur(); assert.equal(payments(f).length,1,'Blur removed validated payment button'); await settle(f);
 assert.equal(f.requests.filter(r=>r.preview).length,previewCount,'Blur revalidated an unchanged ready code'); assert(f.all().some(n => n.className === 'cart-original' && n.textContent.includes('300.00 ₽ / 150 ⭐')));
 assert.equal(f.checkouts(),0,'Preview created an unwanted order');
 type(f,'CATEGORY10'); await settle(f); assert.equal(total(f),dict.webapp_total+': 290.00 ₽ / 145 ⭐');
 type(f,'CATEGORY100'); await settle(f); assert.equal(total(f),dict.webapp_total+': 200.00 ₽ / 100 ⭐'); f.button(dict.webapp_pay_stars);
 f.type(1,'150'); await settle(f); assert.equal(total(f),dict.webapp_total+': 200.00 ₽ / 100 ⭐');
 type(f,'FREE100'); await settle(f); assert.equal(total(f),dict.webapp_total+': 0.00 ₽ / 0 ⭐');
 const free=f.button(dict.free_order_button); free.click(); free.click(); await drain(); assert.equal(f.checkouts(),1); assert.equal(f.requests.find(r=>r.checkout).checkout,'free'); assert.equal(f.invoices.length,0); assert(!f.alerts.includes(dict.webapp_err_internal)); assert(f.alerts.some(text=>text.includes('1')));
}
async function failureAndRetry() {
 const f=await cart([[1,100,1]]);type(f,'UNKNOWN');await settle(f);assert.equal(status(f),dict.promo_not_found);assert.equal(payments(f).length,0);assert.equal(f.alerts.length,0);
 type(f,'');assert.equal(total(f),dict.webapp_total+': 100.00 ₽ / 50 ⭐');f.button(dict.webapp_pay_stars);
 f.failPreview();type(f,'SAVE10');await f.tick();assert.equal(status(f),dict.webapp_err_internal);assert.equal(payments(f).length,0);
 field(f).onblur();await settle(f);assert.equal(total(f),dict.webapp_total+': 90.00 ₽ / 45 ⭐');
 f.expirePromo('SAVE10');f.button(dict.webapp_pay_stars).click();await drain();assert.equal(f.invoices.length,0);assert.equal(f.alerts.at(-1),dict.promo_not_found);
}
async function staleRepliesAndRemoval() {
 const f=await cart([[1,100,1]]);f.holdPreview();type(f,'SAVE10');await f.tick();
 type(f,'FREE100');await f.tick();f.releasePreview(1);await drain();assert.equal(total(f),dict.webapp_total+': 0.00 ₽ / 0 ⭐');
 f.releasePreview(0);await drain();assert.equal(total(f),dict.webapp_total+': 0.00 ₽ / 0 ⭐','Stale reply replaced latest promo');
 type(f,'SAVE10');await f.tick();type(f,'');f.releasePreview(2);await drain();assert.equal(total(f),dict.webapp_total+': 100.00 ₽ / 50 ⭐');f.button(dict.webapp_pay_stars);
 type(f,'FREE100');await f.tick();f.nodes['back-btn'].click();await drain();f.releasePreview(3);await drain();assert.equal(f.nodes.title.textContent,dict.webapp_catalog);assert(!f.all().some(n=>n.className==='promo-status'));
}
async function priceWritesAndNavigation() {
 const f=await cart([[1,100,1]]);f.type(1,'200');type(f,'SAVE10');await settle(f);
 assert.equal(f.cart().items[0].price_rub,200);assert.equal(total(f),dict.webapp_total+': 180.00 ₽ / 90 ⭐');
 assert.equal(f.requests.filter(r=>r.preview).at(-1).amounts[0],200,'Preview raced pending price save');
 f.nodes['back-btn'].click();await drain();f.nodes['cart-btn'].click();await drain();assert.equal(field(f).value,'SAVE10');await settle(f);
 f.button(dict.webapp_pay_stars).click();await drain();assert.equal(f.requests.find(r=>r.checkout).promo,'SAVE10');assert.equal(f.invoices.length,1);
 const z=await cart([[1,0,1]]);type(z,'FREE100');await settle(z);z.button(dict.free_order_button);
}
async function selectedProducts() {
 const f=await cart([[1,100,1],[2,200,1]]);
 type(f,'PRODUCT10');await settle(f);assert.equal(total(f),dict.webapp_total+': 280.00 ₽ / 140 ⭐');
 type(f,'PRODUCT100');await settle(f);assert.equal(total(f),dict.webapp_total+': 100.00 ₽ / 50 ⭐');f.button(dict.webapp_pay_stars);
 f.type(1,'150');await settle(f);assert.equal(total(f),dict.webapp_total+': 150.00 ₽ / 75 ⭐','Unselected price was discounted');
 type(f,'OTHER10');await settle(f);assert.equal(status(f),dict.promo_product_mismatch);assert.equal(payments(f).length,0);
 type(f,'MODELS100');await settle(f);assert.equal(total(f),dict.webapp_total+': 0.00 ₽ / 0 ⭐');
 f.button(dict.free_order_button).click();await drain();assert.equal(f.checkouts(),1);assert.equal(f.invoices.length,0);
}
(async()=>{await selectedProducts();await discounts();await failureAndRetry();await staleRepliesAndRemoval();await priceWritesAndNavigation();console.log('Mini App promo journeys passed: totals, product/category scope, free checkout, stale replies, removal, retries, expiry, pending price saves and navigation.');})().catch(err=>{console.error(err);process.exitCode=1;});
