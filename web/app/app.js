/* Telegram Mini App shop client. ES5 + fetch, no build step.
   Screens: catalog (categories -> products), product card, cart/checkout.
   All strings come from GET /api/i18n; errors from the API are i18n keys. */
(function () {
  'use strict';

  var tg = window.Telegram && window.Telegram.WebApp;
  var initData = tg ? tg.initData : '';
  var dict = {};
  var cartCount = 0;
  var priceEditors = [];
  var checkoutPending = false;
  var navigationPending = false;
  var cartMutation = Promise.resolve();
  var promoDraft = '';
  var cancelPromoPreview = null;
  var promoEditor = null;

  // Serialize cart writes so an older input cannot win over a newer amount.
  function mutateCart(method, path, body) {
    var request = cartMutation.then(function () { return api(method, path, body); });
    cartMutation = request.catch(function () {});
    return request;
  }

  function flushPriceEditors() {
    return Promise.all(priceEditors.map(function (input) { return input.flushPrice(); }));
  }

  function priceError(err) {
    if (!err.priceReported) { err.priceReported = true; showError(err); }
  }

  // Navigation stack of {render: fn} so the back button always works.
  var navStack = [];
  var expandedProductGroups = {};

  var screenEl = document.getElementById('screen');
  var titleEl = document.getElementById('title');
  var backBtn = document.getElementById('back-btn');
  var homeBtn = document.getElementById('home-btn');
  var ordersBtn = document.getElementById('orders-btn');
  var cartBtn = document.getElementById('cart-btn');
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
    promoEditor = null;
    if (cancelPromoPreview) { cancelPromoPreview(); cancelPromoPreview = null; }
    priceEditors.forEach(function (input) { input.cancelPriceTimer(); });
    priceEditors = [];
    while (screenEl.firstChild) { screenEl.removeChild(screenEl.firstChild); }
  }

  function setTitle(text) {
    titleEl.textContent = text;
  }

  function push(render) {
    if (navigationPending || checkoutPending) { return; }
    navigationPending = true;
    return flushPriceEditors().then(function () {
      navigationPending = false;
      navStack.push(render);
      backBtn.className = navStack.length > 1 ? 'icon-btn' : 'icon-btn hidden';
      render();
    }).catch(function (err) { navigationPending = false; priceError(err); });
  }

  function pop() {
    if (navigationPending || checkoutPending) { return; }
    if (navStack.length <= 1) { return; }
    navigationPending = true;
    return flushPriceEditors().then(function () {
      navigationPending = false;
      navStack.pop();
      backBtn.className = navStack.length > 1 ? 'icon-btn' : 'icon-btn hidden';
      navStack[navStack.length - 1]();
    }).catch(function (err) { navigationPending = false; priceError(err); });
  }

  function goHome() {
    if (navigationPending || checkoutPending) { return; }
    navigationPending = true;
    return flushPriceEditors().then(function () {
      navigationPending = false;
      navStack = [renderCatalog];
      backBtn.className = 'icon-btn hidden';
      renderCatalog();
    }).catch(function (err) { navigationPending = false; priceError(err); });
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
  function openPriceValue(raw) {
    var value = String(raw).trim();
    return /^\d+$/.test(value) && Number(value) <= 1000000 ? Number(value) : null;
  }

  function previewStars(amount, rates) {
    if (amount === 0) { return 0; }
    if (!rates || !(rates.rub_per_usd > 0) || !(rates.stars_per_usd > 0)) { return null; }
    return Math.max(1, Math.floor((amount / rates.rub_per_usd) * rates.stars_per_usd));
  }

  function savePrice(input) {
    return input.flushPrice(true).catch(function (err) { priceError(err); return null; });
  }

  function priceEditor(parent, id, current, done, changed, enabled) {
    var group = el('div', 'open-price');
    var label = el('label', 'product-desc', t('webapp_open_price_hint'));
    var input = el('input', 'input');
    input.type = 'number'; input.min = '0'; input.max = '1000000'; input.step = '1'; input.inputMode = 'numeric';
    input.value = String(current || 0);
    label.appendChild(input); group.appendChild(label);
    var status = el('div', 'price-save-status');
    status.setAttribute('aria-live', 'polite'); group.appendChild(status);
    parent.appendChild(group);
    var saved = enabled && !enabled() ? null : Number(current || 0);
    var timer = null;
    var pending = null;
    input.cancelPriceTimer = function () { if (timer !== null) { clearTimeout(timer); timer = null; } };
    input.flushPrice = function (force) {
      input.cancelPriceTimer();
      if (!force && enabled && !enabled()) { return Promise.resolve(null); }
      if (pending) { return pending.then(function () { return input.flushPrice(force); }); }
      var amount = openPriceValue(input.value);
      if (amount === null) { return Promise.reject(new Error('open_price_invalid')); }
      if (amount === saved) { return Promise.resolve(null); }
      function write() {
        var sent = openPriceValue(input.value);
        if (sent === null || sent === saved) { return Promise.resolve(null); }
        status.textContent = t('webapp_price_saving');
        return mutateCart('POST', '/api/cart', { product_id: id, price: sent }).then(function (cart) {
          saved = sent;
          updateCartBadge(countItems(cart));
          done(cart);
          if (changed) { changed(input.value); }
          // Keep typing enabled and save the newest value after the current request.
          if (openPriceValue(input.value) !== null && openPriceValue(input.value) !== saved) { return write(); }
          return cart;
        });
      }
      pending = write().then(function (cart) {
        pending = null; status.textContent = ''; return cart;
      }, function (err) {
        pending = null; status.textContent = t('webapp_price_save_failed'); throw err;
      });
      return pending;
    };
    input.oninput = function () {
      input.cancelPriceTimer();
      var amount = openPriceValue(input.value);
      status.textContent = amount === null ? t('open_price_invalid') : '';
      if (changed) { changed(input.value); }
      if (amount !== null && (!enabled || enabled())) {
        timer = setTimeout(function () { timer = null; input.flushPrice().catch(priceError); }, 300);
      }
    };
    input.onchange = input.onblur = function () {
      if (openPriceValue(input.value) !== null) { input.flushPrice().catch(priceError); }
    };
    priceEditors.push(input);
    return input;
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
      var orders = el('button', 'btn secondary catalog-orders', t('btn_orders'));
      orders.type = 'button'; orders.onclick = function () { push(renderOrders); };
      screenEl.appendChild(orders);
    }).catch(showError);
  }

  // ---- screen: product list --------------------------------------------------

  function catalogPrice(p) {
    return p.buy_from_author ? rub(p.price_rub) : productPrice(p) + ' / ' + stars(p.price_stars);
  }

  function productListCard(p) {
    var card = el('button', 'card'); card.type = 'button';
    var img = el('img', 'thumb'); loadImage(img, p.photo); card.appendChild(img);
    var info = el('div', 'card-info');
    info.appendChild(el('div', 'card-name', p.name));
    info.appendChild(el('div', 'card-price', catalogPrice(p)));
    if (p.coming_soon) { info.appendChild(el('div', 'product-stock', t('product_coming_soon'))); }
    else if (p.open_price) { info.appendChild(el('div', 'product-desc', t('webapp_open_price_hint'))); }
    card.appendChild(info);
    card.onclick = function () { push(function () { renderProduct(p.id); }); };
    return card;
  }

  function appendProductGroup(list, p) {
    if (!p.modification_count) { list.appendChild(productListCard(p)); return; }
    var group = el('div', 'product-group'); group.appendChild(productListCard(p));
    var toggle = el('button', 'modifications-toggle'); toggle.type = 'button';
    var children = el('div', 'modifications-list hidden');
    children.id = 'modifications-' + p.id;
    toggle.setAttribute('aria-controls', children.id);
    var expanded = !!expandedProductGroups[p.id], loaded = false, pending = false, nextPage = 1;
    var more = null;
    function update() {
      toggle.textContent = (expanded ? '▾ ' : '▸ ') + tf('webapp_modifications', p.modification_count);
      toggle.setAttribute('aria-expanded', expanded ? 'true' : 'false');
      children.className = 'modifications-list' + (expanded ? '' : ' hidden');
      expandedProductGroups[p.id] = expanded;
    }
    function loadNext() {
      if (pending) { return; } pending = true;
      if (more) { children.removeChild(more); more = null; }
      var wait = el('div', 'loading', t('webapp_loading')); children.appendChild(wait);
      api('GET', '/api/products/' + p.id + '/modifications?page=' + nextPage).then(function (data) {
        children.removeChild(wait); pending = false; loaded = true;
        p.modification_count = data.total; update();
        for (var i = 0; i < data.products.length; i++) { children.appendChild(productListCard(data.products[i])); }
        nextPage = data.page + 1;
        if (data.page * data.per_page < data.total) {
          more = el('button', 'btn secondary', t('webapp_modifications_more')); more.type = 'button';
          more.onclick = loadNext; children.appendChild(more);
        }
      }).catch(function (err) {
        children.removeChild(wait); pending = false;
        more = el('button', 'btn secondary', t('webapp_orders_refresh')); more.type = 'button';
        more.onclick = loadNext; children.appendChild(more); showError(err);
      });
    }
    toggle.onclick = function () {
      expanded = !expanded; update();
      if (expanded && !loaded) { loadNext(); }
    };
    group.appendChild(toggle); group.appendChild(children); list.appendChild(group);
    update(); if (expanded) { loadNext(); }
  }

  function renderProducts(cat, page) {
    page = page || 1;
    var activeRender = function () { renderProducts(cat, page); };
    if (navStack.length) { navStack[navStack.length - 1] = activeRender; }
    setTitle((cat.emoji ? cat.emoji + ' ' : '') + cat.name);
    loading();
    api('GET', '/api/products?category=' + cat.id + '&page=' + page).then(function (data) {
      if (navStack[navStack.length - 1] !== activeRender) { return; }
      clearScreen();
      var list = el('div', 'list');
      for (var i = 0; i < data.products.length; i++) {
        appendProductGroup(list, data.products[i]);
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
      var selectedPrice = p.open_price && cartItem ? cartItem : p;
      var priceLine = el('div', 'product-price', catalogPrice(selectedPrice));
      priceLine.setAttribute('aria-live', 'polite');
      screenEl.appendChild(priceLine);
      var priceRates = data.open_price_rates || cart.open_price_rates;
      var customPrice = !p.buy_from_author && p.open_price && !p.coming_soon ? priceEditor(screenEl, p.id, cartItem ? cartItem.price_rub : 0, added, previewPrice, function () { return inCart; }) : null;
      function previewPrice(raw) {
        var amount = openPriceValue(raw);
        if (amount === null) { priceLine.textContent = t('open_price_invalid'); return; }
        var starAmount = previewStars(amount, priceRates);
        if (starAmount !== null) { priceLine.textContent = rub(amount) + ' / ' + stars(starAmount); }
      }
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
      if (p.telegram_url && /^https:\/\/t\.me\//.test(p.telegram_url)) {
        var community = el('a', 'btn secondary product-telegram', t('product_telegram_link'));
        community.href = p.telegram_url;
        community.target = '_blank';
        community.rel = 'noopener noreferrer';
        community.onclick = function (event) {
          if (tg && tg.openTelegramLink) { event.preventDefault(); tg.openTelegramLink(p.telegram_url); }
        };
        screenEl.appendChild(community);
      }
      if (p.buy_from_author) {
        if (/^https:\/\/t\.me\/[A-Za-z][A-Za-z0-9_]*$/.test(p.author_telegram_url || '')) {
          var author = el('a', 'btn primary product-author', t('product_buy_from_author'));
          author.href = p.author_telegram_url; author.target = '_blank'; author.rel = 'noopener noreferrer';
          author.onclick = function (event) {
            if (tg && tg.openTelegramLink) { event.preventDefault(); tg.openTelegramLink(p.author_telegram_url); }
          };
          screenEl.appendChild(author);
        }
        return;
      }
      if (p.coming_soon) { screenEl.appendChild(el('div', 'product-stock', t('product_coming_soon'))); }
      else if (!p.infinite_stock) { screenEl.appendChild(el('div', 'product-stock', tf('webapp_stock', p.stock))); }

      if (p.is_digital) { screenEl.appendChild(el('div', 'product-stock', t('digital_product'))); }
      var add = el('button', 'btn primary', t(inCart ? 'product_go_to_cart' : 'webapp_add_to_cart'));
      add.type = 'button';
      if (p.coming_soon) { add.textContent = t('product_coming_soon'); add.disabled = true; }
      function added(cart) {
        var firstAdd = !inCart;
        updateCartBadge(countItems(cart));
        if (p.open_price) {
          priceRates = cart.open_price_rates || priceRates;
          for (var j = 0; j < cart.items.length; j++) {
            if (cart.items[j].product_id === p.id) {
              var saved = cart.items[j];
              priceLine.textContent = productPrice(saved) + ' / ' + stars(saved.price_stars);
              break;
            }
          }
        }
        inCart = true;
        add.textContent = t('product_go_to_cart');
        add.disabled = false;
        if (firstAdd && tg && tg.HapticFeedback) { tg.HapticFeedback.notificationOccurred('success'); }
      }
      add.onclick = function () {
        if (p.coming_soon) { return; }
        if (inCart) { push(renderCart); return; }
        add.disabled = true;
        if (customPrice) {
          savePrice(customPrice).then(function () { add.disabled = false; }); return;
        }
        mutateCart('POST', '/api/cart', { product_id: p.id, delta: 1 }).then(added).catch(function (err) {
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
        promoDraft = '';
        screenEl.appendChild(el('div', 'empty', t('webapp_cart_empty')));
        return;
      }

      var rows = [];
      var list = el('div', 'list');
      for (var i = 0; i < cart.items.length; i++) {
        (function (item) {
          var row = el('div', 'cart-row');
          function openProduct() { push(function () { renderProduct(item.product_id); }); }
          if (item.photo) {
            var photoLink = el('button', 'cart-product-photo');
            photoLink.type = 'button';
            photoLink.setAttribute('aria-label', item.name);
            var photo = el('img', 'thumb');
            photo.alt = '';
            loadImage(photo, item.photo);
            photoLink.appendChild(photo);
            photoLink.onclick = openProduct;
            row.appendChild(photoLink);
          }
          var info = el('div', 'card-info');
          var productLink = el('button', 'card-name cart-product-link', item.name);
          productLink.type = 'button';
          productLink.onclick = openProduct;
          info.appendChild(productLink);
          var priceLine = el('div', 'card-price');
          priceLine.setAttribute('aria-live', 'polite'); info.appendChild(priceLine);
          var input = item.open_price ? priceEditor(info, item.product_id, item.price_rub, function (updated) {
            cart = updated; updateCartView();
          }, updateCartView) : null;
          rows.push({id: item.product_id, price: priceLine, input: input});
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

      var totalLine = el('div', 'cart-total');
      totalLine.setAttribute('aria-live', 'polite'); screenEl.appendChild(totalLine);

      var promo = el('input', 'input');
      promo.type = 'text';
      promo.placeholder = t('webapp_promo_placeholder');
      promo.value = promoDraft; promoEditor = promo;
      promo.autocomplete = 'off'; promo.spellcheck = false;
      screenEl.appendChild(promo);
      var originalLine = el('div', 'cart-original hidden'); screenEl.appendChild(originalLine);
      var promoStatus = el('div', 'promo-status'); promoStatus.setAttribute('aria-live', 'polite'); screenEl.appendChild(promoStatus);
      var promoTimer = null, promoVersion = 0, promoClosed = false, promoReady = false, validatedPromo = null;
      cancelPromoPreview = function () { promoClosed = true; promoVersion++; if (promoTimer !== null) { clearTimeout(promoTimer); } };
      function stopPreview() {
        promoReady = false;
        promoVersion++;
        if (promoTimer !== null) { clearTimeout(promoTimer); promoTimer = null; }
      }
      function requestPreview(immediate) {
        stopPreview();
        var version = promoVersion, code = promo.value;
        promoStatus.textContent = t('webapp_promo_checking');
        promoTimer = setTimeout(function () {
          promoTimer = null;
          flushPriceEditors().then(function () { return cartMutation; }).then(function () {
            if (promoClosed || version !== promoVersion) { return null; }
            return api('POST', '/api/cart/promo', {promo: code});
          }).then(function (preview) {
            if (!preview || promoClosed || version !== promoVersion) { return; }
            promoReady = true; validatedPromo = code;
            totalLine.textContent = t('webapp_total') + ': ' + rub(preview.total_rub) + ' / ' + stars(preview.total_stars);
            originalLine.className = 'cart-original';
            originalLine.textContent = t('webapp_promo_original') + ': ' + rub(preview.original_total_rub) + ' / ' + stars(preview.original_total_stars);
            promoStatus.textContent = preview.promo ? tf('webapp_promo_applied', preview.promo.code) + ' (−' + preview.promo.discount + '%)' : '';
            while (paymentBox.firstChild) { paymentBox.removeChild(paymentBox.firstChild); }
            renderPayments(preview.free_checkout);
          }).catch(function (err) {
            if (promoClosed || version !== promoVersion) { return; }
            promoStatus.textContent = t(err.message || 'webapp_err_internal');
          });
        }, immediate ? 0 : 300);
      }
      promo.oninput = function () { promoDraft = promo.value; updateCartView(); };
      promo.onchange = promo.onblur = function () {
        promoDraft = promo.value;
        if (promoReady && validatedPromo === promo.value) { return; }
        if (promo.value.trim()) {
          while (paymentBox.firstChild) { paymentBox.removeChild(paymentBox.firstChild); }
          requestPreview(true);
        } else { updateCartView(); }
      };

      var paymentBox = el('div', 'cart-payments'); screenEl.appendChild(paymentBox);
      function updateCartView() {
        var totalRUB = 0, totalStars = 0, valid = true;
        for (var j = 0; j < cart.items.length; j++) {
          var item = cart.items[j];
          for (var k = 0; k < rows.length; k++) {
            var row = rows[k];
            if (row.id !== item.product_id) { continue; }
            var amount = row.input ? openPriceValue(row.input.value) : item.price_rub;
            var starAmount = row.input && amount !== null ? previewStars(amount, cart.open_price_rates) : item.price_stars;
            if (amount === null || starAmount === null) { row.price.textContent = t('open_price_invalid'); valid = false; }
            else {
              row.price.textContent = rub(amount) + ' / ' + stars(starAmount) + ' \u00d7 ' + item.quantity;
              totalRUB += amount * item.quantity; totalStars += starAmount * item.quantity;
            }
            break;
          }
        }
        totalLine.textContent = valid ? t('webapp_total') + ': ' + rub(totalRUB) + ' / ' + stars(totalStars) : t('open_price_invalid');
        while (paymentBox.firstChild) { paymentBox.removeChild(paymentBox.firstChild); }
        originalLine.className = 'cart-original hidden';
        if (promo.value.trim()) {
          if (valid) { requestPreview(false); } else { stopPreview(); promoStatus.textContent = t('open_price_invalid'); }
        } else {
          stopPreview(); promoStatus.textContent = '';
          if (valid) { renderPayments(totalRUB === 0 && totalStars === 0 && (cart.free_checkout || cart.items.some(function (item) { return item.open_price; }))); }
        }
      }
      function renderPayments(previewFree) {
        if (previewFree) {
          var getFree = el('button', 'btn primary', t('free_order_button'));
          getFree.type = 'button'; getFree.disabled = checkoutPending; getFree.onclick = function () { checkout('free', promo.value, getFree); };
          paymentBox.appendChild(getFree); return;
        }

        var payStars = el('button', 'btn primary', t('webapp_pay_stars'));
        payStars.type = 'button';
        payStars.onclick = function () { checkout('stars', promo.value, payStars); };
        paymentBox.appendChild(payStars);

        // Card providers render when enabled by the cart payload.
        if (cart.yookassa_enabled) {
          var payRub = el('button', 'btn secondary', t('webapp_pay_rub'));
          payRub.type = 'button';
          payRub.onclick = function () { checkout('yookassa', promo.value, payRub); };
          paymentBox.appendChild(payRub);
        }

        if (cart.stripe_enabled) {
          var payStripe = el('button', 'btn secondary', t('webapp_pay_stripe'));
          payStripe.type = 'button';
          payStripe.onclick = function () { checkout('stripe', promo.value, payStripe); };
          paymentBox.appendChild(payStripe);
        }

        for (var b = 0; b < paymentBox.children.length; b++) { paymentBox.children[b].disabled = checkoutPending; }
      }
      updateCartView();
    }).catch(showError);
  }

  function changeQty(productID, delta) {
    flushPriceEditors().then(function () { return mutateCart('POST', '/api/cart', { product_id: productID, delta: delta }); })
      .then(function () { renderCart(); })
      .catch(priceError);
  }

  function removeItem(productID) {
    flushPriceEditors().then(function () { return mutateCart('DELETE', '/api/cart?product_id=' + productID); })
      .then(function () { renderCart(); })
      .catch(priceError);
  }

  function checkout(method, promo, btn) {
    if (checkoutPending) { return; }
    checkoutPending = true;
    if (promoEditor) { promoEditor.disabled = true; }
    priceEditors.forEach(function (input) { input.disabled = true; });
    function enableCheckout() {
      checkoutPending = false;
      if (promoEditor) { promoEditor.disabled = false; }
      priceEditors.forEach(function (input) { input.disabled = false; });
      btn.disabled = false;
      var buttons = screenEl.querySelectorAll ? screenEl.querySelectorAll('.cart-payments button') : [];
      for (var i = 0; i < buttons.length; i++) { buttons[i].disabled = false; }
    }
    btn.disabled = true;
    var body = { method: method };
    if (promo) { body.promo = promo; }
    flushPriceEditors().then(function () { return mutateCart('POST', '/api/checkout', body); }).then(function (data) {
      promoDraft = '';
      enableCheckout();
      if (data.free) {
        var message = tf('free_order_done', data.order_id);
        if (tg && tg.showAlert) { tg.showAlert(message); } else { alert(message); }
        renderCart(); return;
      }
      // Checkout has moved the cart into a committed order, even if the
      // buyer closes the invoice. Resume that order instead of checking out
      // the now-empty cart again.
      clearScreen();
      api('GET', '/api/cart').then(function (cart) {
        updateCartBadge(countItems(cart));
      }).catch(function () {});
      var orderRender = function () { renderOrder(data.order_id); };
      return push(orderRender).then(function () {
        if (method === 'stars' && tg && tg.openInvoice) {
          tg.openInvoice(data.invoice_link, function () {
            if (navStack[navStack.length - 1] === orderRender) { orderRender(); }
          });
        } else if (tg && tg.openLink) {
          tg.openLink(data.invoice_link);
        } else {
          window.open(data.invoice_link, '_blank');
        }
      });
    }).catch(function (err) {
      enableCheckout();
      priceError(err);
    });
  }

  // ---- screen: order history ----------------------------------------------------

  function orderTotal(order) {
    var amount = order.display_total_rub != null ? order.display_total_rub : order.total_rub;
    if (amount > 0 || (order.total_usd === 0 && order.total_stars === 0)) { return rub(amount) + ' / ' + stars(order.total_stars); }
    if (order.total_usd > 0) { return usd(order.total_usd) + ' / ' + stars(order.total_stars); }
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
        var row = el('div', 'order-item');
        var itemLabel = item.name + ' × ' + item.quantity;
        if (item.product_id > 0) {
          var productLink = el('button', 'cart-product-link', itemLabel);
          productLink.type = 'button';
          (function (productID, link) {
            link.onclick = function () { push(function () { renderProduct(productID); }); };
          })(item.product_id, productLink);
          row.appendChild(productLink);
        } else {
          row.textContent = itemLabel;
        }
        if (item.download_available) {
          row.appendChild(el('div', 'card-price', item.archive_name));
          (function (productID, parent) {
            var download = el('button', 'btn primary', t('webapp_order_download'));
            download.type = 'button';
            download.onclick = function () { downloadOrderArchive(order.id, productID, download); };
            parent.appendChild(download);
          })(item.product_id, row);
        }
        list.appendChild(row);
      }
      screenEl.appendChild(list);
      screenEl.appendChild(el('div', 'cart-total', t('webapp_total') + ': ' + orderTotal(order)));
      var refresh = el('button', 'btn secondary', t('webapp_orders_refresh'));
      if (order.receipt_url) {
        var receipt = el('button', 'btn primary', t('webapp_order_receipt'));
        receipt.type = 'button';
        receipt.onclick = function () {
          if (tg && tg.openLink) { tg.openLink(order.receipt_url); }
          else { window.open(order.receipt_url, '_blank', 'noopener,noreferrer'); }
        };
        screenEl.appendChild(receipt);
      }
      refresh.type = 'button'; refresh.onclick = function () { renderOrder(id); }; screenEl.appendChild(refresh);
      if (order.status === 'pending' && order.payment_state === 'pending') {
        var resume = el('button', 'btn primary', t('webapp_order_pay_continue'));
        resume.type = 'button';
        resume.onclick = function () { push(function () { renderOrderPayment(id); }); };
        screenEl.appendChild(resume);
        var cancel = el('button', 'btn secondary danger', t('btn_cancel_order'));
        cancel.type = 'button';
        cancel.onclick = function () { cancelOrder(id, cancel); };
        screenEl.appendChild(cancel);
      }
    }).catch(function (err) { historyFailure(err, function () { renderOrder(id); }); });
  }

  function renderOrderPayment(id) {
    var activeRender = navStack[navStack.length - 1];
    setTitle(tf('webapp_order_number', id)); loading();
    api('GET', '/api/orders/' + id).then(function (data) {
      if (navStack[navStack.length - 1] !== activeRender) { return; }
      var order = data.order;
      if (order.status !== 'pending' || order.payment_state !== 'pending') { pop(); return; }
      clearScreen();
      screenEl.appendChild(el('h2', 'product-name', t('webapp_order_pay_continue')));
      screenEl.appendChild(el('div', 'cart-total', t('webapp_total') + ': ' + orderTotal(order)));
      var labels = {stars: 'webapp_pay_stars', yookassa: 'webapp_pay_rub', stripe: 'webapp_pay_stripe', free: 'free_order_button'};
      if (order.checkout_provider && labels[order.checkout_provider]) {
        screenEl.appendChild(el('p', 'product-desc', t('payment_method_selected') + ': ' + t(labels[order.checkout_provider])));
      }
      var methods = order.payment_methods || [];
      var buttons = [];
      for (var i = 0; i < methods.length; i++) {
        (function (method) {
          if (!labels[method]) { return; }
          var pay = el('button', 'btn ' + (buttons.length ? 'secondary' : 'primary'), t(labels[method]));
          pay.type = 'button';
          pay.onclick = function () { resumeOrderPayment(id, method, buttons); };
          buttons.push(pay); screenEl.appendChild(pay);
        })(methods[i]);
      }
      if (!buttons.length) { screenEl.appendChild(el('p', 'product-desc', t('webapp_order_pay_no_methods'))); }
      var refresh = el('button', 'btn secondary', t('webapp_orders_refresh'));
      refresh.type = 'button'; refresh.onclick = function () { renderOrderPayment(id); };
      screenEl.appendChild(refresh);
    }).catch(function (err) { historyFailure(err, function () { renderOrderPayment(id); }); });
  }

  function resumeOrderPayment(id, method, buttons) {
    var activeRender = navStack[navStack.length - 1];
    function refresh() { if (navStack[navStack.length - 1] === activeRender) { renderOrderPayment(id); } }
    function enable() { for (var i = 0; i < buttons.length; i++) { buttons[i].disabled = false; } }
    for (var i = 0; i < buttons.length; i++) { buttons[i].disabled = true; }
    api('POST', '/api/orders/' + id + '/pay', {method: method}).then(function (data) {
      enable();
      if (navStack[navStack.length - 1] !== activeRender) { return; }
      if (data.free) {
        if (tg && tg.showAlert) { tg.showAlert(tf('free_order_done', data.order_id)); }
        else { alert(tf('free_order_done', data.order_id)); }
        refresh(); return;
      }
      if (method === 'stars' && tg && tg.openInvoice) {
        tg.openInvoice(data.invoice_link, refresh);
      } else if (tg && tg.openLink) { tg.openLink(data.invoice_link); refresh(); }
      else { window.open(data.invoice_link, '_blank'); refresh(); }
    }).catch(function (err) {
      enable(); showError(err);
      if (err.message === 'webapp_order_pay_unavailable' || err.message === 'webapp_order_pay_method_unavailable' || err.message === 'payment_method_locked') { refresh(); }
    });
  }

  function cancelOrder(orderID, button) {
    button.disabled = true;
    var confirmed = function (yes) {
      if (!yes) { button.disabled = false; return; }
      api('POST', '/api/orders/' + orderID + '/cancel').then(function () {
        renderOrder(orderID);
      }).catch(function (err) {
        button.disabled = false;
        showError(err);
        if (err.message === 'webapp_order_cancel_unavailable') { renderOrder(orderID); }
      });
    };
    if (tg && tg.showConfirm) { tg.showConfirm(t('webapp_order_cancel_confirm'), confirmed); }
    else { confirmed(window.confirm(t('webapp_order_cancel_confirm'))); }
  }

  function downloadOrderArchive(orderID, productID, button) {
    button.disabled = true;
    api('POST', '/api/orders/' + orderID + '/download', {product_id: productID}).then(function () {
      button.disabled = false;
      if (tg && tg.showAlert) { tg.showAlert(t('webapp_order_download_queued')); }
      else { alert(t('webapp_order_download_queued')); }
    }).catch(function (err) { button.disabled = false; showError(err); });
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
    homeBtn.onclick = goHome;
    if (ordersBtn) { ordersBtn.onclick = function () { push(renderOrders); }; }
    cartBtn.onclick = function () { push(renderCart); };

    var lang = '';
    if (tg && tg.initDataUnsafe && tg.initDataUnsafe.user) {
      lang = tg.initDataUnsafe.user.language_code || '';
    }

    fetch('/api/i18n?lang=' + encodeURIComponent(lang)).then(function (resp) {
      return resp.json();
    }).then(function (data) {
      dict = data || {};
    }).catch(function () {
      dict = {};
    }).then(function () {
      homeBtn.title = t('webapp_home');
      homeBtn.setAttribute('aria-label', t('webapp_home'));
      if (ordersBtn) {
        ordersBtn.title = t('btn_orders');
        ordersBtn.setAttribute('aria-label', t('btn_orders'));
      }
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
