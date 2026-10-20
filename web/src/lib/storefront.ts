// The storefront editor (docs/spec/storefronts.md 6.1, 6.2, 6.7): the API's
// shapes, prices shown in naira, the opening hours editor's form and the
// catalogue's column mapping.

import type { Column, ColumnType, Table } from "./tables";

export interface Span {
  open: string;
  close: string;
}

export interface Hours {
  /** Monday first; a day with no openings is closed. */
  days: (Span[] | null)[];
  note?: string;
}

export interface Card {
  name: string;
  tagline?: string;
  about?: string;
  /** A storefront media id. */
  logo?: string;
  area?: string;
  address?: string;
  map_query?: string;
  phone?: string;
  whatsapp?: string;
  hours: Hours;
  template: "classic" | "bold" | "";
  accent: string;
  language?: string;
}

export interface Columns {
  name?: string;
  price?: string;
  price_unit?: "naira" | "kobo" | "";
  description?: string;
  photo?: string;
  available?: string;
  category?: string;
  sort?: string;
}

export interface Storefront {
  id: string;
  slug: string;
  status: "draft" | "published" | "closed";
  draft: Card;
  catalogue_table: string | null;
  catalogue_columns: Columns;
  published_version: number;
  /** The slug the published version serves at. */
  published_slug: string | null;
  /** The draft's logo by width, unless it was rejected. */
  logo_urls?: Image;
  published_at: string | null;
  closed_message: string | null;
  catalogue_synced_at: string | null;
  catalogue_error: string | null;
  updated_at: string;
  preview_url: string;
  templates: string[];
  accents: Record<string, string>;
}

/** URLs by width ("320", "800", "2000"). */
export type Image = Record<string, string>;

export interface Item {
  id: string;
  name: string;
  price_kobo?: number;
  description?: string;
  photo?: Image;
  available: boolean;
  category?: string;
}

export interface Catalogue {
  items: Item[];
  synced_at: string | null;
  error: string | null;
}

export interface Media {
  id: string;
  width: number;
  height: number;
  urls: Image;
  moderation: "unreviewed" | "approved" | "rejected";
}

export const DAYS = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"] as const;
export const MAX_OPENINGS = 3;

/** What each template is for, as the choice cards say it. */
export const TEMPLATE_HELP: Record<string, { title: string; help: string }> = {
  classic: { title: "Classic", help: "A list, for menus and price lists." },
  bold: { title: "Bold", help: "A photo grid, for shops." },
};

/** The card's text limits, in characters (engine/storefront Card.Check). */
export const LIMITS = { tagline: 120, about: 1000, area: 80, address: 200, map_query: 200, note: 120, closed: 200 } as const;

/** A draft for an organisation with no storefront yet. */
export function emptyCard(): Card {
  return { name: "", hours: { days: Array.from({ length: 7 }, () => null) }, template: "classic", accent: "green" };
}

/** A price in kobo as the page shows it: ₦1,500, or ₦1,500.50 when there are kobo. */
export function formatPrice(kobo: number): string {
  const neg = kobo < 0;
  const abs = Math.abs(Math.round(kobo));
  const whole = String(Math.floor(abs / 100)).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  const frac = abs % 100;
  return `${neg ? "-" : ""}₦${whole}${frac ? "." + String(frac).padStart(2, "0") : ""}`;
}

/** A suggested address from a business name: lower-case letters, digits and hyphens, at most 30. */
export function slugify(name: string): string {
  return name
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/&/g, " and ")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 30)
    .replace(/-+$/, "");
}

/** Why a slug will be refused before the server sees it, or null. Reserved names are the server's to say. */
export function slugError(slug: string): string | null {
  if (slug === "") return "Choose an address for your page.";
  if (!/^[a-z0-9-]{3,30}$/.test(slug)) return "3 to 30 lower-case letters, digits and hyphens.";
  if (slug.startsWith("-") || slug.endsWith("-")) return "The address may not start or end with a hyphen.";
  if (slug.includes("--")) return "The address may not have two hyphens together.";
  return null;
}

// --- opening hours ---

/** One day in the editor: open with one to three openings, or closed. The
 * openings are kept while a day is closed so ticking it open again restores them. */
export interface DayForm {
  open: boolean;
  spans: Span[];
}

export interface HoursForm {
  days: DayForm[];
  note: string;
}

export const DEFAULT_SPAN: Span = { open: "09:00", close: "17:00" };

/** The API's hours as the editor holds them: always seven days. */
export function hoursToForm(h: Hours | undefined | null): HoursForm {
  const days = Array.from({ length: 7 }, (_, i): DayForm => {
    const spans = h?.days?.[i];
    return spans && spans.length > 0 ? { open: true, spans: spans.map((s) => ({ open: s.open, close: s.close })) } : { open: false, spans: [{ ...DEFAULT_SPAN }] };
  });
  return { days, note: h?.note ?? "" };
}

/** The editor's hours as the API takes them: a closed day is null. */
export function hoursFromForm(f: HoursForm): Hours {
  const days = f.days.slice(0, 7).map((d) => (d.open && d.spans.length > 0 ? d.spans.slice(0, MAX_OPENINGS).map((s) => ({ open: s.open, close: s.close })) : null));
  while (days.length < 7) days.push(null);
  const note = f.note.trim();
  return note ? { days, note } : { days };
}

/** A copy of the form with `day` set to Monday's hours. */
export function sameAsMonday(f: HoursForm, day: number): HoursForm {
  const mon = f.days[0];
  if (!mon) return f;
  return { ...f, days: f.days.map((d, i) => (i === day ? { open: mon.open, spans: mon.spans.map((s) => ({ ...s })) } : d)) };
}

const HHMM = /^([01][0-9]|2[0-3]):[0-5][0-9]$/;

/** The problems with the hours, by day (-1 for the note). */
export function hoursErrors(f: HoursForm): { day: number; message: string }[] {
  const out: { day: number; message: string }[] = [];
  f.days.forEach((d, i) => {
    if (!d.open) return;
    if (d.spans.length > MAX_OPENINGS) out.push({ day: i, message: `At most ${MAX_OPENINGS} openings a day.` });
    for (const s of d.spans) {
      if (!HHMM.test(s.open) || !HHMM.test(s.close)) {
        out.push({ day: i, message: `Give ${DAYS[i]}'s opening and closing times.` });
        break;
      }
      if (s.open === s.close) {
        out.push({ day: i, message: `${DAYS[i]} opens and closes at the same time.` });
        break;
      }
    }
  });
  if (f.note.trim().length > LIMITS.note) out.push({ day: -1, message: `The note is at most ${LIMITS.note} characters.` });
  return out;
}

/** "08:00" to "02:00" runs past midnight; the page reads it as closing the next morning. */
export const pastMidnight = (s: Span) => HHMM.test(s.open) && HHMM.test(s.close) && s.close < s.open;

// --- catalogue mapping ---

export type MappingKey = Exclude<keyof Columns, "price_unit">;

/** The catalogue's fields, the column types each takes, and what it is for. */
export const MAPPING: { key: MappingKey; label: string; types: ColumnType[]; required?: boolean; help: string }[] = [
  { key: "name", label: "Name", types: ["text"], required: true, help: "What the item is called" },
  { key: "price", label: "Price", types: ["integer", "number"], help: "Shown in naira" },
  { key: "description", label: "Description", types: ["text"], help: "A line about the item" },
  { key: "category", label: "Category", types: ["text"], help: "Groups items on the page" },
  { key: "photo", label: "Photo", types: ["text"], help: "Holds the photos you add below" },
  { key: "available", label: "Available", types: ["boolean"], help: "No hides the item" },
  { key: "sort", label: "Sort", types: ["integer", "number"], help: "Order on the page; otherwise by name" },
];

/** The columns that may fill a catalogue field: of an allowed type, and never personal. */
export function columnsFor(columns: Column[], types: readonly ColumnType[]): Column[] {
  return columns.filter((c) => !c.personal && types.includes(c.type));
}

/** The tables a catalogue can come from: those with a text column that is not personal. */
export const catalogueTables = (tables: Table[]) => tables.filter((t) => columnsFor(t.columns, ["text"]).length > 0);

const GUESSES: Record<MappingKey, string[]> = {
  name: ["name", "title", "item", "product"],
  price: ["price", "price_naira", "price_kobo", "amount", "amount_kobo", "cost"],
  description: ["description", "details", "about"],
  category: ["category", "group", "section", "type"],
  photo: ["photo", "image", "picture"],
  available: ["available", "in_stock", "active"],
  sort: ["sort", "position", "order", "rank"],
};

/** A first mapping for a newly picked table, by column names that say what they hold. */
export function guessColumns(table: Table): Columns {
  const out: Columns = { price_unit: "naira" };
  const used = new Set<string>();
  for (const m of MAPPING) {
    const allowed = columnsFor(table.columns, m.types).filter((c) => !used.has(c.name));
    const hit = GUESSES[m.key].map((g) => allowed.find((c) => c.name === g)).find(Boolean) ?? (m.required ? allowed[0] : undefined);
    if (hit) {
      out[m.key] = hit.name;
      used.add(hit.name);
    }
  }
  if (out.price?.endsWith("kobo")) out.price_unit = "kobo";
  return out;
}

/** The mapping without the fields left empty, as the API stores it. */
export function cleanColumns(c: Columns): Columns {
  const out: Columns = {};
  for (const m of MAPPING) if (c[m.key]) out[m.key] = c[m.key];
  if (out.price) out.price_unit = c.price_unit || "naira";
  return out;
}

// --- media ---

export const IMAGE_TYPES = ["image/jpeg", "image/png", "image/webp"];
export const MAX_IMAGE_BYTES = 10 << 20;

/** Why a file cannot be a storefront image, before it is sent, or null. */
export function imageProblem(file: { type: string; size: number }): string | null {
  if (!IMAGE_TYPES.includes(file.type)) return "Choose a JPEG, PNG or WebP photo.";
  if (file.size > MAX_IMAGE_BYTES) return "That photo is larger than 10 MB. Choose a smaller one.";
  return null;
}

/**
 * A stored image's smallest width, for a thumbnail, shown through the API:
 * photos live on the organisation's Xenibase host, and the app's
 * Content-Security-Policy allows images from its own origin only.
 */
export function thumb(img: Image | undefined): string | undefined {
  const u = img?.["320"] ?? img?.["800"] ?? img?.["2000"];
  return u ? viaAPI(u) : undefined;
}

/** A storefront image's URL through GET /v1/storefront/image. */
export const viaAPI = (url: string) => `/v1/storefront/image?src=${encodeURIComponent(url)}`;

// --- the editor's form ---

/** What a 404 (no storefront yet) leaves the editor to offer: the server's lists (engine/storefront). */
export const DEFAULT_TEMPLATES = ["classic", "bold"];
export const DEFAULT_ACCENTS: Record<string, string> = {
  green: "#0b6e4f", blue: "#1d4ed8", red: "#b91c1c", orange: "#c2410c",
  purple: "#6d28d9", pink: "#be185d", teal: "#0f766e", black: "#111827",
};

/** The card's text fields, edited as plain strings. */
export const TEXT_FIELDS = ["name", "tagline", "about", "logo", "area", "address", "map_query", "phone", "whatsapp"] as const;
export type TextField = (typeof TEXT_FIELDS)[number];

export interface Form {
  slug: string;
  text: Record<TextField, string>;
  template: string;
  accent: string;
  language: string;
  hours: HoursForm;
  table: string | null;
  columns: Columns;
}

/** The editor's form for a storefront, or for a new one. */
export function formFrom(sf: Pick<Storefront, "slug" | "draft" | "catalogue_table" | "catalogue_columns"> | null): Form {
  const card = sf?.draft ?? emptyCard();
  const text = Object.fromEntries(TEXT_FIELDS.map((k) => [k, card[k] ?? ""])) as Record<TextField, string>;
  return {
    slug: sf?.slug ?? "",
    text,
    template: card.template || "classic",
    accent: card.accent || "green",
    language: card.language ?? "",
    hours: hoursToForm(card.hours),
    table: sf?.catalogue_table ?? null,
    columns: { price_unit: "naira", ...(sf?.catalogue_columns ?? {}) },
  };
}

export interface StorefrontRequest {
  slug: string;
  draft: Card;
  catalogue_table: string | null;
  catalogue_columns: Columns;
}

/** The PUT /v1/storefront body for the form; empty text fields are left out. */
export function requestFrom(f: Form): StorefrontRequest {
  const draft: Card = { name: f.text.name.trim(), hours: hoursFromForm(f.hours), template: f.template as Card["template"], accent: f.accent };
  for (const k of TEXT_FIELDS) {
    const v = f.text[k].trim();
    if (k !== "name" && v) draft[k] = v;
  }
  if (f.language) draft.language = f.language;
  return { slug: f.slug.trim(), draft, catalogue_table: f.table, catalogue_columns: f.table ? cleanColumns(f.columns) : {} };
}

/** Whether two forms would save the same storefront. */
export const sameRequest = (a: Form, b: Form) => JSON.stringify(requestFrom(a)) === JSON.stringify(requestFrom(b));

/** A server refusal without the package prefixes it carries ("storefront: invalid: the name: …"). */
export function tidyMessage(msg: string): string {
  const m = msg.replace(/^((storefront|publicnames|media|invalid): )+/, "");
  return m ? m.charAt(0).toUpperCase() + m.slice(1) : msg;
}
