// An order form that follows up a form left half-filled (forms-that-sell
// §6.1): once a valid phone or email is typed, and again when the page is
// hidden or left, it sends a keep with the contact and what is being
// bought, never anything else typed, and nothing once the form is sent.
// Without a script nothing is kept, and the form loses nothing.
(function () {
  "use strict";
  var el = document.querySelector("[data-keep]");
  var form = document.querySelector("form[method=post]");
  if (!el || !form) return;
  var url = el.getAttribute("data-keep");
  var token = el.getAttribute("data-keep-token");
  if (!url || !token) return;
  var sent = false;
  var last = "";
  form.addEventListener("submit", function () { sent = true; });
  function phone(v) {
    v = v.replace(/[\s().-]/g, "");
    return /^(\+?234|0)[789][01]\d{8}$/.test(v) ? v : "";
  }
  function email(v) {
    return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v) ? v : "";
  }
  function value(name) {
    var f = form.querySelector("[name=\"" + name + "\"]");
    return f ? String(f.value || "").trim() : "";
  }
  function site() {
    if (window.parent === window) return "";
    var loc = window.location;
    if (loc.ancestorOrigins && loc.ancestorOrigins[0]) return loc.ancestorOrigins[0];
    try {
      return document.referrer ? new window.URL(document.referrer).origin : "";
    } catch {
      return "";
    }
  }
  function keep() {
    if (sent) return;
    var contact = phone(value("phone")) || email(value("email"));
    if (!contact) return;
    var body = new window.URLSearchParams();
    body.set("keep", token);
    body.set("contact", contact);
    var hp = form.querySelector(".hp input");
    if (hp) body.set(hp.name, hp.value);
    var pkg = form.querySelector("input[name=pkg]:checked");
    if (pkg) body.set("pkg", pkg.value);
    var pz = value("pz");
    if (pz) body.set("pz", pz);
    var zone = value("zone");
    if (zone) body.set("zone", zone);
    var qty = form.querySelectorAll("input[name^=\"q.\"]");
    for (var i = 0; i < qty.length; i++) {
      if (parseInt(qty[i].value, 10) > 0) body.set(qty[i].name, qty[i].value);
    }
    var s = site();
    if (s) body.set("site", s);
    var text = body.toString();
    if (text === last) return;
    last = text;
    if (navigator.sendBeacon) {
      navigator.sendBeacon(url, new window.Blob([text], { type: "application/x-www-form-urlencoded" }));
    } else if (window.fetch) {
      window.fetch(url, { method: "POST", body: body, keepalive: true, credentials: "omit" });
    }
  }
  form.addEventListener("focusout", function (e) {
    var n = e.target && e.target.name;
    if (n === "phone" || n === "email") keep();
  });
  document.addEventListener("visibilitychange", function () {
    if (document.visibilityState === "hidden") keep();
  });
  window.addEventListener("pagehide", keep);
})();
