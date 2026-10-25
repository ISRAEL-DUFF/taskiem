// The console's colour theme: light unless the reader picks dark, or picks
// "system" to follow their device. Kept in this browser only; applied as
// data-theme on <html> before the first paint (main.tsx).
export type Theme = "light" | "dark" | "system";

const KEY = "taskiem-theme";

export function savedTheme(): Theme {
  try {
    const t = localStorage.getItem(KEY);
    return t === "dark" || t === "system" ? t : "light";
  } catch {
    return "light";
  }
}

/** The phone's status bar (and an installed app's title bar): pine on the
 * light theme, the page's own ground on dark. */
const BAR = { light: "#0d3b2e", dark: "#0d0e0c" };
const systemDark = () => typeof window.matchMedia === "function" && window.matchMedia("(prefers-color-scheme: dark)").matches;
let following: MediaQueryList | null = null;

function paintBar(t: Theme) {
  const dark = t === "dark" || (t === "system" && systemDark());
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", dark ? BAR.dark : BAR.light);
}

export function applyTheme(t: Theme) {
  if (t === "light") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = t;
  paintBar(t);
  // "System" follows the device when it switches while the page is open.
  if (following) following.onchange = null;
  following = t === "system" && typeof window.matchMedia === "function" ? window.matchMedia("(prefers-color-scheme: dark)") : null;
  if (following) following.onchange = () => paintBar("system");
}

export function setTheme(t: Theme) {
  try {
    if (t === "light") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, t);
  } catch {
    // Storage blocked: the choice lasts until the page reloads.
  }
  applyTheme(t);
}
