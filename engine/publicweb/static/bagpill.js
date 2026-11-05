// The bag's count and total on the pages around it, and − and + on a
// quantity. The bag is in the address, not on the server: this remembers
// the last bag looked at in this browser, one per order, and forgets it
// once the order is sent or the bag emptied. Pages work without it.
(function () {
  "use strict";
  var d = document, w = window, i, e, all, has = Object.prototype.hasOwnProperty;
  function each(sel, fn) { var l = d.querySelectorAll(sel); for (var j = 0; j < l.length; j++) fn(l[j]); }
  // The order this page belongs to: its bag's address, without the query.
  var el = d.querySelector("[data-bag-link]"), p = el && el.getAttribute("href");
  if (!p) { el = d.querySelector("form[data-merge]"); p = el && el.getAttribute("action"); }
  var K = "taskiem-bag:" + String(p || w.location.pathname).split("?")[0];
  function get() { try { return JSON.parse(w.localStorage.getItem(K)); } catch { return null; } }
  function put(v) {
    try { if (v) w.localStorage.setItem(K, JSON.stringify(v)); else w.localStorage.removeItem(K); } catch { /* storage off */ }
  }
  // An address on this site only, never //elsewhere.
  function local(h) { return /^\/(?![/\\])/.test(h || ""); }
  var mark = d.querySelector("[data-bag-count]"), n;
  if (d.querySelector("[data-bag-clear]")) put(null);
  else if (mark) {
    n = parseInt(mark.getAttribute("data-bag-count"), 10) || 0;
    put(n > 0 ? { n: n, t: mark.getAttribute("data-bag-total") || "", h: mark.getAttribute("data-bag-href") || "" } : null);
  }
  var b = get();
  if (b && b.n > 0 && local(b.h)) {
    each("[data-bag-n]", function (x) { x.textContent = b.n; });
    each("[data-bag-t]", function (x) { x.textContent = b.t; });
    each("[data-bag-link]", function (x) { x.setAttribute("href", b.h); });
    each("[data-bag-show]", function (x) { x.hidden = false; });
    each("[data-bag-hide]", function (x) { x.hidden = true; });
  }
  function same(a, c) {
    for (var k in a) if (has.call(a, k) && a[k] !== c[k]) return false;
    for (k in c) if (has.call(c, k) && a[k] !== c[k]) return false;
    return true;
  }
  // Adding from a card or an item's page keeps what the bag holds: the
  // item with the same choices adds to its line; with other choices it is
  // a line of its own, <id>~2 to ~9.
  function merge(form) {
    form.addEventListener("submit", function (ev) {
      var cur = get(), keep, have = {}, order = [], mine = {}, id = "", qty = 0, most = 20, key, k, x, m, out;
      var action = form.getAttribute("action") || "";
      if (!cur || !local(cur.h) || cur.h.indexOf("?") < 0 || !local(action)) return;
      for (k = 0; k < form.elements.length; k++) {
        x = form.elements[k];
        if (!x.name || (/radio|checkbox/.test(x.type) && !x.checked)) continue;
        if ((m = /^q\.([\w-]+)$/.exec(x.name))) { id = m[1]; qty = parseInt(x.value, 10) || 0; most = parseInt(x.getAttribute("max"), 10) || most; }
        else if ((m = /^o\.[\w-]+\.(\d+)$/.exec(x.name))) mine[m[1]] = x.value;
      }
      if (!id || qty <= 0) return;
      try { keep = new URLSearchParams(cur.h.split("?")[1]); } catch { return; }
      keep.forEach(function (v, k) {
        var m = /^q\.([\w-]+(?:~[2-9])?)$/.exec(k);
        if (m && !have[m[1]]) { have[m[1]] = { q: parseInt(v, 10) || 0, o: {} }; order.push(m[1]); }
      });
      keep.forEach(function (v, k) {
        var m = /^o\.([\w-]+(?:~[2-9])?)\.(\d+)$/.exec(k);
        if (m && have[m[1]]) have[m[1]].o[m[2]] = v;
      });
      for (k = 1; k <= 9 && !key; k++) {
        x = k === 1 ? id : id + "~" + k;
        if (have[x] && same(have[x].o, mine)) { key = x; qty = Math.min(most, qty + have[x].q); }
      }
      for (k = 1; k <= 9 && !key; k++) if (!have[x = k === 1 ? id : id + "~" + k]) key = x;
      if (!key) return; // nine lines of it already: the form goes as it is
      out = new URLSearchParams({ step: "bag" });
      order.forEach(function (l) {
        if (l === key) return;
        out.set("q." + l, have[l].q);
        for (var g in have[l].o) if (has.call(have[l].o, g)) out.set("o." + l + "." + g, have[l].o[g]);
      });
      out.set("q." + key, qty);
      for (k in mine) if (has.call(mine, k)) out.set("o." + key + "." + k, mine[k]);
      ev.preventDefault();
      w.location.assign(action.split("?")[0] + "?" + out);
    });
  }
  each("form[data-merge]", merge);
  // − and + beside a quantity, named for its item (data-stepper="<name>").
  function step(input, by) {
    var x = d.createElement("button"), name = input.getAttribute("data-stepper");
    x.type = "button"; x.className = "step"; x.textContent = by < 0 ? "−" : "+";
    x.setAttribute("aria-label", (by < 0 ? "One fewer" : "One more") + (name ? " " + name : ""));
    x.addEventListener("click", function () {
      var low = parseInt(input.getAttribute("min"), 10), max = parseInt(input.getAttribute("max"), 10) || 20;
      input.value = Math.max(isNaN(low) ? 1 : low, Math.min(max, (parseInt(input.value, 10) || 0) + by));
      input.dispatchEvent(new w.Event("input", { bubbles: true }));
    });
    return x;
  }
  all = d.querySelectorAll("input[data-stepper]");
  for (i = 0; i < all.length; i++) {
    e = all[i];
    e.parentNode.insertBefore(step(e, -1), e);
    e.parentNode.insertBefore(step(e, 1), e.nextSibling);
  }
})();
