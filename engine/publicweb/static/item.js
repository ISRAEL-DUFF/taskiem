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
})();
