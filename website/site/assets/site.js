// What every page of taskiem.ng shares: the light/dark switch, the phone
// menu, the hero's light that follows the pointer, buttons that lean towards
// the pointer, and things below the fold that animate in when they arrive.
(() => {
  const root = document.documentElement;
  root.classList.add("js");
  const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const $ = (s, e = document) => e.querySelector(s);
  const $$ = (s, e = document) => [...e.querySelectorAll(s)];

  // light or dark: the reader's choice, remembered; light until they choose
  const isDark = () => (root.dataset.theme ? root.dataset.theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches);
  try { const t = localStorage.getItem("tk-theme"); if (t === "dark" || t === "light") root.dataset.theme = t; } catch (e) {}
  const theme = $("#theme");
  if (theme) theme.addEventListener("click", () => {
    const next = isDark() ? "light" : "dark";
    root.dataset.theme = next;
    try { localStorage.setItem("tk-theme", next); } catch (e) {}
  });

  // the phone menu
  const menu = $("#menu"), nav = $("#nav");
  if (menu && nav) {
    const set = (open) => { nav.classList.toggle("open", open); menu.setAttribute("aria-expanded", open); };
    menu.addEventListener("click", () => set(!nav.classList.contains("open")));
    $$("a", nav).forEach((a) => a.addEventListener("click", () => set(false)));
    addEventListener("keydown", (e) => { if (e.key === "Escape") set(false); });
  }

  // the hero's light follows the pointer
  const hero = $("#hero");
  if (hero) hero.addEventListener("pointermove", (e) => {
    const r = hero.getBoundingClientRect();
    hero.style.setProperty("--mx", (((e.clientX - r.left) / r.width) * 100).toFixed(1) + "%");
    hero.style.setProperty("--my", (((e.clientY - r.top) / r.height) * 100).toFixed(1) + "%");
  });

  // buttons lean towards the pointer
  if (!reduce && matchMedia("(pointer: fine)").matches) {
    $$(".mag").forEach((b) => {
      b.addEventListener("pointermove", (e) => {
        const r = b.getBoundingClientRect();
        b.style.transform = `translate(${(e.clientX - r.left - r.width / 2) * 0.18}px, ${(e.clientY - r.top - r.height / 2) * 0.3}px)`;
      });
      b.addEventListener("pointerleave", () => { b.style.transform = ""; });
    });
  }

  // below the fold: [data-anim] plays when it arrives; [data-reveal] rises in.
  // Only what starts out of sight waits, so the page is never blank.
  if (reduce || !("IntersectionObserver" in window)) return;
  const io = new IntersectionObserver((es) => es.forEach((e) => {
    if (!e.isIntersecting) return;
    e.target.classList.add("go");
    e.target.dispatchEvent(new CustomEvent("arrive"));
    io.unobserve(e.target);
  }), { threshold: 0.3 });
  $$("[data-anim],[data-reveal]").forEach((el) => {
    if (el.getBoundingClientRect().top > innerHeight) { el.classList.add("wait"); io.observe(el); }
    else el.classList.add("go");
  });
})();
