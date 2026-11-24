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

  // Sizes: a value with none left in any size is out (the page says so).
  // Picking a value clears another pick it has no size with. The size
  // picked names the line (q.<size>), its price and most; a colour shows
  // its photo, or the product's.
  function sizes(f) {
    var L = JSON.parse(f.getAttribute("data-sizes")), S = f.querySelectorAll("fieldset[data-size-choice]"), b = f.querySelector("[data-price]"), q = f.querySelector("input.qty"), P = d.querySelector(".pdp-price");
    var base = b && b.getAttribute("data-price"), max = q && q.max, nm = q && q.name, pt = P && P.textContent, pic = main && main.src, set = main && main.getAttribute("srcset");
    function pk() {
      return [].map.call(S, function (s) { var c = s.querySelector("input:checked"); return c ? c.value : null; });
    }
    function left(y) { return y[2] === null || y[2] > 0; }
    function up(ev) {
      var s = ev && ev.target.closest("fieldset"), at = [].indexOf.call(S, s), p = pk(), z = null;
      if (at >= 0) [].forEach.call(S, function (x, j) {
        var c = x.querySelector("input:checked");
        if (c && j !== at && !L.some(function (y) { return left(y) && y[3][at] === p[at] && y[3][j] === c.value; })) c.checked = false;
      });
      p = pk();
      if (p.indexOf(null) < 0) L.forEach(function (y) { if (left(y) && p.every(function (x, j) { return y[3][j] === x; })) z = y; });
      var k = z && z[1] !== null ? z[1] : null;
      if (b) b.setAttribute("data-price", k === null ? base : k);
      if (P) P.textContent = k === null ? pt : naira(k);
      if (q) { q.max = z && z[2] !== null ? Math.min(z[2], max) : max; q.name = z ? "q." + z[0] : nm; }
      if (main && s && s.querySelector("[data-photo]")) {
        var t = ev.target.getAttribute("data-photo");
        main.src = t || pic;
        if (t || !set) main.removeAttribute("srcset"); else main.setAttribute("srcset", set);
      }
    }
    f.addEventListener("change", up);
    up();
  }
})();
