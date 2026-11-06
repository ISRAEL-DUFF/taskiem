// An order's live total, deposit and − + buttons: only shown, the server
// prices the order, and the form works without this script.
(function () {
  "use strict";
  var form = document.querySelector("form[data-order]");
  if (!form) return;
  var out = form.querySelector("[data-total]");
  var inputs = form.querySelectorAll("input[data-price]");
  var zone = form.querySelector("select[name=zone]");
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
    // The area's fee, while delivery is chosen.
    var fee = 0;
    var how = form.querySelector("input[name=fulfilment]:checked");
    var opt = zone && zone.options[zone.selectedIndex];
    if (total > 0 && opt && opt.value && (!form.querySelector("input[name=fulfilment]") || (how && how.value === "delivery"))) {
      fee = parseInt(opt.getAttribute("data-fee"), 10) || 0;
    }
    // No deposit paying at the door; one is ₦1 or more and less than the
    // whole (as the server takes it). The button says what is paid now.
    var later = form.querySelector("input[name=pay]:checked"), off = !!(later && later.value === "on_delivery");
    var split = form.querySelectorAll(".bag-split"), el = function (x) { return form.querySelector(x); };
    var pct = parseInt(form.getAttribute("data-deposit-pct"), 10) || 0, due = total + fee, part = Math.floor((due * pct + 99) / 100);
    var dep = el("[data-deposit]"), bal = el("[data-balance]"), now = el("[data-pay-now]"), lat = el("[data-pay-later]");
    if (!(pct > 0 && pct < 100 && part >= 100 && part < due)) { off = off || pct > 0; part = due; }
    for (var s = 0; s < split.length; s++) split[s].hidden = off;
    if (dep && bal) { dep.textContent = naira(part); bal.textContent = naira(due - part); }
    if (el("[data-pay]")) el("[data-pay]").textContent = naira(part);
    if (lat) { lat.hidden = !(later && later.value === "on_delivery"); now.hidden = !lat.hidden; }
    out.textContent = total > 0 ? "Total: " + naira(total + fee) + (fee > 0 ? " with " + naira(fee) + " delivery" : "") : "";
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
    if (input.type === "hidden") continue;
    var name = (form.querySelector('label[for="' + input.id + '"] .line-name') || {}).textContent || "";
    var box = document.createElement("span");
    box.className = "stepper";
    input.parentNode.insertBefore(box, input);
    box.appendChild(step(input, -1, "One fewer " + name));
    box.appendChild(input);
    box.appendChild(step(input, 1, "One more " + name));
  }
  // The map pin: the browser's location, if asked (the server makes it a link).
  var pin = form.querySelector("input[data-pin]");
  if (pin && window.navigator.geolocation) {
    var here = document.createElement("button");
    here.type = "button";
    here.className = "btn btn-small";
    here.textContent = "Use my location";
    here.addEventListener("click", function () {
      here.disabled = true;
      window.navigator.geolocation.getCurrentPosition(
        function (p) {
          pin.value = p.coords.latitude.toFixed(6) + "," + p.coords.longitude.toFixed(6);
          here.textContent = "Location added";
        },
        function () {
          here.disabled = false;
          here.textContent = "Location not available";
        },
        { timeout: 15000 }
      );
    });
    pin.parentNode.appendChild(here);
  }
  form.addEventListener("input", update);
  form.addEventListener("change", update);
  // Sent once on a slow connection; back on the page frees the button.
  if ((form.getAttribute("method") || "").toLowerCase() === "post") {
    var sends = form.querySelectorAll("button[type=submit]");
    form.addEventListener("submit", function () {
      setTimeout(function () { for (var i = 0; i < sends.length; i++) sends[i].disabled = true; }, 0);
    });
    window.addEventListener("pageshow", function () { for (var i = 0; i < sends.length; i++) sends[i].disabled = false; });
  }
  update();
})();
