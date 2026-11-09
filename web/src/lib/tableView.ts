import type { Column } from "./tables";

// The Tables tool's viewer (docs/spec/chat-five-more-flows.md, phase 0):
// how each column is drawn, by its type, its name, and what the shop's
// catalogue says of it. Pure, so it is tested on its own.

export type CellKind =
  | "money"
  | "number"
  | "date"
  | "datetime"
  | "yesno"
  | "status"
  | "photo"
  | "json"
  | "text";

export interface ColumnView {
  kind: CellKind;
  /** For money: whether the cell holds kobo or naira. */
  unit?: "kobo" | "naira";
}

/** What the shop's catalogue says of its own table's columns. */
export interface ShopColumns {
  photo?: string;
  price?: string;
  price_unit?: string;
  cost?: string;
}

const MONEY_WORDS =
  /^(price|amount|cost|total|balance|fee|paid|owed|due|subtotal|discount|salary|wage|wages)(_naira)?$/i;
const STATUS_WORDS = /^(status|state|stage|payment_status|order_status)$/i;
const PHOTO_WORDS = /^(photo|photos|image|images|picture|pictures)$/i;

/** How a column is drawn. The shop's own price and photo columns are
 * known; otherwise its type, then its name ("price_kobo" is kobo, a
 * number named "amount" is naira, a text named "status" is a pill). */
export function columnView(c: Column, shop?: ShopColumns): ColumnView {
  const numeric = c.type === "integer" || c.type === "number";
  if (shop?.photo && c.name === shop.photo) return { kind: "photo" };
  if (shop?.price && c.name === shop.price && numeric)
    return {
      kind: "money",
      unit: shop.price_unit === "kobo" ? "kobo" : "naira",
    };
  if (shop?.cost && c.name === shop.cost && numeric)
    return {
      kind: "money",
      unit: shop.price_unit === "kobo" ? "kobo" : "naira",
    };
  if (numeric && /_kobo$/i.test(c.name)) return { kind: "money", unit: "kobo" };
  if (numeric && MONEY_WORDS.test(c.name))
    return { kind: "money", unit: "naira" };
  switch (c.type) {
    case "integer":
    case "number":
      return { kind: "number" };
    case "boolean":
      return { kind: "yesno" };
    case "date":
      return { kind: "date" };
    case "datetime":
      return { kind: "datetime" };
    case "json":
      return { kind: "json" };
  }
  if (c.type === "text" && STATUS_WORDS.test(c.name)) return { kind: "status" };
  if (c.type === "text" && PHOTO_WORDS.test(c.name)) return { kind: "photo" };
  return { kind: "text" };
}

/** A status's colour: done and paid are lime, waiting is amber, refused
 * and late are red, the rest plain. */
export function statusTone(v: string): "lime" | "warn" | "danger" | "plain" {
  const s = v.trim().toLowerCase();
  if (
    /^(paid|done|complete|completed|delivered|active|approved|confirmed|in stock|available|open for orders|yes|ok|accepted|received|settled)$/.test(
      s,
    )
  )
    return "lime";
  if (
    /^(pending|waiting|new|open|draft|sent|processing|in progress|on the way|partly paid|low|queried|due)$/.test(
      s,
    )
  )
    return "warn";
  if (
    /^(cancelled|canceled|failed|overdue|declined|rejected|refunded|out of stock|late|lost|closed|blocked|no)$/.test(
      s,
    )
  )
    return "danger";
  return "plain";
}

/** A money cell in kobo, whatever the column holds; null when it is not a number. */
export function moneyKobo(
  v: unknown,
  unit: "kobo" | "naira" = "naira",
): number | null {
  const n =
    typeof v === "number"
      ? v
      : typeof v === "string" && v.trim() !== ""
        ? Number(v)
        : NaN;
  if (!Number.isFinite(n)) return null;
  return unit === "kobo" ? Math.round(n) : Math.round(n * 100);
}

/** "1–25 of 312", "26–31 of 31", or "1–25" when the total is not known. */
export function pageWords(
  page: number,
  size: number,
  shown: number,
  total: number | null,
): string {
  if (shown === 0)
    return total === 0 || total === null
      ? "No rows"
      : `0 of ${total.toLocaleString("en-NG")}`;
  const from = page * size + 1;
  const to = page * size + shown;
  const span = `${from.toLocaleString("en-NG")}–${to.toLocaleString("en-NG")}`;
  return total === null ? span : `${span} of ${total.toLocaleString("en-NG")}`;
}

/** The words a person types as a pattern: % and _ are themselves. */
export function likeWords(words: string): string {
  return `%${words.replace(/[%_\\]/g, (m) => `\\${m}`)}%`;
}

/** The filter a search sends: one column, or any of them (at most 20,
 * the server's limit for a group). */
export function searchFilter(
  words: string,
  columns: string[],
): Record<string, unknown> | null {
  const cols = columns.slice(0, 20);
  if (!words || cols.length === 0) return null;
  const one = (column: string) => ({
    column,
    op: "ilike",
    value: likeWords(words),
  });
  const [only] = cols;
  return cols.length === 1 && only ? one(only) : { any: cols.map(one) };
}

export interface Sort {
  column: string;
  desc: boolean;
}

/** A header tapped: A to Z, then Z to A, then back to newest first. */
export function nextSort(sort: Sort | null, column: string): Sort | null {
  if (!sort || sort.column !== column) return { column, desc: false };
  return sort.desc ? null : { column, desc: true };
}
