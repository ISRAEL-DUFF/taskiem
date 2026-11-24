// How a line bought is treated, and its label (what a business buys §5,
// §6): the question asked on each line of an order sheet ("<item> from
// <supplier>: How should we treat it?"), the memory it is answered from,
// Taskiem's suggestion, and the labels a business spends under
// (/v1/money/labels). The words and the requests; no screens.

import { ApiError } from "../api";
import { feeKobo } from "./orders";
import type { Purchase, PurchaseLine } from "./purchases";

export type Treatment = "record" | "stock" | "track";

/** The three answers, in the question's order. */
export const TREATMENTS: { value: Treatment; label: string; hint: string }[] = [
  {
    value: "record",
    label: "Just record it",
    hint: "What it cost goes into your spending. Nothing is counted.",
  },
  {
    value: "stock",
    label: "Add to stock",
    hint: "It is counted into one of your stock items when it comes.",
  },
  {
    value: "track",
    label: "Keep track of it",
    hint: "Each one is put on Things we have when it comes.",
  },
];

export const treatmentWords = (t: Treatment | undefined): string =>
  TREATMENTS.find((x) => x.value === t)?.label ?? "Just record it";

/** A label the business spends under (spend_labels). */
export interface SpendLabel {
  id: string;
  name: string;
  merged_into?: string;
  created_by: string;
  created_at: string;
  /** Money-log entries and purchase lines carrying it. */
  uses: number;
}

export interface LabelSuggestion {
  name: string;
  /** The business's own label; none for a new name. */
  label_id?: string;
}

export interface LabelsView {
  labels: SpendLabel[];
  suggestions: LabelSuggestion[];
}

/** What the business remembers of a supplier's item: item_links for a
 * linked supplier (seller_item_id), supplier_items for one not on
 * Taskiem (name_key). */
export interface Remembered {
  link_id?: string;
  seller_item_id?: string;
  supplier_id?: string;
  name_key?: string;
  name: string;
  treatment: Treatment;
  stock_item_id?: string;
  stock_item?: string;
  uses: number;
  label_id?: string;
  label?: string;
  updated_by: string;
  updated_at: string;
}

/** A line's answer, as a sheet holds it. */
export interface Choice {
  treatment: Treatment;
  /** For stock: one of the business's stock items, or "new" to make one. */
  stockItemId?: string;
  /** A new stock item: its name, and Do you sell it? (null: not said). */
  newName?: string;
  sells?: boolean | null;
  /** Its selling price, when sold (optional). */
  price?: string;
  uses?: number;
  /** The label as typed or picked; "" for none (Not labelled). */
  label: string;
  /** Said yes to putting more than 50 on Things we have. */
  trackMany?: boolean;
  /** For a remembered item changed on one order: kept for next time too. */
  remember?: boolean;
}

export const NEW_STOCK = "new";

/** The memory as an answer. */
export const fromMemory = (m: Remembered): Choice => ({
  treatment: m.treatment,
  stockItemId: m.stock_item_id,
  uses: m.uses > 1 ? m.uses : undefined,
  label: m.label ?? "",
});

/** A line of an order as an answer (its label's name as the server sends
 * it, a merged label's the one kept; else from the business's labels). */
export const fromLine = (
  l: Pick<PurchaseLine, "treatment" | "stock_item_id" | "uses" | "label_id" | "label">,
  labels: SpendLabel[] | undefined,
): Choice => ({
  treatment: l.treatment ?? "record",
  stockItemId: l.treatment === "stock" ? l.stock_item_id : undefined,
  uses: (l.uses ?? 1) > 1 ? l.uses : undefined,
  label: lineLabel(l, labels),
});

/** A line's label as cards show it: the server's name of the label kept
 * (so a label merged since shows as the one kept), else its id looked up
 * among the business's labels; "" for none. */
export const lineLabel = (
  l: Pick<PurchaseLine, "label_id" | "label">,
  labels: SpendLabel[] | undefined,
): string => l.label || labelName(labels, l.label_id);

/** The units that count things one by one: one bought is one of the stock
 * item. As stock.AskUses. */
const COUNT_UNITS = new Set(["", "piece", "pc", "unit", "item", "each", "one", "no"]);

/** A unit word for one: bags, bag; boxes, box (stock.Singular). */
export function singular(u: string): string {
  const l = u.toLowerCase();
  if (l.length > 3 && l.endsWith("ies")) return u.slice(0, -3) + "y";
  if (["xes", "ches", "shes", "sses"].some((s) => l.endsWith(s))) return u.slice(0, -2);
  if (l.length > 2 && l.endsWith("s") && !l.endsWith("ss")) return u.slice(0, -1);
  return u;
}

const unitKey = (u: string | undefined) => singular((u ?? "").trim()).toLowerCase();

/** Whether a line bought in lineUnit, going into a stock item counted in
 * stockUnit, asks how many of the stock item one of it is (g-uses-ask):
 * counted in another unit (grams, yards) with nothing saying it yet. The
 * server's stock.AskUses, and the app's askUses, by the same rule. */
export function askUses(
  lineUnit: string | undefined,
  stockUnit: string | undefined,
  uses?: number,
): boolean {
  if ((uses ?? 1) > 1) return false;
  const s = unitKey(stockUnit);
  return !COUNT_UNITS.has(s) && unitKey(lineUnit) !== s;
}

/** The uses question: "How many g is one bag?" (one of the item when the
 * line has no unit). */
export const usesQuestion = (stockUnit: string, lineUnit: string | undefined, item: string) =>
  `How many ${stockUnit} is one ${lineUnit?.trim() ? singular(lineUnit.trim()) : item}?`;

/** Why a uses typed will be refused, or "" (empty is 1). */
export function usesProblem(s: string): string {
  const t = s.trim().replace(/,/g, "");
  if (t === "") return "";
  if (!/^\d+$/.test(t) || Number(t) < 1 || Number(t) > 1_000_000) return "A whole number, like 1000.";
  return "";
}

/** A label's name by its id ("" when none or not among them). */
export const labelName = (
  labels: SpendLabel[] | undefined,
  id: string | undefined,
): string => (id ? (labels?.find((x) => x.id === id)?.name ?? "") : "");

/** A name as the business's memory matches it: without case or extra
 * spaces, so "Diesel" and "diesel " are one item (stock.NameKey). */
export const nameKey = (s: string) => s.trim().replace(/\s+/g, " ").toLowerCase();
const key = nameKey;

/** Taskiem's guess for an item it has no memory of (§6), never a
 * decision: a stock item of that name: add to it; something of that name
 * kept track of before: keep track of it; else just record it. The label
 * is the first suggestion. */
export function suggestChoice(
  name: string,
  stockItems: { id: string; name: string; retired?: boolean }[] | undefined,
  tracked: string[] | undefined,
  suggestions: LabelSuggestion[] | undefined,
): Choice {
  const k = key(name);
  const label = suggestions?.[0]?.name ?? "";
  const item = stockItems?.find((s) => !s.retired && key(s.name) === k);
  if (item) return { treatment: "stock", stockItemId: item.id, label };
  if (tracked?.some((t) => key(t) === k)) return { treatment: "track", label };
  return { treatment: "record", label };
}

/** The labels to pick from: the business's, most used first, then the
 * suggestions it does not have, at most `most` in all. */
export function labelChoices(
  labels: SpendLabel[] | undefined,
  suggestions: LabelSuggestion[] | undefined,
  most = 6,
): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  const add = (n: string) => {
    if (out.length >= most || seen.has(key(n)) || n.trim() === "") return;
    seen.add(key(n));
    out.push(n);
  };
  const s = suggestions ?? [];
  // A suggestion of the business's own goes first: it shares a word with
  // the item's name.
  for (const x of s) if (x.label_id) add(x.name);
  for (const l of [...(labels ?? [])].sort((a, b) => b.uses - a.uses)) add(l.name);
  for (const x of s) add(x.name);
  return out;
}

/** What is still to say before the line can go ("" when nothing). */
export function choiceProblem(c: Choice): string {
  if (c.label.trim().length > 60) return "A label is up to 60 characters.";
  if (c.treatment !== "stock") return "";
  if (!c.stockItemId) return "Say which stock item it goes into.";
  if (c.stockItemId === NEW_STOCK) {
    if (!c.newName?.trim()) return "Give the new stock item a name.";
    if (c.sells === null || c.sells === undefined)
      return "Say whether you sell it.";
    if (c.sells && c.price?.trim() && !(feeKobo(c.price) > 0))
      return "A selling price in naira, like 25,000.";
  }
  return "";
}

/** The answer in a few words, as a remembered line shows it: "Keep track
 * of it · Machines", "Add to stock: Ankara fabric · Stock bought". */
export function choiceWords(
  c: Choice,
  stockName?: (id: string) => string | undefined,
): string {
  let t = treatmentWords(c.treatment);
  if (c.treatment === "stock") {
    const n =
      c.stockItemId === NEW_STOCK
        ? c.newName
        : c.stockItemId
          ? stockName?.(c.stockItemId)
          : undefined;
    if (n) t += `: ${n}`;
    if (c.uses && c.uses > 1) t += ` (${c.uses} each)`;
  }
  return `${t} · ${c.label.trim() || "Not labelled"}`;
}

/** Whether two answers say the same (what a line differs from its memory
 * by). */
export const sameChoice = (a: Choice, b: Choice) =>
  a.treatment === b.treatment &&
  (a.treatment !== "stock" ||
    (a.stockItemId === b.stockItemId && (a.uses ?? 1) === (b.uses ?? 1))) &&
  key(a.label) === key(b.label);

/** A line's treatment as the API takes it, once the label and a new
 * stock item have ids. */
export interface LineBody {
  treatment: Treatment;
  stock_item_id?: string;
  uses?: number;
  label_id?: string;
  track_many?: boolean;
}

export function lineBody(
  c: Choice,
  ids: { stockItemId?: string; labelId?: string },
): LineBody {
  const out: LineBody = { treatment: c.treatment };
  if (c.treatment === "stock") {
    out.stock_item_id = ids.stockItemId;
    if (c.uses && c.uses > 1) out.uses = c.uses;
  }
  if (ids.labelId) out.label_id = ids.labelId;
  if (c.treatment === "track" && c.trackMany) out.track_many = true;
  return out;
}

/** The label of that name the business has, by id, when it has it. */
export const knownLabel = (
  labels: SpendLabel[] | undefined,
  name: string,
): string | undefined =>
  name.trim() ? labels?.find((l) => key(l.name) === key(name))?.id : undefined;

/** POST /v1/stock/items for a new stock item made from the question: a
 * product when it is sold (its price when given), else a material. */
export function newStockRequest(c: Choice, unit?: string) {
  const base = { name: (c.newName ?? "").trim(), unit: unit ?? "", quantity: 0 };
  if (!c.sells) return { ...base, material: true };
  const k = c.price?.trim() ? feeKobo(c.price) : NaN;
  return k > 0 ? { ...base, price_kobo: k } : base;
}

/** The stock item a made item is counted by: a material's own id, a
 * product's stock_item_id. */
export const madeStockItem = (item: {
  id: string;
  material?: boolean;
  stock_item_id?: string;
}) => (item.material ? item.id : (item.stock_item_id ?? item.id));

/** A drafting refused because a line asks first (409 track_ask) or
 * cannot be tracked one by one (422 track_too_many). */
export interface TrackAsk {
  /** The line's position on the order. */
  line: number;
  item: string;
  quantity: number;
  tooMany: boolean;
}

export function trackAskOf(e: unknown): TrackAsk | null {
  if (!(e instanceof ApiError)) return null;
  const code = e.body?.code;
  if (code !== "track_ask" && code !== "track_too_many") return null;
  return {
    line: Number(e.body.line ?? 0),
    item: String(e.body.item ?? ""),
    quantity: Number(e.body.quantity ?? 0),
    tooMany: code === "track_too_many",
  };
}

/** "Put 500 nylon bags on Things we have, one row each?" */
export function trackAskWords(a: TrackAsk): string {
  if (a.tooMany)
    return `${a.quantity} ${a.item} are too many to keep track of one by one. Just record them, and add one row for the lot on Things we have.`;
  return `Put ${a.quantity} ${a.item} on Things we have, one row each?`;
}

/** Whether a line can no longer be changed: something on it came. */
export const lineLocked = (l: Pick<PurchaseLine, "received">) => l.received > 0;

/** A line's small mark on an order or bill card: where it went. */
export function lineMark(
  t: Treatment | undefined,
): { word: string; icon: "package" | "tag" } | null {
  if (t === "stock") return { word: "To stock", icon: "package" };
  if (t === "track") return { word: "On Things we have", icon: "tag" };
  return null;
}

/** A label's line on the Labels page: "Used 12 times". */
export const usesWords = (n: number) =>
  n === 0 ? "Not used yet" : n === 1 ? "Used once" : `Used ${n} times`;

/** Why a new name for a label will be refused, or "". */
export function labelNameProblem(
  name: string,
  labels: SpendLabel[] | undefined,
  self?: string,
): string {
  const n = name.trim();
  if (n === "") return "Give the label a name.";
  if (n.length > 60) return "A label is up to 60 characters.";
  const other = labels?.find((l) => l.id !== self && key(l.name) === key(n));
  if (other) return `${other.name} is already a label: merge them instead.`;
  return "";
}

/** What an order still asks of the business: "Say how to treat 2 items"
 * ("" when nothing, or when it is cancelled or waits on their quote,
 * which asks itself). */
export function toAnswer(
  p: Pick<Purchase, "lines" | "status" | "seller_status">,
): string {
  if (p.status === "cancelled" || p.seller_status === "proposed") return "";
  const n = p.lines.filter((l) => l.unconfirmed && l.received === 0).length;
  if (n === 0) return "";
  return n === 1 ? "Say how to treat 1 item" : `Say how to treat ${n} items`;
}

/** A line of an order by hand, as its form holds it. */
export interface HandLine {
  item: string;
  quantity: string;
  /** The price of one, in naira ("" for none). */
  price: string;
}

export const emptyHandLine = (): HandLine => ({ item: "", quantity: "1", price: "" });

/** Why an order by hand will be refused, or "". */
export function handProblem(supplier: string, lines: HandLine[]): string {
  if (!supplier) return "Say which supplier it is to.";
  const filled = lines.filter((l) => l.item.trim() !== "");
  if (filled.length === 0) return "Add what you are ordering.";
  if (filled.length > 50) return "An order has at most 50 lines.";
  for (const l of filled) {
    if (!/^\d+$/.test(l.quantity.trim()) || Number(l.quantity) <= 0)
      return `How many ${l.item.trim()} is a whole number, like 2.`;
    if (l.price.trim() && Number.isNaN(feeKobo(l.price)))
      return `The price of ${l.item.trim()} is in naira, like 15,000.`;
  }
  return "";
}

/** A line by hand as POST /v1/stock/purchases takes it, before its
 * treatment. */
export const handLineRequest = (l: HandLine) => ({
  item: l.item.trim(),
  quantity: Number(l.quantity.trim()),
  ...(l.price.trim() ? { cost_kobo: feeKobo(l.price) } : {}),
});

/** The order line a bill line was raised from: the same seller's item on
 * the order it names; undefined for a line on no order line (an extra, a
 * delivery charge: Not labelled). */
export function billLineOf(
  l: { seller_item_id?: string; purchase_id?: string },
  orders: Pick<Purchase, "id" | "lines">[] | undefined,
): PurchaseLine | undefined {
  if (!l.purchase_id || !l.seller_item_id) return undefined;
  return orders
    ?.find((o) => o.id === l.purchase_id)
    ?.lines.find((x) => x.seller_item_id === l.seller_item_id);
}
