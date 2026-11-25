// A form that sells in packages (forms-that-sell §5.2): the chosen
// package's total with the area's fee, and a size for each item it holds.
// Only shown: the server prices the order, and the form works without it.
(function () {
  "use strict";
  var form = document.querySelector("form[data-sell]");
  if (!form) return;
  var out = form.querySelector("[data-total]");
  var zone = form.querySelector("select[name=zone]");
  function naira(k) {
    return "₦" + String(Math.floor(k / 100)).replace(/\B(?=(\d{3})+(?!\d))/g, ",") + (k % 100 ? "." + (k % 100 < 10 ? "0" : "") + (k % 100) : "");
  }
  function update() {
    var p = form.querySelector("input[name=pkg]:checked");
    var total = p ? parseInt(p.getAttribute("data-price"), 10) || 0 : 0;
    var qty = p ? parseInt(p.value, 10) || 0 : 0;
    var fee = 0, opt = zone && zone.options[zone.selectedIndex], how = form.querySelector("input[name=fulfilment]:checked");
    if (total > 0 && opt && opt.value && (!form.querySelector("input[name=fulfilment]") || (how && how.value === "delivery"))) {
      fee = parseInt(opt.getAttribute("data-fee"), 10) || 0;
    }
    var units = form.querySelectorAll("[data-unit]");
    for (var i = 0; i < units.length; i++) units[i].hidden = !(parseInt(units[i].getAttribute("data-unit"), 10) <= qty);
    if (out) out.textContent = total > 0 ? "Total: " + naira(total + fee) + (fee > 0 ? " with " + naira(fee) + " delivery" : "") : "";
  }
  form.addEventListener("change", update);
  update();
})();
