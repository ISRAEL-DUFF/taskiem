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
  if (params.get("size") === "9x16") document.documentElement.classList.add("portrait");

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
    addEventListener("DOMContentLoaded", () => seek(0));
    return;
  }
  // Playing in a browser: the clock runs, and the scene loops.
  addEventListener("DOMContentLoaded", () => {
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
