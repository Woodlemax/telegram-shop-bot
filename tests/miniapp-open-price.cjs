// Run with node tests/miniapp-open-price.cjs; no dependencies or real payments.
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm'), assert = require('node:assert/strict');
const repo = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(repo, 'web/app/app.js'), 'utf8');
const dict = JSON.parse(fs.readFileSync(path.join(repo, 'locales/ru.json'), 'utf8'));
class Element {
  constructor(tag) { this.tagName = tag; this.children = []; this.className = ''; this.textContent = ''; this.disabled = false; }
  appendChild(child) { this.children.push(child); return child; }
  removeChild(child) { this.children.splice(this.children.indexOf(child), 1); }
  get firstChild() { return this.children[0]; }
  setAttribute(key, value) { this[key] = value; }
  click() { if (!this.disabled && this.onclick) this.onclick(); }
  querySelectorAll() { return flatten(this).filter(n => n.tagName === 'button'); }
}
function flatten(node) { return [node, ...node.children.flatMap(flatten)]; }
async function drain() { for (let i = 0; i < 12; i++) await new Promise(setImmediate); }
async function fixture(initial = [[1, 100, 1]], options = {}) {
  const nodes = Object.fromEntries(['screen', 'title', 'back-btn', 'home-btn', 'cart-btn', 'cart-badge'].map(id => [id, new Element('div')]));
  const products = [1, 2].map(id => ({id, name: 'Plane ' + id, description: 'Model', price_rub: 0, price_stars: 0, stock: 0, infinite_stock: true, single_in_cart: true, open_price: true}));
  const timers = new Map(), alerts = [], requests = [], invoices = [], invoiceCallbacks = [], orders = new Map();
  let timerID = 0, rate = 100, held = false, release, fail = false, checkouts = 0;
  let previewHeld = false, previewFail = false; const previewReleases = [], unavailable = new Set();
  let items = initial.map(([id, amount, quantity]) => ({product_id: id, price_rub: amount, quantity, name: 'Plane ' + id, open_price: true, single_in_cart: true}));
  const stars = n => n ? Math.max(1, Math.floor(n / rate * 50)) : 0;
  const cart = () => ({items: items.map(item => ({...item, price_stars: stars(item.price_rub)})), total_rub: items.reduce((n, item) => n + item.price_rub * item.quantity, 0), total_stars: items.reduce((n, item) => n + stars(item.price_rub) * item.quantity, 0), stars_only: true, free_checkout: items.length > 0 && items.every(item => item.price_rub === 0), open_price_rates: {rub_per_usd: rate, stars_per_usd: 50}});
  const codes = {
    PRODUCT10: {code: 'PRODUCT10', discount: 10, product_ids: [2]},
    PRODUCT100: {code: 'PRODUCT100', discount: 100, product_ids: [2]},
    MODELS100: {code: 'MODELS100', discount: 100, product_ids: [1,2]},
    OTHER10: {code: 'OTHER10', discount: 10, product_ids: [99]},
    SAVE10: {code: 'SAVE10', discount: 10},
    FREE100: {code: 'FREE100', discount: 100},
    CATEGORY10: {code: 'CATEGORY10', discount: 10, category_id: 1},
    CATEGORY100: {code: 'CATEGORY100', discount: 100, category_id: 1}
  };
  function quote(code, snapshot = cart()) {
    const promo = codes[String(code || '').trim().toUpperCase()];
    if (code && (!promo || unavailable.has(promo.code))) return {error: 'promo_not_found'};
    if (!promo) return snapshot;
    let eligibleRub = 0, eligibleStars = 0, matched = false;
    for (const item of snapshot.items) {
      if (promo.category_id && item.product_id !== promo.category_id) continue;
      if (promo.product_ids && !promo.product_ids.includes(item.product_id)) continue;
      matched = true; eligibleRub += item.price_rub * item.quantity; eligibleStars += item.price_stars * item.quantity;
    }
    if (!matched) return {error: promo.product_ids ? 'promo_product_mismatch' : 'promo_category_mismatch'};
    const rub = Math.round((snapshot.total_rub - eligibleRub + eligibleRub * (100 - promo.discount) / 100) * 100) / 100;
    const totalStars = Math.max(rub > 0 ? 1 : 0, snapshot.total_stars - eligibleStars + Math.floor(eligibleStars * (100 - promo.discount) / 100));
    return {...snapshot, promo, original_total_rub: snapshot.total_rub, original_total_stars: snapshot.total_stars, total_rub: rub, total_stars: totalStars, free_checkout: rub === 0 && totalStars === 0};
  }
  const fetch = async (url, opts = {}) => {
    let data, ok = true;
    if (url.startsWith('/api/i18n')) data = dict;
    else if (url === '/api/catalog') data = {categories: [{id: 1, name: 'Planes'}]};
    else if (url.startsWith('/api/products?')) data = {products, total: 2, per_page: 10};
    else if (url.startsWith('/api/products/')) data = {product: products.find(p => p.id === Number(url.split('/').pop())), photos: [], rating_count: 0, open_price_rates: {rub_per_usd: rate, stars_per_usd: 50}};
    else if (url === '/api/cart/promo') {
      const body = JSON.parse(opts.body), snapshot = cart(); requests.push({preview: body.promo, amounts: snapshot.items.map(item => item.price_rub)});
      const response = quote(body.promo, snapshot);
      if (previewHeld) await new Promise(resolve => {previewReleases.push(resolve);});
      data = previewFail ? {error: 'webapp_err_internal'} : response; previewFail = false; ok = !data.error;
    }
    else if (url === '/api/cart' && opts.method === 'POST') {
      const body = JSON.parse(opts.body); requests.push(body);
      if (held) await new Promise(resolve => { release = resolve; });
      if (fail) { fail = false; ok = false; data = {error: 'webapp_err_internal'}; }
      else {
        let item = items.find(item => item.product_id === body.product_id);
        if (!item) { item = {product_id: body.product_id, quantity: 1, name: 'Plane ' + body.product_id, open_price: true, single_in_cart: true}; items.push(item); }
        if (body.price !== undefined) item.price_rub = body.price;
        if (body.delta) item.quantity += body.delta;
        items = items.filter(item => item.quantity > 0); data = cart();
      }
    } else if (url.startsWith('/api/cart?') && opts.method === 'DELETE') {
      const id = Number(url.split('=').pop()); requests.push({delete: id}); items = items.filter(item => item.product_id !== id); data = cart();
    } else if (url === '/api/cart') data = cart();
    else if (url === '/api/checkout') {
      const body = JSON.parse(opts.body), result = quote(body.promo); checkouts++; requests.push({checkout: body.method, promo: body.promo, amounts: items.map(item => item.price_rub)});
      if (result.error) { ok = false; data = result; }
      else {
        assert.equal(body.method, result.free_checkout ? 'free' : 'stars', 'Checkout used an obsolete free/paid action');
        const id = orders.size + 1;
        orders.set(id, {id, status: result.free_checkout ? 'paid' : 'pending', payment_state: result.free_checkout ? 'confirmed' : 'pending', payment_method: result.free_checkout ? 'free' : '', total_rub: result.total_rub, total_usd: result.total_rub / rate, total_stars: result.total_stars, created_at: '2026-10-09T06:00:00Z', items: items.map(item => ({product_id: item.product_id, name: item.name, quantity: item.quantity})), payment_methods: ['stars']});
        items = []; // Real CreateFromCart consumes the cart before invoice creation.
        data = result.free_checkout ? {free: true, order_id: id} : {order_id: id, invoice_link: 'test-invoice-' + id};
      }
    } else if (/^\/api\/orders\/\d+$/.test(url)) {
      const id = Number(url.split('/').pop());
      data = {order: {...orders.get(id)}};
    } else if (/^\/api\/orders\/\d+\/pay$/.test(url)) {
      const id = Number(url.split('/')[3]), order = orders.get(id);
      const body = JSON.parse(opts.body); requests.push({pay: id, method: body.method});
      if (order.status !== 'pending') { ok = false; data = {error: 'webapp_order_pay_unavailable'}; }
      else data = {order_id: id, invoice_link: 'test-invoice-' + id};
    } else throw Error('Unexpected request ' + url);
    return {ok, json: async () => data};
  };
  const tg = {initData: 'test', ready() {}, expand() {}, onEvent() {}, showAlert(text) { alerts.push(text); }, openInvoice(link, callback) { invoices.push(link); invoiceCallbacks.push(callback); }};
  if (options.externalPayment) { delete tg.openInvoice; tg.openLink = link => invoices.push(link); }
  vm.runInNewContext(source, {window: {Telegram: {WebApp: tg}}, document: {getElementById: id => nodes[id], createElement: tag => new Element(tag), documentElement: {style: {setProperty() {}}}}, fetch, Promise, URL, alert: text => alerts.push(text), setTimeout: fn => {timers.set(++timerID, fn); return timerID;}, clearTimeout: id => timers.delete(id)});
  const all = () => flatten(nodes.screen);
  const button = text => {const node = all().find(n => n.tagName === 'button' && n.textContent === text); assert(node, 'Missing button ' + text); return node;};
  const input = id => {if (nodes.title.textContent.startsWith('Plane ')) return all().find(n => n.type === 'number'); const row = all().filter(n => n.className === 'cart-row').find(n => flatten(n).some(c => c.textContent === 'Plane ' + id)); return row && flatten(row).find(n => n.type === 'number');};
  const type = (id, value) => {const field = input(id); assert(!field.disabled, 'Input lost editability'); field.value = value; field.oninput();};
  const tick = async () => {const callbacks = [...timers.values()]; timers.clear(); callbacks.forEach(fn => fn()); await drain();};
  const product = async id => {button('Planes').click(); await drain(); all().filter(n => n.className === 'card')[id - 1].click(); await drain();};
  await drain();
  return {nodes, all, button, input, type, tick, product, requests, alerts, invoices, cart, orders,
    finishInvoice: (status, index = invoiceCallbacks.length - 1) => {
      const id = Number(invoices[index].split('-').pop());
      if (status === 'paid') { const order = orders.get(id); order.status = 'paid'; order.payment_state = 'confirmed'; order.payment_method = 'stars'; }
      invoiceCallbacks[index](status);
    }, holdPreview: () => {previewHeld = true;}, releasePreview: index => {previewReleases[index]();}, unholdPreview: () => {previewHeld = false;}, failPreview: () => {previewFail = true;}, expirePromo: code => {unavailable.add(code);}, setRate: n => {rate = n;}, hold: () => {held = true;}, release: () => {held = false; release();}, fail: () => {fail = true;}, checkouts: () => checkouts};
}
async function productAutosave() {
  const f = await fixture([]); await f.product(1);
  assert(!f.all().some(n => n.textContent === dict.open_price_apply));
  assert(f.all().some(n => n.textContent === 'Свободная цена: от 0 ₽.'));
  f.type(1, '100'); assert.equal(f.all().find(n => n.className === 'product-price').textContent, '100.00 ₽ / 50 ⭐');
  await f.tick(); assert.equal(f.requests.length, 0, 'A draft added an unwanted cart item');
  f.button(dict.webapp_add_to_cart).click(); f.button(dict.webapp_add_to_cart).click(); await drain(); assert.equal(f.requests.length, 1);
  f.hold(); f.type(1, '150'); await f.tick(); assert.equal(f.requests.at(-1).price, 150);
  f.type(1, '200'); await f.tick(); assert.equal(f.requests.length, 2, 'Concurrent price writes escaped serialization');
  f.setRate(200); f.release(); await drain();
  assert.equal(f.requests.at(-1).price, 200); assert.equal(f.cart().items[0].price_rub, 200);
  assert.equal(f.input(1).value, '200'); assert.equal(f.all().find(n => n.className === 'product-price').textContent, '200.00 ₽ / 50 ⭐');
  f.type(1, '75'); f.button(dict.product_go_to_cart).click(); await drain();
  assert.equal(f.cart().items[0].price_rub, 75, 'Navigation lost the latest draft'); assert.equal(f.nodes.title.textContent, dict.webapp_cart);
  f.nodes['back-btn'].click(); await drain(); assert.equal(f.input(1).value, '75');
}
async function cartAutosave() {
  const f = await fixture([[1, 100, 2], [2, 20, 1]]); f.nodes['cart-btn'].click(); await drain();
  const first = f.input(1), second = f.input(2), total = () => f.all().find(n => n.className === 'cart-total').textContent;
  f.type(1, '10'); f.type(2, '30'); assert.equal(total(), dict.webapp_total + ': 50.00 ₽ / 25 ⭐');
  await f.tick(); assert.equal(f.cart().items[0].price_rub, 10); assert.equal(f.cart().items[1].price_rub, 30);
  assert.equal(f.input(1), first); assert.equal(f.input(2), second, 'Autosave replaced the focused input');
  f.type(1, '0'); f.type(2, '0'); f.button(dict.free_order_button); await f.tick(); assert(f.cart().free_checkout);
  f.type(1, '1'); assert.equal(total(), dict.webapp_total + ': 2.00 ₽ / 2 ⭐'); f.button(dict.webapp_pay_stars);
  f.type(1, '1000000'); assert.equal(total(), dict.webapp_total + ': 2000000.00 ₽ / 1000000 ⭐');
  for (const invalid of ['', '-1', '1.5', '1e2', '1000001']) {f.type(1, invalid); assert.equal(total(), dict.open_price_invalid); assert(!f.all().some(n => n.className === 'btn primary')); await f.tick();}
  assert.equal(f.cart().items[0].price_rub, 0, 'Invalid input mutated the cart');
  f.type(1, '100'); f.fail(); await f.tick(); assert.equal(f.cart().items[0].price_rub, 0); assert.equal(f.alerts.length, 1);
  f.input(1).onblur(); await drain(); assert.equal(f.cart().items[0].price_rub, 100, 'A failed autosave could not retry');
  f.type(1, '250'); f.hold(); const pay = f.button(dict.webapp_pay_stars); pay.click(); pay.click(); await drain();
  assert.equal(f.checkouts(), 0, 'Checkout raced a pending price save');
  f.release(); await drain(); assert.equal(f.checkouts(), 1); assert.equal(f.requests.at(-1).amounts[0], 250); assert.equal(f.invoices.length, 1);
  const removal = await fixture([[1, 250, 1]]); removal.nodes['cart-btn'].click(); await drain();
  removal.type(1, '300'); const row = removal.all().find(n => n.className === 'cart-row'); flatten(row).find(n => n.className === 'icon-btn danger').click(); await drain();
  assert(!removal.cart().items.some(item => item.product_id === 1)); assert.equal(removal.requests.at(-1).delete, 1, 'An autosave re-added a removed product');
  await removal.tick(); assert(!removal.cart().items.some(item => item.product_id === 1));
}
async function checkoutFlushFailureAndFree() {
  const f = await fixture([[1, 0, 1]]); f.nodes['cart-btn'].click(); await drain();
  f.type(1, '100'); f.fail(); f.button(dict.webapp_pay_stars).click(); await drain();
  assert.equal(f.checkouts(), 0); assert.equal(f.cart().items[0].price_rub, 0); assert.equal(f.input(1).disabled, false);
  f.button(dict.webapp_pay_stars).click(); await drain(); assert.equal(f.checkouts(), 1); assert.equal(f.requests.at(-1).amounts[0], 100);
  const free = await fixture([[1, 0, 1]]); free.nodes['cart-btn'].click(); await drain(); free.button(dict.free_order_button).click(); await drain(); assert.equal(free.checkouts(), 1); assert.equal(free.requests.findLast(r => r.checkout).checkout, 'free');
}
async function quantityAndInvalidNavigation() {
  const f = await fixture([[1, 100, 2]]); f.nodes['cart-btn'].click(); await drain();
  f.type(1, '200'); f.hold(); await f.tick();
  const row = f.all().find(n => n.className === 'cart-row'); flatten(row).find(n => n.className === 'icon-btn' && n.textContent === '\u2212').click(); await drain();
  assert.equal(f.requests.length, 1); f.release(); await drain();
  assert.equal(f.cart().items[0].quantity, 1); assert.equal(f.cart().total_rub, 200); assert.equal(f.requests.at(-1).delta, -1);
  f.type(1, ''); f.nodes['back-btn'].click(); await drain(); assert.equal(f.nodes.title.textContent, dict.webapp_cart); assert.equal(f.alerts.length, 1);
  f.type(1, '300'); f.hold(); f.nodes['back-btn'].click(); f.nodes['back-btn'].click(); await drain(); f.release(); await drain();
  assert.equal(f.nodes.title.textContent, dict.webapp_catalog, 'Repeated navigation escaped its save barrier'); assert.equal(f.cart().items[0].price_rub, 300);
}
if (require.main === module) (async () => {await productAutosave(); await cartAutosave(); await checkoutFlushFailureAndFree(); await quantityAndInvalidNavigation(); console.log('Mini App open-price tests passed: preview, autosave, cart totals, preserved input, concurrent edits, navigation, checkout, zero/positive, invalid input, retries, quantity and removal.');})().catch(err => {console.error(err); process.exitCode = 1;});

module.exports = {fixture, drain, dict};
