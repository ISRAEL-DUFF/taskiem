// The order form's live total. It only shows a sum: the server prices the
// order from the catalogue, and the form works without this script.
(function () {
  "use strict";
  var form = document.querySelector("form[data-order]");
  if (!form) return;
  var out = form.querySelector("[data-total]");
  var inputs = form.querySelectorAll("input[data-price]");
  function naira(k) {
    var whole = String(Math.floor(k / 100)).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
    var frac = k % 100;
    return "₦" + whole + (frac ? "." + (frac < 10 ? "0" : "") + frac : "");
  }
  function update() {
    var total = 0;
    for (var i = 0; i < inputs.length; i++) {
      var q = parseInt(inputs[i].value, 10);
      var p = parseInt(inputs[i].getAttribute("data-price"), 10);
      if (q > 0 && q <= 20 && p >= 0) total += q * p;
    }
    out.textContent = total > 0 ? "Total: " + naira(total) : "";
  }
  form.addEventListener("input", update);
  update();
})();
