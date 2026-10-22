// The order form's live total and its − and + buttons. It only shows a sum:
// the server prices the order from the catalogue, and the form works
// without this script (the quantities are plain number boxes then).
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
      var most = parseInt(inputs[i].getAttribute("max"), 10) || 20;
      if (q > 0 && q <= most && p >= 0) total += q * p;
    }
    out.textContent = total > 0 ? "Total: " + naira(total) : "";
  }
  function step(input, by, label) {
    var b = document.createElement("button");
    b.type = "button";
    b.className = "step";
    b.textContent = by < 0 ? "\u2212" : "+";
    b.setAttribute("aria-label", label);
    b.addEventListener("click", function () {
      var q = (parseInt(input.value, 10) || 0) + by;
      var most = parseInt(input.getAttribute("max"), 10) || 20;
      input.value = String(Math.max(0, Math.min(most, q)));
      update();
    });
    return b;
  }
  for (var i = 0; i < inputs.length; i++) {
    var input = inputs[i];
    var name = (form.querySelector('label[for="' + input.id + '"] .line-name') || {}).textContent || "";
    var box = document.createElement("span");
    box.className = "stepper";
    input.parentNode.insertBefore(box, input);
    box.appendChild(step(input, -1, "One fewer " + name));
    box.appendChild(input);
    box.appendChild(step(input, 1, "One more " + name));
  }
  form.addEventListener("input", update);
  update();
})();
