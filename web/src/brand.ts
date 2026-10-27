// Taskiem's own look (decision 0043): relay, a "t" with a dot at its top
// right in pine and lime, is the brand. Seal and signal, the other two
// directions, are kept to experiment with: ?brand=seal (or signal, or relay)
// in the address switches this browser to it and remembers it. Applied as
// data-brand on <html> before the first paint (main.tsx), with the tab
// icon to match; the colours are in styles.css, the files in
// public/brand/<brand>/ (made by docs/brand/make.py).
export type TaskiemBrand = "relay" | "seal" | "signal";

export const BRANDS: TaskiemBrand[] = ["relay", "seal", "signal"];

/** The brand: relay. */
export const DEFAULT_BRAND: TaskiemBrand = "relay";

const KEY = "taskiem-brand";

const isBrand = (s: string | null): s is TaskiemBrand => s !== null && (BRANDS as string[]).includes(s);

/** The brand this browser shows: one asked for in the address (kept), else the one kept, else relay. */
export function savedBrand(): TaskiemBrand {
  try {
    const asked = new URLSearchParams(window.location.search).get("brand");
    if (isBrand(asked)) {
      if (asked === DEFAULT_BRAND) localStorage.removeItem(KEY);
      else localStorage.setItem(KEY, asked);
      return asked;
    }
    const kept = localStorage.getItem(KEY);
    return isBrand(kept) ? kept : DEFAULT_BRAND;
  } catch {
    return DEFAULT_BRAND;
  }
}

export function applyBrand(b: TaskiemBrand) {
  if (b === DEFAULT_BRAND) delete document.documentElement.dataset.brand;
  else document.documentElement.dataset.brand = b;
  for (const [id, file] of [
    ["icon-svg", "favicon.svg"],
    ["icon-png", "favicon-32.png"],
    ["icon-touch", "apple-touch-icon.png"],
  ] as const) {
    document.getElementById(id)?.setAttribute("href", brandFile(file, b));
  }
}

/** The shown brand's file under public/brand/. */
export function brandFile(name: string, b: TaskiemBrand = currentBrand()): string {
  return `/brand/${b}/${name}`;
}

export function currentBrand(): TaskiemBrand {
  const b = document.documentElement.dataset.brand ?? null;
  return isBrand(b) ? b : DEFAULT_BRAND;
}
