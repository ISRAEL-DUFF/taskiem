// site.js: the design's two interactive parts, the phone menu, the contact
// form, and motion that shows the product the way it behaves.
(function () {
  var doc = document.documentElement;
  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var motion = !reduce && "IntersectionObserver" in window;
  if (motion) doc.classList.add("motion");

  function each(sel, fn, root) { Array.prototype.forEach.call((root || document).querySelectorAll(sel), fn); }
  function wait(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }
  function byText(sel, start, root) {
    var found = null;
    each(sel, function (el) { if (!found && el.textContent.trim().indexOf(start) === 0) found = el; }, root);
    return found;
  }
  // Run fn once, when el first comes into view.
  function whenSeen(el, fn, ratio) {
    if (!el) return;
    if (!motion) { fn(); return; }
    var io = new IntersectionObserver(function (entries) {
      if (entries[0].isIntersecting) { io.disconnect(); fn(); }
    }, { threshold: ratio || 0.35 });
    io.observe(el);
  }

  // ---------- The phone menu ----------
  each(".tk header .nav-narrow", function (button) {
    var header = button.closest("header");
    button.setAttribute("aria-expanded", "false");
    button.addEventListener("click", function () {
      var open = header.classList.toggle("menu-open");
      button.setAttribute("aria-expanded", String(open));
      button.setAttribute("aria-label", open ? "Close menu" : "Open menu");
    });
  });

  // ---------- Light or dark: a switch in the header ----------
  var SUN = '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>';
  var MOON = '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z"/></svg>';
  var deviceDark = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)");
  // Light unless the reader chose dark; "auto" (from a link) follows the device.
  function current() {
    var f = doc.getAttribute("data-force");
    if (f === "auto") return deviceDark && deviceDark.matches ? "dark" : "light";
    return f === "dark" ? "dark" : "light";
  }
  var toggles = [];
  function paint() {
    var dark = current() === "dark";
    toggles.forEach(function (b) {
      b.innerHTML = dark ? SUN : MOON;
      b.setAttribute("aria-label", dark ? "Switch to light theme" : "Switch to dark theme");
      b.setAttribute("title", dark ? "Light theme" : "Dark theme");
    });
  }
  each(".tk header", function (header) {
    var menu = header.querySelector(".nav-narrow");
    if (!menu) return;
    var cta = header.querySelector("a.btn.nav-wide");
    var group = document.createElement("div");
    group.className = "tk-actions";
    var b = document.createElement("button");
    b.type = "button";
    b.className = "theme-toggle";
    b.addEventListener("click", function () {
      var next = current() === "dark" ? "light" : "dark";
      doc.setAttribute("data-force", next);
      try { window.localStorage.setItem("tk-theme", next); } catch { /* not remembered */ }
      paint();
    });
    toggles.push(b);
    header.insertBefore(group, cta || menu);
    if (cta) group.appendChild(cta);
    group.appendChild(b);
    group.appendChild(menu);
  });
  paint();
  if (deviceDark && deviceDark.addEventListener) deviceDark.addEventListener("change", paint);

  // ---------- Pricing: monthly or yearly ----------
  var prices = window.TK_PRICES;
  if (prices) {
    var buttons = document.querySelectorAll('[data-action="setMonthly"], [data-action="setYearly"]');
    var figures = document.querySelectorAll("main .num");
    Array.prototype.forEach.call(buttons, function (b) {
      b.addEventListener("click", function () {
        var p = b.getAttribute("data-action") === "setYearly" ? prices.yearly : prices.monthly;
        var list = [p.prices.starter, p.prices.growing, p.prices.enterprise];
        Array.prototype.forEach.call(figures, function (f, i) {
          if (!list[i]) return;
          f.textContent = list[i];
          if (f.nextElementSibling) f.nextElementSibling.textContent = p.per;
          f.classList.remove("flip"); void f.offsetWidth; f.classList.add("flip");
        });
        Array.prototype.forEach.call(buttons, function (o) { o.setAttribute("aria-pressed", String(o === b)); });
      });
    });
  }

  // ---------- The contact form ----------
  each(".tk main form", function (form) {
    form.addEventListener("submit", function (e) {
      e.preventDefault();
      var to = form.getAttribute("data-email");
      var note = form.querySelector(".form-note") || form.appendChild(Object.assign(document.createElement("p"), { className: "small form-note" }));
      note.setAttribute("role", "status");
      if (!to) { note.textContent = "Messages from this form are not switched on yet."; return; }
      var get = function (id) { var el = form.querySelector("#" + id); return el ? el.value : ""; };
      var body = get("c-msg") || (form.querySelector("textarea") || {}).value || "";
      window.location.href = "mailto:" + to + "?subject=" + encodeURIComponent("Taskiem: a message from " + (get("c-name") || "the website")) +
        "&body=" + encodeURIComponent(body + "\n\n" + get("c-name") + "\n" + get("c-email"));
      note.textContent = "Your email app should open with the message. Send it from there.";
    });
  });

  if (!motion) return;

  // ---------- Sections and cards ease in as they come into view ----------
  var revealIO = new IntersectionObserver(function (entries) {
    entries.forEach(function (e) {
      if (e.isIntersecting) { e.target.classList.add("in"); revealIO.unobserve(e.target); }
    });
  }, { threshold: 0.12, rootMargin: "0px 0px -6% 0px" });
  var fold = window.innerHeight * 0.92;
  each(".tk main .h2, .tk main .lede, .tk main .card, .tk main .card-pine, .tk main .card-band, .tk main .moment, .tk main .tbl-wrap, .tk main details.faq, .tk main .phone, .tk .onpine > .container > .h2", function (el) {
    if (el.getBoundingClientRect().top < fold) return; // already on screen: leave it be
    var siblings = el.parentElement ? Array.prototype.indexOf.call(el.parentElement.children, el) : 0;
    el.style.setProperty("--d", Math.min(siblings, 5) * 70 + "ms");
    el.classList.add("rv");
    revealIO.observe(el);
  });

  // ---------- Chats: each message arrives in turn, Taskiem types first ----------
  function play(body, start) {
    var bubbles = Array.prototype.filter.call(body.children, function (el) { return el.classList.contains("bub") || el.hasAttribute("data-from"); });
    if (!bubbles.length) return;
    // Hold the chat at its finished height; messages fill it from the
    // bottom up, as in a real chat, so nothing on the page moves.
    body.style.minHeight = body.offsetHeight + "px";
    body.style.justifyContent = "flex-end";
    bubbles.forEach(function (b) { b.classList.add("wait"); });
    var typing = document.createElement("div");
    typing.className = "bub bub-tk typing";
    typing.setAttribute("aria-hidden", "true");
    typing.innerHTML = "<i></i><i></i><i></i>";
    var chain = wait(start || 300);
    bubbles.forEach(function (b) {
      chain = chain.then(function () {
        if (b.classList.contains("bub-tk") || b.getAttribute("data-from") === "tk") {
          b.parentNode.insertBefore(typing, b);
          return wait(Math.min(700 + b.textContent.length * 9, 1700)).then(function () { typing.remove(); });
        }
        return wait(650);
      }).then(function () {
        b.classList.remove("wait");
        b.classList.add("pop");
      });
    });
  }
  var hero = document.querySelector('[data-chat="hero"]');
  if (hero && hero.getBoundingClientRect().top < fold) play(hero, 700);
  else if (hero) whenSeen(hero, function () { play(hero, 300); }, 0.4);
  each(".tk main .moment-chat, .tk main .phone .chat-body", function (body) {
    whenSeen(body, function () { play(body, 200); }, 0.5);
  });

  // ---------- Naira figures count up ----------
  if (!prices) {
    each(".tk main .num", function (el) {
      var m = el.textContent.trim().match(/^₦([\d,]+)$/);
      if (!m) return;
      var target = parseInt(m[1].replace(/,/g, ""), 10);
      whenSeen(el, function () {
        var t0 = performance.now(), dur = 900;
        (function step(now) {
          var k = Math.min(1, (now - t0) / dur), eased = 1 - Math.pow(1 - k, 3);
          el.textContent = "₦" + Math.round(target * eased).toLocaleString("en-NG");
          if (k < 1) requestAnimationFrame(step);
        })(t0);
      }, 0.6);
    });
  }

  // ---------- The salary run: PIN, Approve, sent once, paid ----------
  var STATUS = {
    sending: { bg: "#2f5bd3", icon: '<path d="M21 12a9 9 0 1 1-3-6.7"/><path d="M21 4v5h-5"/>', text: "Sending, once" },
    paid: { bg: "#1f7a50", icon: '<path d="m5 12.5 4.5 4.5L19 7.5"/>', text: "Paid" },
  };
  function setStatus(el, s) {
    el.style.background = s.bg;
    el.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + s.icon + "</svg>" + s.text;
    el.classList.remove("flip"); void el.offsetWidth; el.classList.add("flip");
  }
  var salary = byText(".tk main span", "Salary run");
  if (salary) {
    var box = salary.parentElement.parentElement;
    var status = box.querySelector(".status");
    var pin = byText("span", "••••", box);
    var approve = byText("span", "Approve", box);
    whenSeen(box, function () {
      if (pin) pin.textContent = "";
      var chain = wait(700);
      [1, 2, 3, 4].forEach(function (n) {
        chain = chain.then(function () { if (pin) pin.textContent = "••••".slice(0, n); return wait(230); });
      });
      chain.then(function () { if (approve) approve.classList.add("press"); return wait(450); })
        .then(function () { setStatus(status, STATUS.sending); return wait(1300); })
        .then(function () { setStatus(status, STATUS.paid); });
    }, 0.55);
  }

  // ---------- An invoice marks itself paid ----------
  var invoices = byText(".tk main h3", "Invoices that mark themselves paid");
  if (invoices) {
    var waiting = byText(".status", "Waiting", invoices.closest(".card, .card-pine"));
    whenSeen(waiting, function () {
      wait(1600).then(function () { setStatus(waiting, STATUS.paid); });
    }, 0.8);
  }
})();

// ---------- The canvas: fit the stage to the frame, then play one run ----------
(function () {
  var doc = document.documentElement;
  var stage = document.querySelector(".tk .wf-stage");
  if (!stage) return;
  var frame = stage.parentElement;
  var W = 1160, H = 440;
  function fit() {
    var w = frame.clientWidth;
    // Narrow screens scroll the canvas sideways instead of shrinking it past reading size.
    var s = Math.min(1, Math.max(w / W, 0.66));
    stage.style.transform = "scale(" + s + ")";
    // Room under the last row for the tool bar that sits over the canvas.
    frame.style.height = Math.round(H * s) + (s < 1 ? 56 : 0) + "px";
    frame.style.overflowX = s * W > w + 1 ? "auto" : "hidden";
  }
  fit();
  if ("ResizeObserver" in window) new ResizeObserver(fit).observe(frame); else window.addEventListener("resize", fit);
  if (!doc.classList.contains("motion") || !("IntersectionObserver" in window)) return;
  var cards = Array.prototype.slice.call(stage.querySelectorAll(".wf-card[data-run]")).sort(function (a, b) { return a.dataset.run - b.dataset.run; });
  cards.forEach(function (c) { var d = document.createElement("span"); d.className = "wf-done"; d.setAttribute("aria-hidden", "true"); d.textContent = "✓"; c.appendChild(d); });
  var io = new IntersectionObserver(function (entries) {
    if (!entries[0].isIntersecting) return;
    io.disconnect();
    var t = 600;
    cards.forEach(function (c) { setTimeout(function () { c.classList.add("ran"); }, t); t += 520; });
  }, { threshold: 0.45 });
  io.observe(frame);
})();
