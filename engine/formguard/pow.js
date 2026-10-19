// Taskiem form proof-of-work (engine/formguard). No dependencies.
(function () {
  "use strict";
  var enc = new TextEncoder();
  function ok(h, n) {
    for (var i = 0; i < n; i++) if (h[i >> 3] & (128 >> (i & 7))) return false;
    return true;
  }
  async function solve(f) {
    var n = +f.getAttribute("data-pow"), t = f.getAttribute("data-pow-token");
    if (!(n > 0) || !t || !(window.crypto && crypto.subtle)) return;
    var bs = f.querySelectorAll("[type=submit]"), i;
    for (i = 0; i < bs.length; i++) bs[i].disabled = true;
    for (var c = 0; c < 4294967296; c += 256) {
      var p = [];
      for (i = 0; i < 256; i++) p.push(crypto.subtle.digest("SHA-256", enc.encode(t + ":" + (c + i))));
      var hs = await Promise.all(p);
      for (i = 0; i < 256; i++) {
        if (!ok(new Uint8Array(hs[i]), n)) continue;
        var el = f.querySelector("input[name=pow]");
        if (!el) {
          el = f.appendChild(document.createElement("input"));
          el.type = "hidden";
          el.name = "pow";
        }
        el.value = String(c + i);
        for (i = 0; i < bs.length; i++) bs[i].disabled = false;
        return;
      }
      await new Promise(function (r) { setTimeout(r, 0); }); // yield
    }
  }
  function start() {
    var fs = document.querySelectorAll("form[data-pow]");
    for (var i = 0; i < fs.length; i++) solve(fs[i]);
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
  else start();
})();
