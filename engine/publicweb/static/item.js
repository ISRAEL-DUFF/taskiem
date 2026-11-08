// An item's page: a thumbnail shows its photo in the main place, and the
// button's sum follows the choices and the quantity. It only shows: the
// server prices the order. Without it a thumbnail opens its photo.
(function () {
  "use strict";
  var d = document, i, main = d.querySelector("[data-pic-main]"), pics = d.querySelectorAll("[data-pic]");
  function show(ev) {
    var a = ev.currentTarget;
    if (!main) return;
    ev.preventDefault();
    main.removeAttribute("srcset");
    main.src = a.getAttribute("data-pic");
    for (var j = 0; j < pics.length; j++) pics[j].removeAttribute("aria-current");
    a.setAttribute("aria-current", "true");
  }
  for (i = 0; i < pics.length; i++) pics[i].addEventListener("click", show);
  var form = d.querySelector("form[data-run]"), out = form && form.querySelector("[data-run-total]");
  if (form && form.hasAttribute("data-sizes")) sizes(form);
  if (!out) return;
  var btn = form.querySelector("[data-price]"), qty = form.querySelector("input.qty");
  function naira(k) {
    var whole = String(Math.floor(k / 100)).replace(/\B(?=(\d{3})+(?!\d))/g, ","), frac = k % 100;
    return "₦" + whole + (frac ? "." + (frac < 10 ? "0" : "") + frac : "");
  }
  function sum() {
    var k = parseInt(btn.getAttribute("data-price"), 10) || 0, q = parseInt(qty && qty.value, 10) || 1;
    var on = form.querySelectorAll("input[data-add]:checked");
    for (var j = 0; j < on.length; j++) k += parseInt(on[j].getAttribute("data-add"), 10) || 0;
    out.textContent = "· " + naira(k * Math.max(1, q));
  }
  form.addEventListener("input", sum);
  form.addEventListener("change", sum);

  // Sizes: a value with none left among the others picked is out; the
  // size picked gives its price and most; a colour's photo shows.
  function sizes(f) {
    var L = f.getAttribute("data-sizes").split("\n").map(function (r) {
      var p = r.split("|");
      return { p: p[1], n: p[2] === "" ? 1e9 : +p[2], v: p.slice(3) };
    });
    var S = f.querySelectorAll("fieldset[data-size-choice]"), b = f.querySelector("[data-price]"), q = f.querySelector("input.qty"), base = b && b.getAttribute("data-price"), max = q && q.max;
    function pk() {
      return [].map.call(S, function (s) { var c = s.querySelector("input:checked"); return c ? c.value : null; });
    }
    function fit(z, at, p) {
      return p.every(function (x, j) { return j === at || x === null || z.v[j] === x; });
    }
    function up(ev) {
      var p = pk(), z = null;
      [].forEach.call(S, function (s, j) {
        [].forEach.call(s.querySelectorAll("input"), function (i) {
          var ok = L.some(function (y) { return y.v[j] === i.value && y.n > 0 && fit(y, j, p); });
          i.disabled = !ok;
          i.parentNode.classList.toggle("is-out", !ok);
          if (!ok) i.checked = false;
        });
      });
      p = pk();
      if (p.indexOf(null) < 0) L.forEach(function (y) { if (fit(y, -1, p)) z = y; });
      if (b) b.setAttribute("data-price", z && z.p ? z.p : base);
      if (q) q.max = z && z.n < 1e9 ? z.n : max;
      var t = ev && ev.target.getAttribute("data-photo");
      if (main && t) { main.removeAttribute("srcset"); main.src = t; }
    }
    f.addEventListener("change", up);
    up();
  }
})();
