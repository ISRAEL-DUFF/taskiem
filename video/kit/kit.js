// The explainer's timeline (video/README.md). A scene plays by itself when
// opened in a browser; the renderer opens it with ?render and steps it with
// window.seek(ms), one frame at a time, so every frame is exact.
//
// In a scene:
//   - CSS animations run as written (their delays are their place in time);
//   - <span data-count="486500" data-at="1200" data-dur="900" data-naira>
//     counts up from 0;
//   - <i class="tap" data-at="3000" style="left:…;top:…"></i> is a tap;
//   - window.scene = { duration: 8000 } says how long the scene is.

(function () {
  const params = new URLSearchParams(location.search);
  const rendering = params.has("render");
  const size = params.get("size");
  if (size === "9x16") document.documentElement.classList.add("portrait");
  if (size === "1x1") document.documentElement.classList.add("square");

  const ease = (t) => 1 - Math.pow(1 - t, 3);
  const naira = (n) => "₦" + Math.round(n).toLocaleString("en-NG");

  function counters(ms) {
    for (const el of document.querySelectorAll("[data-count]")) {
      const to = Number(el.dataset.count);
      const at = Number(el.dataset.at || 0);
      const dur = Number(el.dataset.dur || 900);
      const t = Math.min(1, Math.max(0, (ms - at) / dur));
      const v = to * ease(t);
      el.textContent = "naira" in el.dataset ? naira(v) : Math.round(v).toLocaleString("en-NG");
    }
  }

  // A tap: grows and fades over 600 ms from its moment.
  function taps(ms) {
    for (const el of document.querySelectorAll(".tap")) {
      const t = (ms - Number(el.dataset.at)) / 600;
      if (t < 0 || t > 1) {
        el.style.opacity = "0";
        continue;
      }
      el.style.opacity = String(0.9 * (1 - t));
      el.style.transform = `scale(${0.6 + 1.4 * ease(t)})`;
    }
  }

  // Shorthands a scene may use, expanded before anything plays:
  //   <header data-name="Rivers Foods Ltd" data-sub="Buys from you · linked" data-av="RF" data-tone="lime">
  //   <footer data-composer="Message, @ to tag">  (data-send="1": the send arrow, not the mic)
  const icon = {
    tools: '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#0d3b2e" stroke-width="2" aria-hidden="true"><rect x="4" y="4" width="6" height="6" rx="1.5"/><rect x="14" y="4" width="6" height="6" rx="1.5"/><rect x="4" y="14" width="6" height="6" rx="1.5"/><rect x="14" y="14" width="6" height="6" rx="1.5"/></svg>',
    mic: '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#c8f169" stroke-width="2" stroke-linecap="round" aria-hidden="true"><rect x="9" y="3" width="6" height="11" rx="3"/><path d="M5 11a7 7 0 0 0 14 0M12 18v3"/></svg>',
    send: '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#c8f169" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 19V5M6 11l6-6 6 6"/></svg>',
    box: '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#0d3b2e" stroke-width="1.8" stroke-linejoin="round" aria-hidden="true"><path d="M3 7l9-4 9 4-9 4-9-4z"/><path d="M3 7v10l9 4 9-4V7"/><path d="M12 11v10"/></svg>',
    clock: '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#2f5bd3" stroke-width="1.9" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/></svg>',
    close: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#0e1a14" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>',
    search: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#55615a" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="7"/><path d="M20 20l-3.5-3.5"/></svg>',
    check: '<svg viewBox="0 0 24 24" fill="none" stroke="#0d3b2e" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>',
    spark: '<svg width="20" height="20" viewBox="0 0 24 24" fill="#c8f169" aria-hidden="true"><path d="M12 2l1.8 5.6L19.5 9l-5.7 1.5L12 16l-1.8-5.5L4.5 9l5.7-1.4z"/><path d="M19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z"/></svg>',
  };
  function expand() {
    for (const h of document.querySelectorAll("header[data-name]")) {
      const av = h.dataset.av === "spark" ? icon.spark : h.dataset.av;
      h.innerHTML = `<div class="avatar ${h.dataset.tone || "pine"}">${av}</div><div class="who"><b>${h.dataset.name}</b><span>${h.dataset.sub || ""}</span></div>`;
    }
    // A burst: twelve dots around its point, starting at its moment.
    for (const b of document.querySelectorAll(".burst[data-at]")) {
      b.innerHTML = Array.from({ length: 12 }, (_, i) => `<b style="--a:${i * 30}deg;animation-delay:${Number(b.dataset.at) / 1000}s"></b>`).join("");
    }
    for (const el of document.querySelectorAll("[data-icon]")) el.innerHTML = icon[el.dataset.icon] || "";
    for (const f of document.querySelectorAll("footer[data-composer]")) {
      const typed = f.dataset.typed ? `<span style="color:#0e1a14">${f.dataset.typed}</span>` : f.dataset.composer;
      f.innerHTML = `<span class="round" style="background:#fff;box-shadow:0 1px 2px rgba(14,26,20,.12)">${icon.tools}</span><span class="box">${typed}</span><span class="round" style="background:#0d3b2e">${f.dataset.send ? icon.send : icon.mic}</span>`;
    }
  }
  window.kitIcon = icon;

  // Every animation the page has, held at one moment.
  async function seek(ms) {
    for (const a of document.getAnimations()) {
      a.pause();
      a.currentTime = ms;
    }
    counters(ms);
    taps(ms);
    await document.fonts.ready;
  }
  window.seek = seek;

  if (rendering) {
    document.documentElement.classList.add("rendering");
    addEventListener("DOMContentLoaded", () => {
      expand();
      seek(0);
    });
    return;
  }
  // Playing in a browser: the clock runs, and the scene loops.
  addEventListener("DOMContentLoaded", () => {
    expand();
    const start = performance.now();
    const length = (window.scene && window.scene.duration) || 8000;
    const tick = (now) => {
      const ms = (now - start) % (length + 1500);
      if (ms < 40) for (const a of document.getAnimations()) a.currentTime = 0;
      counters(ms);
      taps(ms);
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });
})();
