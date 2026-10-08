// Theming for the embedded builder (docs/embedding.md#theming): an embed
// app's branding tokens, validated by the server (engine/embed) and checked
// again here, become CSS custom properties on the builder's root inside
// its shadow root (or the frame page). Only these properties are ever set,
// only from values of these shapes, so a token can never inject CSS.

export interface Branding {
  colours?: Record<string, string>;
  font_family?: string;
  font_url?: string;
  logo_url?: string;
  radius?: string;
  mode?: "light" | "dark" | "auto";
}

/** Each colour token and the custom property it sets. */
export const COLOUR_PROPERTIES: Record<string, string> = {
  primary: "--accent",
  on_primary: "--accent-text",
  background: "--bg",
  surface: "--panel",
  text: "--text",
  muted: "--muted",
  border: "--border",
  accent: "--info",
  danger: "--danger",
  success: "--ok",
};

/** The other tokens' properties. */
export const FONT_PROPERTY = "--tk-font";
export const RADIUS_PROPERTY = "--tk-radius";

const colourRe = /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/;
const fontRe = /^[A-Za-z0-9 ,-]{1,100}$/;
const radiusRe = /^[0-9]{1,2}px$/;

// The light and dark palettes, for a mode that fixes one (auto follows the
// viewer's preference through the stylesheet's media query).
const LIGHT: Record<string, string> = {
  "--bg": "#f6f7f9",
  "--panel": "#ffffff",
  "--text": "#17191c",
  "--muted": "#5f6670",
  "--border": "#dde1e6",
  "--accent": "#0f6e56",
  "--accent-text": "#ffffff",
  "--danger": "#b42318",
  "--warn": "#b54708",
  "--ok": "#067647",
  "--info": "#175cd3",
  "--code-bg": "#f1f3f5",
  "color-scheme": "light",
};
const DARK: Record<string, string> = {
  "--bg": "#111315",
  "--panel": "#1a1d20",
  "--text": "#e8eaed",
  "--muted": "#9aa1a9",
  "--border": "#2c3136",
  "--accent": "#34b28a",
  "--accent-text": "#0b1512",
  "--danger": "#f97066",
  "--warn": "#fdb022",
  "--ok": "#47cd89",
  "--info": "#84adff",
  "--code-bg": "#22262a",
  "color-scheme": "dark",
};

export function isHttpsURL(s: unknown): s is string {
  if (typeof s !== "string" || s.length > 2048) return false;
  try {
    const u = new URL(s);
    return u.protocol === "https:" && u.host !== "" && u.username === "" && u.password === "";
  } catch {
    return false;
  }
}

/** Checks branding as the server does, dropping anything else. */
export function parseBranding(raw: unknown): Branding {
  const b: Branding = {};
  if (!raw || typeof raw !== "object") return b;
  const r = raw as Record<string, unknown>;
  if (r.colours && typeof r.colours === "object") {
    const c: Record<string, string> = {};
    for (const [k, v] of Object.entries(r.colours as Record<string, unknown>)) {
      if (k in COLOUR_PROPERTIES && typeof v === "string" && colourRe.test(v)) c[k] = v;
    }
    b.colours = c;
  }
  if (typeof r.font_family === "string" && fontRe.test(r.font_family)) b.font_family = r.font_family;
  if (isHttpsURL(r.font_url)) b.font_url = r.font_url;
  if (isHttpsURL(r.logo_url)) b.logo_url = r.logo_url;
  if (typeof r.radius === "string" && radiusRe.test(r.radius)) b.radius = r.radius;
  if (r.mode === "light" || r.mode === "dark" || r.mode === "auto") b.mode = r.mode;
  return b;
}

/** The custom properties to set on the builder's root for branding. */
export function themeVariables(raw: unknown): Record<string, string> {
  const b = parseBranding(raw);
  const out: Record<string, string> = { ...(b.mode === "light" ? LIGHT : b.mode === "dark" ? DARK : {}) };
  for (const [k, v] of Object.entries(b.colours ?? {})) {
    const prop = COLOUR_PROPERTIES[k];
    if (prop) out[prop] = v;
  }
  if (b.font_family) out[FONT_PROPERTY] = b.font_family;
  if (b.radius) out[RADIUS_PROPERTY] = b.radius;
  return out;
}

/** Adds a font's stylesheet to the document once. */
export function addFontStylesheet(url: string): void {
  if (!isHttpsURL(url) || typeof document === "undefined") return;
  for (const l of document.head.querySelectorAll("link[data-taskiem-font]")) if (l.getAttribute("href") === url) return;
  const link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = url;
  link.referrerPolicy = "no-referrer";
  link.dataset.taskiemFont = "";
  document.head.append(link);
}
