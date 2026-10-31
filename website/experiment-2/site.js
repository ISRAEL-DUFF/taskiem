// site.js: the two interactive parts of the design, and the phone menu.
(function () {
  // The phone menu.
  document.querySelectorAll(".tk header .nav-narrow").forEach(function (button) {
    var header = button.closest("header");
    button.setAttribute("aria-expanded", "false");
    button.addEventListener("click", function () {
      var open = header.classList.toggle("menu-open");
      button.setAttribute("aria-expanded", String(open));
      button.setAttribute("aria-label", open ? "Close menu" : "Open menu");
    });
  });

  // Pricing: monthly or yearly.
  var prices = window.TK_PRICES;
  if (prices) {
    var buttons = document.querySelectorAll('[data-action="setMonthly"], [data-action="setYearly"]');
    var figures = document.querySelectorAll(".num");
    var pers = [];
    figures.forEach(function (f) { pers.push(f.nextElementSibling); });
    buttons.forEach(function (b) {
      b.addEventListener("click", function () {
        var p = b.getAttribute("data-action") === "setYearly" ? prices.yearly : prices.monthly;
        var list = [p.prices.starter, p.prices.growing, p.prices.enterprise];
        figures.forEach(function (f, i) { if (list[i]) f.textContent = list[i]; });
        pers.forEach(function (s) { if (s) s.textContent = p.per; });
        buttons.forEach(function (o) { o.setAttribute("aria-pressed", String(o === b)); });
      });
    });
  }
})();
