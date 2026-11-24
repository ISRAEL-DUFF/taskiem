// A shop's stock (docs/stock.md; GET /v1/stock): the API's shapes, how an
// item and a movement read in a row, and the forms for adding an item,
// changing its columns, and moving its stock.

import { whenLabel } from "./bookings";
import { feeInput, feeKobo } from "./orders";
import { formatPrice, type Image } from "./storefront";

export interface StockItem {
  id: string;
  name: string;
  available: boolean;
  /** null when the row holds no count. */
  stock: number | null;
  unit?: string;
  price_kobo?: number;
  /** The last price paid for one. */
  cost_kobo?: number;
  reorder_level?: number;
  target_level?: number;
  supplier?: string;
  /** The kind of thing it is ("Sneakers"), when the catalogue has a
   * category column. */
  category?: string;
  /** Its words on the storefront, when the catalogue maps them. */
  description?: string;
  /** Sizes and colours: the product this is one of, and its values. */
  variant_of?: string;
  variant?: Record<string, string>;
  /** A product's choices, when it has sizes and colours (it then keeps no
   * stock of its own: theirs is its). */
  choices?: { name: string; values: string[] }[];
  /** A product's sizes and colours with a price of their own (by their
   * values, lower case: "colour=black|size=42"); the rest follow its. */
  own_price?: string[];
  /** A size's own photo (its colour's), read from the table. */
  photo?: Image;
  low?: boolean;
  /** How many to order, when low. */
  suggest?: number;
  /** A stock item no product is counted by (what a business buys §4.1):
   * its id is the stock item's. */
  material?: boolean;
  /** A product's: the stock item it is counted by, and how many of it one
   * sale uses. */
  stock_item_id?: string;
  uses?: number;
}

export type MovementReason =
  | "order"
  | "sale"
  | "received"
  | "adjustment"
  | "count"
  | "moved";

export interface Movement {
  id: string;
  item_id: string;
  item: string;
  reason: MovementReason;
  delta?: number;
  set_to?: number;
  before: number | null;
  after: number | null;
  change: number | null;
  short?: number;
  note?: string;
  reference?: string;
  by: string;
  created_at: string;
  done_at: string | null;
}

export interface StockView {
  keeps_stock: boolean;
  catalogue: string | null;
  items: StockItem[];
  low?: number;
  /** What the counted items are worth at cost. */
  value_kobo?: number;
  /** The page is published (items without a price are hidden from it). */
  published?: boolean;
}

/** A unit for more than one: bag, bags; box, boxes; battery, batteries. */
export function plural(unit: string): string {
  const l = unit.toLowerCase();
  if (/(s|x|ch|sh)$/.test(l))
    return l.endsWith("s") && !l.endsWith("ss") ? unit : unit + "es";
  if (/[^aeiou]y$/.test(l)) return unit.slice(0, -1) + "ies";
  return unit + "s";
}

/** n of an item: "12 bags", "1 carton", "4". */
export function quantity(n: number, unit?: string): string {
  const u = unit?.trim();
  if (!u) return String(n);
  return `${n} ${Math.abs(n) === 1 ? u : plural(u)}`;
}

/** An item's stock as a row's side says it: "48 cartons", "No count". */
export const stockLine = (it: StockItem) =>
  it.stock === null ? "No count" : quantity(it.stock, it.unit);

/** Why a movement was made, in a word or two. */
export const REASON: Record<MovementReason, string> = {
  order: "Order",
  sale: "Sold",
  received: "Received",
  adjustment: "Adjusted",
  count: "Counted",
  moved: "Moved",
};

/** A movement's title: "Indomie: received 50". */
export function movementTitle(m: Movement): string {
  const n = m.change ?? m.delta ?? m.set_to ?? 0;
  if (m.reason === "count")
    return `${m.item}: counted ${m.set_to ?? m.after ?? n}`;
  const sign = n > 0 ? "+" : "";
  return `${m.item}: ${REASON[m.reason].toLowerCase()} ${sign}${n}`;
}

/** A movement underneath its title: when, the count it left, what it came from, who, and why. */
export function movementSub(m: Movement): string {
  const parts = [whenLabel(m.done_at ?? m.created_at)];
  if (m.before !== null && m.after !== null)
    parts.push(`${m.before} → ${m.after}`);
  if (m.short) parts.push(`${m.short} more asked for than there was`);
  if (m.reference)
    parts.push(m.reason === "order" ? `order ${m.reference}` : m.reference);
  if (m.note) parts.push(m.note);
  return parts.join(" · ");
}

// --- the forms ---

export interface ItemForm {
  name: string;
  quantity: string;
  unit: string;
  cost: string;
  price: string;
  reorder: string;
  target: string;
  supplier: string;
}

export const emptyItem = (): ItemForm => ({
  name: "",
  quantity: "",
  unit: "",
  cost: "",
  price: "",
  reorder: "",
  target: "",
  supplier: "",
});

const whole = (v: string) => (/^\d+$/.test(v.trim()) ? Number(v.trim()) : NaN);

/** Why an item form will be refused, or null. adding checks the quantity too. */
export function itemProblem(f: ItemForm, adding: boolean): string | null {
  if (f.name.trim() === "") return "Give the item a name.";
  if (f.name.trim().length > 120) return "A name is up to 120 characters.";
  if (
    adding &&
    f.quantity.trim() !== "" &&
    !(whole(f.quantity) >= 0 && whole(f.quantity) <= 1_000_000)
  )
    return "How many there are is a whole number, like 50.";
  if (f.unit.trim().length > 30)
    return "A unit is one word, like bag or carton.";
  for (const [v, what] of [
    [f.cost, "The cost price"],
    [f.price, "The selling price"],
  ] as const) {
    if (v.trim() !== "" && Number.isNaN(feeKobo(v)))
      return `${what} is an amount, like 8500.`;
  }
  for (const [v, what] of [
    [f.reorder, "The reorder level"],
    [f.target, "The level to order up to"],
  ] as const) {
    if (v.trim() !== "" && Number.isNaN(whole(v)))
      return `${what} is a whole number.`;
  }
  if (
    f.reorder.trim() !== "" &&
    f.target.trim() !== "" &&
    whole(f.target) <= whole(f.reorder)
  )
    return "Order up to more than the reorder level.";
  return null;
}

const opt = (v: string, conv: (s: string) => number) =>
  v.trim() === "" ? undefined : conv(v);

/** The POST /v1/stock/items body for Add something to count: sold, it
 * is a product counted by a new stock item; not sold, a material (no
 * price). */
export const addRequest = (f: ItemForm, sells: boolean) => ({
  name: f.name.trim(),
  quantity: f.quantity.trim() === "" ? 0 : whole(f.quantity),
  unit: f.unit.trim(),
  cost_kobo: opt(f.cost, feeKobo),
  ...(sells ? { price_kobo: opt(f.price, feeKobo) } : { material: true }),
  reorder_level: opt(f.reorder, whole),
  target_level: opt(f.target, whole),
  supplier: f.supplier.trim(),
});

/** The PATCH /v1/stock/items/{id} body: what changed from before only
 * (a field cleared is left as it was). */
export function changeRequest(
  before: ItemForm,
  f: ItemForm,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (f.name.trim() !== "" && f.name.trim() !== before.name)
    out.name = f.name.trim();
  if (f.unit.trim() !== before.unit) out.unit = f.unit.trim();
  if (f.supplier.trim() !== before.supplier) out.supplier = f.supplier.trim();
  if (f.cost.trim() !== before.cost && f.cost.trim() !== "")
    out.cost_kobo = feeKobo(f.cost);
  if (f.price.trim() !== before.price && f.price.trim() !== "")
    out.price_kobo = feeKobo(f.price);
  if (f.reorder.trim() !== before.reorder && f.reorder.trim() !== "")
    out.reorder_level = whole(f.reorder);
  if (f.target.trim() !== before.target && f.target.trim() !== "")
    out.target_level = whole(f.target);
  return out;
}

/** A change to an item's stock from the web. */
export interface MoveForm {
  kind: "received" | "adjustment" | "count";
  amount: string;
  /** Adjustments: taken off (spoilt, lost) rather than added (found). */
  off: boolean;
  note: string;
}

/** Why a stock change will be refused, or null. */
export function moveProblem(f: MoveForm): string | null {
  const n = whole(f.amount);
  if (Number.isNaN(n))
    return f.kind === "count"
      ? "Enter how many you counted, like 12."
      : "Enter how many, like 5.";
  if (f.kind !== "count" && n === 0) return "Enter how many, more than 0.";
  if (n > 1_000_000) return "That is more than Taskiem keeps for one item.";
  if (f.kind === "adjustment" && f.note.trim() === "")
    return "Say why, like spoilt or found.";
  if (f.note.length > 200) return "The reason is up to 200 characters.";
  return null;
}

/** What a stock change is made on: a stock item's id (or a product's),
 * its count and unit. */
export type Counted = { id: string; stock: number | null; unit?: string };

/** The POST /v1/stock/movements body. */
export function moveRequest(it: Counted, f: MoveForm) {
  const n = whole(f.amount);
  const body: Record<string, unknown> = {
    item_id: it.id,
    reason: f.kind,
    note: f.note.trim(),
  };
  if (f.kind === "count") body.count = n;
  else body.change = f.kind === "adjustment" && f.off ? -n : n;
  return body;
}

/** What the stock will be after a change, as the form shows it ("48 → 45 cartons"). */
export function movePreview(it: Counted, f: MoveForm): string | null {
  const n = whole(f.amount);
  if (Number.isNaN(n) || it.stock === null) return null;
  const to =
    f.kind === "count"
      ? n
      : Math.max(0, it.stock + (f.kind === "adjustment" && f.off ? -n : n));
  return `${it.stock} → ${quantity(to, it.unit)}`;
}

// --- sales (docs/stock.md "Sales"; /v1/stock/sales) ---

export type PaidHow = "cash" | "transfer" | "pos";
/** How a recorded sale was paid: as the form's ways, or on credit (chat's "credit to Ada"), which no money pays yet. */
export type SaleHow = PaidHow | "credit";

export interface SaleLine {
  item_id: string;
  item: string;
  quantity: number;
  unit?: string;
  price_kobo: number;
  line_kobo: number;
}

export interface Sale {
  id: string;
  reference: string;
  lines: SaleLine[];
  amount_kobo: number;
  paid_how: SaleHow;
  /** recorded (cash), waiting (its money not yet seen), matched, or credit (owed by a customer, on their debts). */
  status: "recorded" | "waiting" | "matched" | "credit";
  entry_id?: string;
  matched_how?: string;
  note?: string;
  by: string;
  sold_at: string;
  /** Items sold beyond the count there was. */
  short?: string[];
}

/** A terminal's sale with no items said yet. */
export interface UnsoldEntry {
  entry_id: string;
  amount_kobo: number;
  channel: string;
  at: string;
}

export interface SalesDay {
  day: string;
  total_kobo: number;
  by_how: Partial<Record<SaleHow, number>>;
  count: number;
  waiting: number;
  top: { item: string; unit?: string; quantity: number; total_kobo: number }[];
  unsold: UnsoldEntry[];
  sales: Sale[];
}

/** How a sale was paid, as a word. */
export const PAID_HOW: Record<PaidHow, string> = {
  cash: "Cash",
  transfer: "Transfer",
  pos: "Terminal",
};

/** How a recorded sale was paid, as a word: the form's ways and on credit. */
export const SALE_HOW: Record<SaleHow, string> = { ...PAID_HOW, credit: "On credit" };

/** A day's takings by how they were paid, the ways with none left out. */
export function byHowLine(d: SalesDay): string {
  return (Object.keys(SALE_HOW) as SaleHow[])
    .filter((h) => (d.by_how[h] ?? 0) > 0)
    .map((h) => `${SALE_HOW[h]} ${formatPrice(d.by_how[h] ?? 0)}`)
    .join(" · ");
}

/** A sale's title: what was sold. */
export const saleTitle = (s: Sale) =>
  s.lines.map((l) => `${quantity(l.quantity, l.unit)} ${l.item}`).join(", ");

/** A sale underneath its title: when, how paid, whether its money came, its reference. */
export function saleSub(s: Sale): string {
  const parts = [whenLabel(s.sold_at), SALE_HOW[s.paid_how]];
  if (s.status === "waiting")
    parts.push(
      s.paid_how === "pos"
        ? "waiting for the terminal's report"
        : "waiting for the transfer",
    );
  else if (s.status === "matched") parts.push("money came in");
  parts.push(s.reference);
  if (s.short?.length)
    parts.push(`more ${s.short.join(", ")} sold than counted`);
  if (s.note) parts.push(s.note);
  return parts.join(" · ");
}

export interface SaleForm {
  lines: { item_id: string; quantity: string; price: string }[];
  /** The amount taken, when it differs from the prices. */
  amount: string;
  how: PaidHow;
  note: string;
}

export const emptySale = (how: PaidHow = "cash"): SaleForm => ({
  lines: [{ item_id: "", quantity: "1", price: "" }],
  amount: "",
  how,
  note: "",
});

/** A line's price in kobo: as typed, else the item's, else NaN. */
function linePrice(items: StockItem[], l: SaleForm["lines"][number]): number {
  if (l.price.trim() !== "") return feeKobo(l.price);
  return items.find((i) => i.id === l.item_id)?.price_kobo ?? NaN;
}

/** The sale's total from its lines' prices, or null when one has none. */
export function saleTotal(items: StockItem[], f: SaleForm): number | null {
  let sum = 0;
  for (const l of f.lines) {
    const p = linePrice(items, l);
    const n = whole(l.quantity);
    if (Number.isNaN(p) || Number.isNaN(n)) return null;
    sum += p * n;
  }
  return sum;
}

/** Why a sale will be refused, or null. */
export function saleProblem(items: StockItem[], f: SaleForm): string | null {
  if (f.lines.length === 0) return "Add what was sold.";
  for (const l of f.lines) {
    const it = items.find((i) => i.id === l.item_id);
    if (!it) return "Choose the item sold on each line.";
    const n = whole(l.quantity);
    if (Number.isNaN(n) || n <= 0 || n > 1_000_000)
      return `How many ${it.name} is a whole number, like 2.`;
    if (l.price.trim() !== "" && Number.isNaN(feeKobo(l.price)))
      return `The price of ${it.name} is an amount, like 4500.`;
  }
  if (f.amount.trim() !== "") {
    const a = feeKobo(f.amount);
    if (Number.isNaN(a) || a <= 0)
      return "The amount taken is an amount, like 45000.";
  } else {
    const missing = f.lines
      .filter((l) => l.price.trim() === "")
      .map((l) => items.find((i) => i.id === l.item_id))
      .find((it) => it && it.price_kobo === undefined);
    if (missing)
      return `${missing.name} has no price: give its price or the amount taken.`;
    if (saleTotal(items, f) === 0) return "The sale comes to ₦0.";
  }
  if (f.note.length > 200) return "The note is up to 200 characters.";
  return null;
}

/** The POST /v1/stock/sales body; entry gives a terminal's sale its
 * items, for the amount the terminal reported. */
export function saleRequest(f: SaleForm, entry?: UnsoldEntry) {
  const body: Record<string, unknown> = {
    lines: f.lines.map((l) => ({
      item_id: l.item_id,
      quantity: whole(l.quantity),
      price_kobo: l.price.trim() === "" ? undefined : feeKobo(l.price),
    })),
    paid_how: entry ? "pos" : f.how,
    note: f.note.trim(),
  };
  if (entry) {
    body.amount_kobo = entry.amount_kobo;
    body.entry_id = entry.entry_id;
  } else if (f.amount.trim() !== "") body.amount_kobo = feeKobo(f.amount);
  return body;
}

/** A terminal's sale's form: paid on the terminal, for its amount. */
export const unsoldSale = (u: UnsoldEntry): SaleForm => ({
  ...emptySale("pos"),
  amount: feeInput(u.amount_kobo),
});

// --- counts and shrinkage (docs/stock.md "Counts and shrinkage") ---

export interface CountLine {
  item_id: string;
  item: string;
  unit?: string;
  expected: number | null;
  counted: number;
  cost_kobo?: number;
}

export interface StockCount {
  id: string;
  reference: string;
  lines: CountLine[];
  /** Every item was counted. */
  full: boolean;
  reason?: string;
  lost_kobo: number;
  by: string;
  counted_at: string;
  changed: number;
}

export interface Shrink {
  from: string;
  to: string;
  received: number;
  sold: number;
  lost: number;
  lost_kobo: number;
  items: {
    item_id: string;
    item: string;
    received: number;
    sold: number;
    lost: number;
    lost_kobo: number;
  }[];
}

/** What was counted, by item id, as the sheet holds it ("" not counted). */
export type CountSheet = Record<string, string>;

/** Why a count will be refused, or null. */
export function countProblem(f: CountSheet): string | null {
  const filled = Object.values(f).filter((v) => v.trim() !== "");
  if (filled.length === 0) return "Enter how many of at least one item.";
  if (filled.some((v) => Number.isNaN(whole(v))))
    return "How many is a whole number, like 12.";
  return null;
}

/** The POST /v1/stock/counts body: the items counted. */
export const countRequest = (f: CountSheet, reason: string) => ({
  lines: Object.entries(f)
    .filter(([, v]) => v.trim() !== "")
    .map(([item_id, v]) => ({ item_id, count: whole(v) })),
  reason: reason.trim(),
});

/** How a counted item differs from what Taskiem has: "15 → 12 (3 fewer)". */
export function countDiff(it: StockItem, v: string): string | null {
  const n = whole(v);
  if (v.trim() === "" || Number.isNaN(n)) return null;
  if (it.stock === null) return `${quantity(n, it.unit)}, first count`;
  if (n === it.stock) return "as Taskiem has it";
  const d = n - it.stock;
  return `${it.stock} → ${quantity(n, it.unit)} (${Math.abs(d)} ${d < 0 ? "fewer" : "more"})`;
}

/** A count's sheet from a CSV of item names and counts ("name,count" per
 * line, a heading line allowed); unknown names the rows no item matches. */
export function countFromCSV(
  text: string,
  items: StockItem[],
): { sheet: CountSheet; unknown: string[] } {
  const byName = new Map(items.map((i) => [i.name.trim().toLowerCase(), i]));
  const sheet: CountSheet = {};
  const unknown: string[] = [];
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (line === "") continue;
    const cells = csvCells(line);
    const name = cells[0] ?? "";
    const count = (cells.at(-1) ?? "").replace(/,/g, "");
    if (cells.length < 2 || name === "") continue;
    if (!/^\d+$/.test(count)) continue; // a heading, or not a count
    const it = byName.get(name.toLowerCase());
    if (!it) unknown.push(name);
    else sheet[it.id] = count;
  }
  return { sheet, unknown };
}

/** A CSV line's cells: split on commas, semicolons or tabs outside
 * double quotes, quotes taken off. */
function csvCells(line: string): string[] {
  const out: string[] = [];
  let cell = "";
  let quoted = false;
  for (const ch of line) {
    if (ch === '"') quoted = !quoted;
    else if (!quoted && (ch === "," || ch === ";" || ch === "\t")) {
      out.push(cell.trim());
      cell = "";
    } else cell += ch;
  }
  out.push(cell.trim());
  return out;
}

/** A period's shrinkage in a line. */
export function shrinkLine(s: Shrink): string {
  if (s.received === 0 && s.sold === 0 && s.lost === 0)
    return "Nothing moved yet this month.";
  const lost =
    s.lost === 0
      ? "nothing lost"
      : `${s.lost} lost${s.lost_kobo ? ` (${formatPrice(s.lost_kobo)} at cost)` : ""}`;
  return `${s.received} in, ${s.sold} sold, ${lost}`;
}

// --- stock items (what a business buys §4.5; GET /v1/stock/stock-items) ---

/** A product a stock item is sold as: one sale of it takes uses of it. */
export interface SoldAs {
  product_id: string;
  name: string;
  uses: number;
  price_kobo: number | null;
  variant: Record<string, string> | null;
  available: boolean;
}

/** A row of the Stock page: something counted, with what it is sold as
 * (none: a material, Not sold). */
export interface StockItemRow {
  id: string;
  name: string;
  unit: string;
  category: string;
  count: number;
  cost_kobo: number | null;
  low_level: number | null;
  target_level: number | null;
  supplier_id: string | null;
  supplier: string;
  /** A size's or colour's: the product it is one of, and its name. */
  group_of: string;
  group_name: string;
  low: boolean;
  suggest: number | null;
  value_kobo: number;
  retired: boolean;
  sold_as: SoldAs[];
}

export type StockShow = "all" | "sold" | "not_sold";

/** The Stock page's filter, in words. */
export const SHOW: Record<StockShow, string> = {
  all: "All",
  sold: "Sold",
  not_sold: "Not sold",
};

export interface StockItemsView {
  keeps_stock: boolean;
  /** Of those listed: what they are worth at cost, and how many are low. */
  value_kobo: number;
  low: number;
  items: StockItemRow[];
}

/** A stock item's count as a row's side says it: "48 cartons". */
export const rowCount = (r: Pick<StockItemRow, "count" | "unit">) =>
  quantity(r.count, r.unit);

/** What one sale of a product takes of its stock item, when more than
 * one: "uses 6 bottles". */
export const usesWords = (uses: number, unit?: string) =>
  `uses ${quantity(uses, unit)}`;

/** What a stock item is sold as: "Sold as Malt, pack of 6 (uses 6
 * bottles) and Malt bottle", or "Not sold". */
export function soldAsLine(r: Pick<StockItemRow, "sold_as" | "unit">): string {
  if (r.sold_as.length === 0) return "Not sold";
  const names = r.sold_as.map((s) =>
    s.uses > 1 ? `${s.name} (${usesWords(s.uses, r.unit)})` : s.name,
  );
  const last = names.pop();
  return `Sold as ${names.length ? `${names.join(", ")} and ${last}` : last}`;
}

/** A stock item underneath what it is sold as: its levels, cost, supplier
 * and what to order. */
export function rowSub(r: StockItemRow): string {
  const parts: string[] = [];
  if (r.low_level !== null) parts.push(`reorder at ${r.low_level}`);
  if (r.target_level !== null) parts.push(`up to ${r.target_level}`);
  if (r.cost_kobo !== null) parts.push(`cost ${formatPrice(r.cost_kobo)}`);
  if (r.supplier) parts.push(r.supplier);
  if (r.low && r.suggest) parts.push(`order ${quantity(r.suggest, r.unit)}`);
  return parts.join(" · ") || "No levels yet";
}

/** The list's line: how many, what they are worth at cost, how many are low. */
export function worthLine(v: StockItemsView, show: StockShow): string {
  const n = v.items.length;
  const what =
    show === "sold"
      ? `${n} sold`
      : show === "not_sold"
        ? `${n} not sold`
        : `${n} counted`;
  const parts = [what, `worth ${formatPrice(v.value_kobo)} at cost`];
  if (v.low > 0) parts.push(`${v.low} low`);
  return parts.join(" · ");
}

/** The Stock page's rows: a stock item of its own, or a product sold in
 * sizes and colours (group_of) with theirs under it, the group's line
 * first. */
export type StockListRow =
  | { kind: "item"; row: StockItemRow; group?: string }
  | {
      kind: "group";
      id: string;
      name: string;
      count: number;
      sizes: number;
      low: number;
    };

export function stockList(items: StockItemRow[]): StockListRow[] {
  const groups = new Map<string, StockItemRow[]>();
  for (const r of items)
    if (r.group_of) groups.set(r.group_of, [...(groups.get(r.group_of) ?? []), r]);
  const out: StockListRow[] = [];
  const done = new Set<string>();
  for (const r of items) {
    if (!r.group_of) {
      out.push({ kind: "item", row: r });
      continue;
    }
    if (done.has(r.group_of)) continue;
    done.add(r.group_of);
    const g = groups.get(r.group_of) ?? [];
    const name = r.group_name || r.name;
    out.push({
      kind: "group",
      id: r.group_of,
      name,
      count: g.reduce((a, x) => a + Math.max(0, x.count), 0),
      sizes: g.length,
      low: g.filter((x) => x.low).length,
    });
    for (const x of g) out.push({ kind: "item", row: x, group: name });
  }
  return out;
}

/** How a stock item is changed from the Stock page: the id PATCH
 * /v1/stock/items/{id} takes (a material's own; else the product it is
 * sold as, whose stock item it is), and whether its name and price can be
 * changed here. A name and a price are a product's: only a stock item sold
 * as one product, one for one, changes them here; a size's follow its
 * product. */
export function rowEdit(r: StockItemRow): {
  id: string;
  name: boolean;
  price: boolean;
} {
  const first = r.sold_as[0];
  if (!first) return { id: r.id, name: true, price: false };
  const only = r.sold_as.length === 1 && first.uses === 1 && !r.group_of;
  return { id: first.product_id, name: only, price: only };
}

/** The change form of a stock item, as it is. */
export function rowForm(r: StockItemRow): ItemForm {
  const p = rowEdit(r).price ? (r.sold_as[0]?.price_kobo ?? null) : null;
  return {
    name: r.name,
    quantity: "",
    unit: r.unit,
    cost: r.cost_kobo === null ? "" : feeInput(r.cost_kobo),
    price: p === null ? "" : feeInput(p),
    reorder: r.low_level === null ? "" : String(r.low_level),
    target: r.target_level === null ? "" : String(r.target_level),
    supplier: r.supplier,
  };
}

/** A stock item as a stock change and its history name it: by its own id,
 * which the movements take for either kind. */
export const rowCounted = (r: StockItemRow): Counted => ({
  id: r.id,
  stock: r.count,
  unit: r.unit,
});

/** What a product's form says it is counted by: "Malt bottle · uses 6
 * bottles · 20 bottles left", or why it is not. */
export function countedByLine(
  it: Pick<StockItem, "stock_item_id" | "uses" | "choices">,
  rows: StockItemRow[] | undefined,
): string {
  if ((it.choices?.length ?? 0) > 0)
    return "Sold in sizes and colours: each is counted by its own";
  if (!it.stock_item_id) return "Not counted: selling it changes no count";
  const r = rows?.find((x) => x.id === it.stock_item_id);
  if (!r) return "Loading…";
  const parts = [r.name];
  if ((it.uses ?? 1) > 1) parts.push(`one sale ${usesWords(it.uses ?? 1, r.unit)}`);
  parts.push(`${rowCount(r)} now`);
  return parts.join(" · ");
}
