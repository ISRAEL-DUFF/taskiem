// Search over search.json, which tools/docsite writes: one record per page
// and per section ({t: page, s: section, u: url, x: text}). Every word of
// the query must appear; titles and headings weigh more than text.
/* global document, window, fetch */
(function () {
  "use strict";
  var base = document.currentScript.getAttribute("data-base") || "/";
  var index = null;
  var loading = null;

  function load() {
    if (!loading) {
      loading = fetch(base + "search.json")
        .then(function (r) { return r.json(); })
        .then(function (data) {
          index = data.map(function (e) {
            return { e: e, t: (e.t || "").toLowerCase(), s: (e.s || "").toLowerCase(), x: (e.x || "").toLowerCase() };
          });
        })
        .catch(function () { index = []; });
    }
    return loading;
  }

  function search(q) {
    var words = q.toLowerCase().split(/\s+/).filter(Boolean);
    if (!words.length || !index) return [];
    var out = [];
    index.forEach(function (r) {
      var score = 0;
      for (var i = 0; i < words.length; i++) {
        var w = words[i], hit = 0;
        if (r.s.indexOf(w) >= 0) hit += 6;
        if (r.t.indexOf(w) >= 0) hit += r.s ? 2 : 8;
        if (r.x.indexOf(w) >= 0) hit += 1;
        if (!hit) return;
        score += hit;
      }
      out.push({ r: r, score: score });
    });
    out.sort(function (a, b) { return b.score - a.score; });
    return out.slice(0, 20);
  }

  function snippet(r, words) {
    var x = r.e.x || "";
    var at = r.x.indexOf(words[0]);
    var start = Math.max(0, at - 40);
    return (start > 0 ? "…" : "") + x.slice(start, start + 160);
  }

  function setup() {
    var input = document.getElementById("q");
    var list = document.getElementById("results");
    if (!input || !list) return;
    var side = document.querySelector("details.side");
    if (side && window.matchMedia("(max-width: 800px)").matches) side.open = false;

    function show() {
      var q = input.value.trim();
      list.textContent = "";
      if (!q) { list.hidden = true; return; }
      var words = q.toLowerCase().split(/\s+/).filter(Boolean);
      var hits = search(q);
      if (!hits.length) {
        var li = document.createElement("li");
        li.className = "none";
        li.textContent = index ? "Nothing matches." : "Loading…";
        list.appendChild(li);
      }
      hits.forEach(function (h) {
        var li = document.createElement("li");
        var a = document.createElement("a");
        a.href = h.r.e.u;
        var title = document.createElement("span");
        title.textContent = h.r.e.s || h.r.e.t;
        a.appendChild(title);
        if (h.r.e.s) {
          var where = document.createElement("span");
          where.className = "where";
          where.textContent = h.r.e.t;
          a.appendChild(where);
        }
        var snip = document.createElement("span");
        snip.className = "snip";
        snip.textContent = snippet(h.r, words);
        a.appendChild(snip);
        li.appendChild(a);
        list.appendChild(li);
      });
      list.hidden = false;
    }

    input.addEventListener("focus", function () { load().then(show); });
    input.addEventListener("input", function () { load().then(show); });
    input.addEventListener("keydown", function (ev) {
      var links = list.querySelectorAll("a");
      if (ev.key === "Enter" && links.length) { window.location.href = links[0].href; }
      if (ev.key === "ArrowDown" && links.length) { ev.preventDefault(); links[0].focus(); }
      if (ev.key === "Escape") { input.value = ""; show(); }
    });
    list.addEventListener("keydown", function (ev) {
      var links = Array.prototype.slice.call(list.querySelectorAll("a"));
      var i = links.indexOf(document.activeElement);
      if (ev.key === "ArrowDown" && i < links.length - 1) { ev.preventDefault(); links[i + 1].focus(); }
      if (ev.key === "ArrowUp") { ev.preventDefault(); (i > 0 ? links[i - 1] : input).focus(); }
      if (ev.key === "Escape") { input.focus(); input.value = ""; show(); }
    });
    document.addEventListener("click", function (ev) {
      if (!list.contains(ev.target) && ev.target !== input) list.hidden = true;
    });
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", setup);
  else setup();
})();
