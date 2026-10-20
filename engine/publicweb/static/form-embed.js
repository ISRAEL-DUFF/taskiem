// A hosted form framed on the organisation's website: tells the page
// around it its height and, once sent, the reference (forms 6.9). It
// carries no answers, and posts only to the verified websites.
(function () {
  "use strict";
  var el = document.querySelector("[data-embed]");
  if (!el || window.parent === window) return;
  var origins = (el.getAttribute("data-origins") || "").split(" ").filter(Boolean);
  function send(msg) {
    for (var i = 0; i < origins.length; i++) window.parent.postMessage(msg, origins[i]);
  }
  var site = document.querySelector("[data-site]");
  if (site) {
    var anc = location.ancestorOrigins && location.ancestorOrigins[0];
    if (!anc && document.referrer) { try { anc = new URL(document.referrer).origin; } catch (e) {} }
    if (anc) site.value = anc;
  }
  var last = 0;
  function height() {
    var h = document.documentElement.scrollHeight;
    if (h !== last) { last = h; send({ type: "form:height", height: h }); }
  }
  height();
  if (window.ResizeObserver) new ResizeObserver(height).observe(document.body);
  else window.addEventListener("resize", height);
  var ref = el.getAttribute("data-submitted");
  if (ref) send({ type: "form:submitted", reference: ref });
})();
