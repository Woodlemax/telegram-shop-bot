/* Telegram Mini App shop client. ES5 + fetch, no build step.
   Screens: catalog (categories -> products), product card, cart/checkout.
   All strings come from GET /api/i18n; errors from the API are i18n keys. */
(function () {
  'use strict';

  var tg = window.Telegram && window.Telegram.WebApp;
  var initData = tg ? tg.initData : '';
  var dict = {};
  var cartCount = 0;
  // Navigation stack of {render: fn} so the back button always works.
  var navStack = [];

  var screenEl = document.getElementById('screen');
  var titleEl = document.getElementById('title');
  var backBtn = document.getElementById('back-btn');
  var cartBtn = document.getElementById('cart-btn');
  var ordersBtn = document.getElementById('orders-btn');
  var cartBadge = document.getElementById('cart-badge');

  // ---- i18n ----------------------------------------------------------------

  function t(key) {
    return dict[key] || key;
  }

  // tf('%d items', 3) — sequential %d/%s substitution, mirrors Go's Sprintf use.
  function tf(key) {
    var args = Array.prototype.slice.call(arguments, 1);
    var i = 0;
    return t(key).replace(/%[ds]/g, function (m) {
      return i < args.length ? String(args[i++]) : m;
    });
  }

  // ---- API -----------------------------------------------------------------

  function authHeaders() {
    return { 'Authorization': 'tma ' + initData };
  }

  function api(method, path, body) {
    var opts = { method: method, headers: authHeaders() };
    if (body) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then(function (resp) {
      return resp.json().then(function (data) {
        if (!resp.ok) {
          throw new Error(data && data.error ? data.error : 'webapp_err_internal');
        }
        return data;
      });
    });
  }

  function showError(err) {
    var key = err && err.message ? err.message : 'webapp_err_internal';
    if (tg && tg.showAlert) {
      tg.showAlert(t(key));
    } else {
      alert(t(key));
    }
  }

  // The photo endpoint requires the tma header, so <img src> cannot load it
  // directly — fetch as a blob instead. Public http(s) URLs load as-is.
  function loadImage(img, src) {
    if (!src) { img.className += ' hidden'; return; }
    if (src.indexOf('/api/') !== 0) { img.src = src; return; }
    fetch(src, { headers: authHeaders() }).then(function (resp) {
      if (!resp.ok) { throw new Error('photo'); }
      return resp.blob();
    }).then(function (blob) {
      img.src = URL.createObjectURL(blob);
    }).catch(function () { img.className += ' hidden'; });
  }

  // ---- rendering helpers -----------------------------------------------------

  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) { node.className = className; }
    if (text !== undefined && text !== null) { node.textContent = text; }
    return node;
  }

  function clearScreen() {
    while (screenEl.firstChild) { screenEl.removeChild(screenEl.firstChild); }
  }

  function setTitle(text) {
    titleEl.textContent = text;
  }

  function push(render) {
    navStack.push(render);
    backBtn.className = navStack.length > 1 ? 'icon-btn' : 'icon-btn hidden';
    render();
  }

  function pop() {
    if (navStack.length <= 1) { return; }
    navStack.pop();
    backBtn.className = navStack.length > 1 ? 'icon-btn' : 'icon-btn hidden';
    navStack[navStack.length - 1]();
  }

  function updateCartBadge(count) {
    cartCount = count;
    cartBadge.textContent = String(count);
    cartBadge.className = count > 0 ? '' : 'hidden';
  }

  function countItems(cart) {
    var n = 0;
    for (var i = 0; i < cart.items.length; i++) { n += cart.items[i].quantity; }
    return n;
  }

  function stars(n) {
    return n + ' \u2b50';
  }

  function usd(n) {
    return '$' + n.toFixed(2);
  }

  function rub(n) { return Number(n).toFixed(2) + ' ₽'; }
  function productPrice(p) { return p.price_rub == null ? usd(p.price_usd) : rub(p.price_rub); }
  function savePrice(id, input, done) {
    var value = input.value.trim();
    if (!/^\d+$/.test(value) || Number(value) > 1000000) { showError(new Error('open_price_invalid')); return Promise.resolve(null); }
    return api('POST', '/api/cart', { product_id: id, price: Number(value) }).then(function (cart) {
      updateCartBadge(countItems(cart)); done(cart); return cart;
    }).catch(function (err) { showError(err); return null; });
  }
  function priceEditor(parent, id, current, done) {
    var group = el('div', 'open-price');
    var label = el('label', 'product-desc', t('open_price_hint'));
    var input = el('input', 'input');
    input.type = 'number'; input.min = '0'; input.max = '1000000'; input.step = '1'; input.inputMode = 'numeric';
    input.value = String(current || 0);
    label.appendChild(input); group.appendChild(label);
    var save = el('button', 'btn secondary', t('open_price_apply'));
    save.type = 'button'; save.onclick = function () { savePrice(id, input, done); };
    group.appendChild(save); parent.appendChild(group); return input;
  }

  function loading() {
    clearScreen();
    screenEl.appendChild(el('div', 'loading', t('webapp_loading')));
  }

  // ---- screen: catalog (categories) -----------------------------------------

  function renderCatalog() {
    setTitle(t('webapp_catalog'));
    loading();
    api('GET', '/api/catalog').then(function (data) {
      clearScreen();
    var orders = el('button', 'btn secondary orders-link', t('btn_orders'));
    orders.type = 'button'; orders.onclick = function () { push(renderOrders); };
    screenEl.appendChild(orders);
      var list = el('div', 'list');
      for (var i = 0; i < data.categories.length; i++) {
        (function (cat) {
          var row = el('button', 'row', (cat.emoji ? cat.emoji + ' ' : '') + cat.name);
          row.type = 'button';
          row.onclick = function () { push(function () { renderProducts(cat); }); };
          list.appendChild(row);
        })(data.categories[i]);
      }
      if (!data.categories.length) {
        list.appendChild(el('div', 'empty', t('webapp_empty')));
      }
      screenEl.appendChild(list);
    }).catch(showError);
  }

  // ---- screen: product list --------------------------------------------------

  function renderProducts(cat, page) {
    page = page || 1;
    setTitle((cat.emoji ? cat.emoji + ' ' : '') + cat.name);
    loading();
    api('GET', '/api/products?category=' + cat.id + '&page=' + page).then(function (data) {
      clearScreen();
      var list = el('div', 'list');
      for (var i = 0; i < data.products.length; i++) {
        (function (p) {
          var card = el('button', 'card');
          card.type = 'button';
          var img = el('img', 'thumb');
          loadImage(img, p.photo);
          card.appendChild(img);
          var info = el('div', 'card-info');
          info.appendChild(el('div', 'card-name', p.name));
          info.appendChild(el('div', 'card-price', productPrice(p) + ' / ' + stars(p.price_stars)));
          if (p.open_price) { info.appendChild(el('div', 'product-desc', t('open_price_hint'))); }
          card.appendChild(info);
          card.onclick = function () { push(function () { renderProduct(p.id); }); };
          list.appendChild(card);
        })(data.products[i]);
      }
      if (!data.products.length) {
        list.appendChild(el('div', 'empty', t('webapp_empty')));
      }
      screenEl.appendChild(list);

      var pages = Math.ceil(data.total / data.per_page);
      if (pages > 1) {
        var pager = el('div', 'pager');
        var prev = el('button', 'btn secondary', '\u2039');
        prev.type = 'button';
        prev.disabled = page <= 1;
        prev.onclick = function () { renderProducts(cat, page - 1); };
        var next = el('button', 'btn secondary', '\u203a');
        next.type = 'button';
        next.disabled = page >= pages;
        next.onclick = function () { renderProducts(cat, page + 1); };
        pager.appendChild(prev);
        pager.appendChild(el('span', 'pager-label', page + ' / ' + pages));
        pager.appendChild(next);
        screenEl.appendChild(pager);
      }
    }).catch(showError);
  }

  // ---- screen: product card ---------------------------------------------------

  function renderProduct(id) {
    loading();
    Promise.all([api('GET', '/api/products/' + id), api('GET', '/api/cart')]).then(function (results) {
      var data = results[0];
      var cart = results[1];
      var p = data.product;
      var cartItem = null;
      for (var c = 0; c < cart.items.length; c++) {
        if (cart.items[c].product_id === p.id && cart.items[c].quantity > 0) { cartItem = cart.items[c]; break; }
      }
      var inCart = !!cartItem;
      updateCartBadge(countItems(cart));
      setTitle(p.name);
      clearScreen();

      if (data.photos.length) {
        var gallery = el('div', 'gallery');
        for (var i = 0; i < data.photos.length; i++) {
          var img = el('img', 'photo');
          loadImage(img, data.photos[i]);
          gallery.appendChild(img);
        }
        screenEl.appendChild(gallery);
      }

      screenEl.appendChild(el('h2', 'product-name', p.name));
      screenEl.appendChild(el('div', 'product-price', productPrice(p) + ' / ' + stars(p.price_stars)));
      var customPrice = p.open_price ? priceEditor(screenEl, p.id, cartItem ? cartItem.price_rub : 0, added) : null;
      if (data.rating_count > 0) {
        screenEl.appendChild(el('div', 'product-rating',
          '\u2605 ' + data.rating_avg.toFixed(1) + ' \u00b7 ' + tf('webapp_reviews', data.rating_count)));
      }
      if (p.sub_period_days) {
        screenEl.appendChild(el('div', 'product-sub', tf('webapp_subscription', p.sub_period_days)));
      }
      if (p.description) {
        screenEl.appendChild(el('p', 'product-desc', p.description));
      }
      screenEl.appendChild(el('div', 'product-stock', p.infinite_stock ? t('product_infinite_stock') : tf('webapp_stock', p.stock)));

      if (p.is_digital) { screenEl.appendChild(el('div', 'product-stock', t('digital_product'))); }
      if (p.single_in_cart) { screenEl.appendChild(el('div', 'product-stock', t('product_single_in_cart'))); }
      var add = el('button', 'btn primary', t(inCart ? 'product_go_to_cart' : 'webapp_add_to_cart'));
      add.type = 'button';
      function added(cart) {
        updateCartBadge(countItems(cart));
        inCart = true;
        add.textContent = t('product_go_to_cart');
        add.disabled = false;
        if (tg && tg.HapticFeedback) { tg.HapticFeedback.notificationOccurred('success'); }
      }
      add.onclick = function () {
        if (inCart) { push(renderCart); return; }
        add.disabled = true;
        if (customPrice) {
          savePrice(p.id, customPrice, added).then(function () { add.disabled = false; }); return;
        }
        api('POST', '/api/cart', { product_id: p.id, delta: 1 }).then(added).catch(function (err) {
          add.disabled = false;
          showError(err);
        });
      };
      screenEl.appendChild(add);
    }).catch(showError);
  }

  // ---- screen: cart + checkout -------------------------------------------------

  function renderCart() {
    setTitle(t('webapp_cart'));
    loading();
    api('GET', '/api/cart').then(function (cart) {
      clearScreen();
      updateCartBadge(countItems(cart));

      if (!cart.items.length) {
        screenEl.appendChild(el('div', 'empty', t('webapp_cart_empty')));
        return;
      }

      var list = el('div', 'list');
      for (var i = 0; i < cart.items.length; i++) {
        (function (item) {
          var row = el('div', 'cart-row');
          var info = el('div', 'card-info');
          info.appendChild(el('div', 'card-name', item.name));
          info.appendChild(el('div', 'card-price', productPrice(item) + ' \u00d7 ' + item.quantity));
          if (item.open_price) { priceEditor(info, item.product_id, item.price_rub, renderCart); }
          row.appendChild(info);

          var controls = el('div', 'qty-controls');
          var minus = el('button', 'icon-btn', '\u2212');
          minus.type = 'button';
          minus.onclick = function () { changeQty(item.product_id, -1); };
          var qty = el('span', 'qty', String(item.quantity));
          var plus = el('button', 'icon-btn', '+');
          plus.type = 'button';
          plus.disabled = !!item.single_in_cart;
          plus.onclick = function () { changeQty(item.product_id, 1); };
          var del = el('button', 'icon-btn danger', '\u00d7');
          del.type = 'button';
          del.onclick = function () { removeItem(item.product_id); };
          controls.appendChild(minus);
          controls.appendChild(qty);
          controls.appendChild(plus);
          controls.appendChild(del);
          row.appendChild(controls);
          list.appendChild(row);
        })(cart.items[i]);
      }
      screenEl.appendChild(list);

      screenEl.appendChild(el('div', 'cart-total',
        t('webapp_total') + ': ' + rub(cart.total_rub) + ' / ' + stars(cart.total_stars)));

      var promo = el('input', 'input');
      promo.type = 'text';
      promo.placeholder = t('webapp_promo_placeholder');
      screenEl.appendChild(promo);

      if (cart.free_checkout) {
        var getFree = el('button', 'btn primary', t('free_order_button'));
        getFree.type = 'button'; getFree.onclick = function () { checkout('free', promo.value, getFree); };
        screenEl.appendChild(getFree); return;
      }

      var payStars = el('button', 'btn primary', t('webapp_pay_stars'));
      payStars.type = 'button';
      payStars.onclick = function () { checkout('stars', promo.value, payStars); };
      screenEl.appendChild(payStars);

      if (!cart.stars_only) {
      var payCrypto = el('button', 'btn secondary', t('webapp_pay_crypto'));
      payCrypto.type = 'button';
      payCrypto.onclick = function () { checkout('crypto', promo.value, payCrypto); };
      screenEl.appendChild(payCrypto);
      }

      // The four newer rails render only when the cart payload marks them
      // enabled (rail available, non-subscription cart, positive converted
      // total) — same visibility rules as the bot's payment keyboard.
      if (cart.yookassa_enabled) {
        var payRub = el('button', 'btn secondary', t('webapp_pay_rub'));
        payRub.type = 'button';
        payRub.onclick = function () { checkout('yookassa', promo.value, payRub); };
        screenEl.appendChild(payRub);
      }

      if (cart.stripe_enabled) {
        var payStripe = el('button', 'btn secondary', t('webapp_pay_stripe'));
        payStripe.type = 'button';
        payStripe.onclick = function () { checkout('stripe', promo.value, payStripe); };
        screenEl.appendChild(payStripe);
      }

      if (cart.ton_enabled) {
        var payTon = el('button', 'btn secondary', t('webapp_pay_ton'));
        payTon.type = 'button';
        payTon.onclick = function () { checkout('ton', promo.value, payTon); };
        screenEl.appendChild(payTon);
      }

      if (cart.nowpayments_enabled) {
        var payNowp = el('button', 'btn secondary', t('webapp_pay_nowpayments'));
        payNowp.type = 'button';
        payNowp.onclick = function () { checkout('nowpayments', promo.value, payNowp); };
        screenEl.appendChild(payNowp);
      }
    }).catch(showError);
  }

  function changeQty(productID, delta) {
    api('POST', '/api/cart', { product_id: productID, delta: delta })
      .then(function () { renderCart(); })
      .catch(showError);
  }

  function removeItem(productID) {
    api('DELETE', '/api/cart?product_id=' + productID)
      .then(function () { renderCart(); })
      .catch(showError);
  }

  function checkout(method, promo, btn) {
    btn.disabled = true;
    var body = { method: method };
    if (promo) { body.promo = promo; }
    api('POST', '/api/checkout', body).then(function (data) {
      btn.disabled = false;
      if (data.free) {
        var message = tf('free_order_done', data.order_id);
        if (tg && tg.showAlert) { tg.showAlert(message); } else { alert(message); }
        renderCart(); return;
      }
      if (method === 'stars' && tg && tg.openInvoice) {
        tg.openInvoice(data.invoice_link, function (status) {
          if (status === 'paid') { renderCart(); }
        });
      } else if (tg && tg.openLink) {
        tg.openLink(data.invoice_link);
      } else {
        window.open(data.invoice_link, '_blank');
      }
    }).catch(function (err) {
      btn.disabled = false;
      showError(err);
    });
  }

  // ---- screen: order history ----------------------------------------------------

  function orderTotal(order) {
    if (order.total_rub > 0 || (order.total_usd === 0 && order.total_stars === 0)) { return rub(order.total_rub); }
    if (order.total_usd > 0) { return usd(order.total_usd); }
    return stars(order.total_stars);
  }

  function orderStatus(order) {
    var special = {refunded: 'webapp_order_refunded', partially_refunded: 'webapp_order_partial_refund', needs_review: 'webapp_order_review', cancelled: 'order_status_cancelled'};
    if (special[order.payment_state]) { return t(special[order.payment_state]); }
    if (order.payment_method === 'free' && order.status === 'paid') { return t('webapp_order_free'); }
    var key = 'order_status_' + order.status;
    return dict[key] || t('webapp_order_unknown');
  }

  function orderDate(value) {
    var date = new Date(value);
    return isNaN(date.getTime()) || date.getFullYear() < 2000 ? t('webapp_order_date_unknown') : date.toLocaleString();
  }

  function historyFailure(err, retry) {
    clearScreen();
    screenEl.appendChild(el('div', 'empty', t('error_load_orders')));
    var button = el('button', 'btn secondary', t('webapp_orders_refresh'));
    button.type = 'button'; button.onclick = retry; screenEl.appendChild(button);
    showError(err);
  }

  function renderOrders(page) {
    page = page || 1;
  if (navStack.length) { navStack[navStack.length - 1] = function () { renderOrders(page); }; }
    setTitle(t('btn_orders')); loading();
    api('GET', '/api/orders?page=' + page).then(function (data) {
      clearScreen();
      var refresh = el('button', 'btn secondary orders-link', t('webapp_orders_refresh'));
      refresh.type = 'button'; refresh.onclick = function () { renderOrders(page); }; screenEl.appendChild(refresh);
      var list = el('div', 'list');
      for (var i = 0; i < data.orders.length; i++) {
        (function (order) {
          var row = el('button', 'order-card'); row.type = 'button';
          row.appendChild(el('div', 'order-heading', tf('webapp_order_number', order.id) + ' · ' + orderTotal(order)));
          row.appendChild(el('div', 'order-status', orderStatus(order)));
          row.appendChild(el('div', 'card-price', orderDate(order.created_at)));
          var names = [];
          for (var j = 0; j < order.items.length; j++) { names.push(order.items[j].name + ' × ' + order.items[j].quantity); }
          row.appendChild(el('div', 'order-summary', names.join(', ')));
          row.onclick = function () { push(function () { renderOrder(order.id); }); };
          list.appendChild(row);
        })(data.orders[i]);
      }
      if (!data.orders.length) { list.appendChild(el('div', 'empty', t('orders_empty'))); }
      screenEl.appendChild(list);
      var pages = Math.ceil(data.total / data.per_page);
      if (pages > 1) {
        var pager = el('div', 'pager');
        var prev = el('button', 'btn secondary', '\u2039'); prev.type = 'button'; prev.disabled = page <= 1;
        prev.onclick = function () { renderOrders(page - 1); };
        var next = el('button', 'btn secondary', '\u203a'); next.type = 'button'; next.disabled = page >= pages;
        next.onclick = function () { renderOrders(page + 1); };
        pager.appendChild(prev); pager.appendChild(el('span', 'pager-label', page + ' / ' + pages)); pager.appendChild(next);
        screenEl.appendChild(pager);
      }
    }).catch(function (err) { historyFailure(err, function () { renderOrders(page); }); });
  }

  function renderOrder(id) {
    setTitle(tf('webapp_order_number', id)); loading();
    api('GET', '/api/orders/' + id).then(function (data) {
      var order = data.order;
      clearScreen();
      screenEl.appendChild(el('h2', 'product-name', tf('webapp_order_number', order.id)));
      screenEl.appendChild(el('div', 'order-status', orderStatus(order)));
      screenEl.appendChild(el('div', 'product-stock', orderDate(order.created_at)));
      var method = dict['payment_method_' + order.payment_method] || t('webapp_order_payment_unknown');
      screenEl.appendChild(el('p', 'product-desc', tf('webapp_order_payment', method)));
      screenEl.appendChild(el('h3', 'order-heading', t('webapp_order_items')));
      var list = el('div', 'list');
      for (var i = 0; i < order.items.length; i++) {
        var item = order.items[i];
        list.appendChild(el('div', 'order-item', item.name + ' × ' + item.quantity));
      }
      screenEl.appendChild(list);
      screenEl.appendChild(el('div', 'cart-total', t('webapp_total') + ': ' + orderTotal(order)));
      if (order.payment_method === 'stars') { screenEl.appendChild(el('div', 'card-price', stars(order.total_stars))); }
      var refresh = el('button', 'btn secondary', t('webapp_orders_refresh'));
      refresh.type = 'button'; refresh.onclick = function () { renderOrder(id); }; screenEl.appendChild(refresh);
    }).catch(function (err) { historyFailure(err, function () { renderOrder(id); }); });
  }

  // ---- boot ---------------------------------------------------------------------

  function applyTheme() {
    if (!tg) { return; }
    var params = tg.themeParams || {};
    var root = document.documentElement.style;
    if (params.bg_color) { root.setProperty('--bg', params.bg_color); }
    if (params.text_color) { root.setProperty('--text', params.text_color); }
    if (params.hint_color) { root.setProperty('--hint', params.hint_color); }
    if (params.button_color) { root.setProperty('--button', params.button_color); }
    if (params.button_text_color) { root.setProperty('--button-text', params.button_text_color); }
    if (params.secondary_bg_color) { root.setProperty('--secondary-bg', params.secondary_bg_color); }
    if (params.link_color) { root.setProperty('--link', params.link_color); }
  }

  function boot() {
    if (tg) {
      tg.ready();
      tg.expand();
      applyTheme();
      tg.onEvent('themeChanged', applyTheme);
    }

    backBtn.onclick = pop;
    cartBtn.onclick = function () { push(renderCart); };
  ordersBtn.onclick = function () { push(renderOrders); };

    var lang = '';
    if (tg && tg.initDataUnsafe && tg.initDataUnsafe.user) {
      lang = tg.initDataUnsafe.user.language_code || '';
    }

    fetch('/api/i18n?lang=' + encodeURIComponent(lang)).then(function (resp) {
      return resp.json();
    }).then(function (data) {
      dict = data || {};
    ordersBtn.title = t('btn_orders'); ordersBtn.setAttribute('aria-label', t('btn_orders'));
    }).catch(function () {
      dict = {};
    }).then(function () {
      setTitle(t('webapp_title'));
      push(renderCatalog);
      // Prime the cart badge in the background.
      api('GET', '/api/cart').then(function (cart) {
        updateCartBadge(countItems(cart));
      }).catch(function () {});
    });
  }

  boot();
})();
