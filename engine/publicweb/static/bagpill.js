// The bag's count and total on the pages around it, and − and + on a card's
// quantity. The bag is in the address, not on the server: this only remembers
// the last bag the visitor looked at, in this browser, and clears it once an
// order is sent. Without the script every page still works.
(function () {
  "use strict";
  var K = "taskiem-bag", d = document;
  function get() { try { return JSON.parse(localStorage.getItem(K)); } catch (e) { return null; } }
  function put(v) { try { if (v) localStorage.setItem(K, JSON.stringify(v)); else localStorage.removeItem(K); } catch (e) {} }
  var mark = d.querySelector("[data-bag-count]");
  if (d.querySelector("[data-bag-clear]")) put(null);
  else if (mark) put({ n: parseInt(mark.getAttribute("data-bag-count"), 10) || 0, t: mark.getAttribute("data-bag-total") || "", h: mark.getAttribute("data-bag-href") || "" });
  var b = get(), i, e, all;
  if (b && b.n > 0 && /^\/[^/]/.test(b.h || "")) {
    all = d.querySelectorAll("[data-bag-n]"); for (i = 0; i < all.length; i++) all[i].textContent = b.n;
    all = d.querySelectorAll("[data-bag-t]"); for (i = 0; i < all.length; i++) all[i].textContent = b.t;
    all = d.querySelectorAll("[data-bag-link]"); for (i = 0; i < all.length; i++) all[i].setAttribute("href", b.h);
    all = d.querySelectorAll("[data-bag-show]"); for (i = 0; i < all.length; i++) all[i].hidden = false;
    all = d.querySelectorAll("[data-bag-hide]"); for (i = 0; i < all.length; i++) all[i].hidden = true;
  }
  // Adding from a card or an item's page keeps what the bag already holds.
  all = d.querySelectorAll("form[data-merge]");
  function merge(form) {
    form.addEventListener("submit", function () {
      var cur = get(), mine = {}, keep, k, input, n;
      if (!cur || !cur.h || cur.h.indexOf("?") < 0) return;
      for (n = 0; n < form.elements.length; n++) if (form.elements[n].name) mine[form.elements[n].name.split(".").slice(0, 2).join(".")] = 1;
      try { keep = new URLSearchParams(cur.h.split("?")[1]); } catch (e) { return; }
      keep.forEach(function (v, k) {
        if (!/^[qo]\.[A-Za-z0-9_-]+(\.\d+)?$/.test(k) || mine[k.split(".").slice(0, 2).join(".")]) return;
        input = d.createElement("input"); input.type = "hidden"; input.name = k; input.value = v; form.appendChild(input);
      });
    });
  }
  for (i = 0; i < all.length; i++) merge(all[i]);
  all = d.querySelectorAll("input[data-stepper]");
  function step(input, by, label) {
    var x = d.createElement("button");
    x.type = "button"; x.className = "step"; x.textContent = by < 0 ? "−" : "+"; x.setAttribute("aria-label", label);
    x.addEventListener("click", function () {
      var q = (parseInt(input.value, 10) || 1) + by, max = parseInt(input.getAttribute("max"), 10) || 99;
      input.value = Math.max(1, Math.min(max, q));
    });
    return x;
  }
  for (i = 0; i < all.length; i++) {
    e = all[i];
    e.parentNode.insertBefore(step(e, -1, "One fewer"), e);
    e.parentNode.insertBefore(step(e, 1, "One more"), e.nextSibling);
  }
})();
