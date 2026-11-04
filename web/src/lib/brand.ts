// How a business's public pages look (N13, decision 0042): a theme, a
// colour, a logo and a cover photo, used by every public page of the
// business: the storefront, its forms and status pages, hosted forms.

export type Theme = "clean" | "bold" | "editorial";

export interface Palette {
  brand: string;
  on_brand: string;
  text: string;
  soft: string;
  tint: string;
}

export interface Brand {
  theme: Theme;
  color: string;
  /** Storefront media ids, and their URLs by width. */
  logo?: string;
  logo_urls?: Record<string, string>;
  cover?: string;
  cover_urls?: Record<string, string>;
  palette: Palette;
  themes: Theme[];
}

export const THEMES: { id: Theme; title: string; help: string }[] = [
  { id: "clean", title: "Clean", help: "Light cards, a cover photo and soft corners. Suits almost any business." },
  { id: "bold", title: "Bold", help: "Your colour fills the top, with big type and a photo grid. For food, fashion and beauty." },
  { id: "editorial", title: "Editorial", help: "Black and white with serif headings and fine lines. Quiet and premium." },
];

/** Colours to start from: the storefront colours offered before any colour could be picked. */
export const SWATCHES = ["#0b6e4f", "#1d4ed8", "#b91c1c", "#c2410c", "#6d28d9", "#be185d", "#0f766e", "#111827"];

/** The pages a preview shows, as its tabs name them. */
export const PREVIEW_PAGES: { id: string; label: string }[] = [
  { id: "storefront", label: "Storefront" },
  { id: "order", label: "Order form" },
  { id: "booking", label: "Booking" },
  { id: "form", label: "Form" },
];

/** A colour as typed (#abc, abc, #AABBCC), as #rrggbb; null when it is not one. */
export function normalizeHex(s: string): string | null {
  const m = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(s.trim());
  if (!m) return null;
  const h = (m[1] ?? "").toLowerCase();
  return "#" + (h.length === 3 ? h.replace(/./g, (c) => c + c) : h);
}

/** What the brand screen sends: the look, images by id. */
export interface BrandRequest {
  theme: Theme;
  color: string;
  logo: string;
  cover: string;
}

export function requestOf(b: Pick<Brand, "theme" | "color" | "logo" | "cover">): BrandRequest {
  return { theme: b.theme, color: b.color, logo: b.logo ?? "", cover: b.cover ?? "" };
}

export function sameLook(a: BrandRequest, b: BrandRequest): boolean {
  return a.theme === b.theme && a.color === b.color && a.logo === b.logo && a.cover === b.cover;
}
