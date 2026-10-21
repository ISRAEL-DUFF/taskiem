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

export function applyTheme(t: Theme) {
  if (t === "light") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = t;
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
