// Things we have (what a business buys §5.3) and spending by label in the
// money reports (§5.4): the rows the business keeps track of, their
// statuses, adding one by hand, and a period's spending by label. The
// words and the requests; no screens.

import { feeKobo } from "./orders";
import { naira } from "./invoices";
import { lagosDate, periodQuery, type Period } from "./money";

export type ThingStatus = "have" | "sold" | "broken" | "given" | "gone";

/** A row of Things we have (GET /v1/things). */
export interface Thing {
  id: string;
  name: string;
  label_id?: string;
  label?: string;
  supplier_id?: string;
  supplier?: string;
  purchase_order_id?: string;
  order_reference?: string;
  purchase_line_id?: string;
  unit_no?: number;
  cost_kobo?: number;
  received_at?: string;
  serial?: string;
  warranty_until?: string;
  note?: string;
  status: ThingStatus;
  status_at: string;
  sale_entry_id?: string;
  created_by: string;
  created_at: string;
}

export interface ThingGroup {
  label_id: string | null;
  label: string;
  count: number;
  total_kobo: number;
}

export interface ThingsView {
  things: Thing[];
  /** Those still had: how many, and what they cost. */
  count: number;
  total_kobo: number;
  groups?: ThingGroup[];
}

/** The statuses, in the order they are offered. */
export const STATUSES: { value: ThingStatus; label: string; tone: string }[] = [
  { value: "have", label: "Have", tone: "ok" },
  { value: "sold", label: "Sold", tone: "info" },
  { value: "broken", label: "Broken", tone: "broken" },
  { value: "given", label: "Given away", tone: "skipped" },
  { value: "gone", label: "Gone", tone: "cancelled" },
];

export const statusWords = (s: ThingStatus) =>
  STATUSES.find((x) => x.value === s)?.label ?? "Have";
export const statusTone = (s: ThingStatus) =>
  STATUSES.find((x) => x.value === s)?.tone ?? "ok";

export const THINGS_PATH = "/v1/things";

/** The list: with those no longer had or not; always grouped by label. */
export const thingsPath = (all: boolean) =>
  `${THINGS_PATH}?${all ? "all=true&" : ""}group=label`;

/** Where an order line kept track of leads: its rows on Things we have,
 * once something on it came; "" otherwise. */
export function thingsOfLine(l: {
  treatment?: string;
  id?: string;
  received?: number;
}): string {
  if (l.treatment !== "track" || !l.id || !(l.received && l.received > 0))
    return "";
  return `/money/things?line=${encodeURIComponent(l.id)}`;
}

/** The rows of one order line (?line=), or all of them. */
export const rowsOf = (things: Thing[], line: string | null) =>
  line ? things.filter((t) => t.purchase_line_id === line) : things;

/** The rows by label, as the list shows them: the server's groups in its
 * order (the most cost first, Not labelled last), each with its rows.
 * Rows no longer had go under their label too, after those had. */
export function byLabel(
  things: Thing[],
  groups: ThingGroup[] | undefined,
): { key: string; label: string; count: number; total_kobo: number; rows: Thing[] }[] {
  const k = (id: string | null | undefined) => id ?? "";
  const out = (groups ?? []).map((g) => ({
    key: k(g.label_id),
    label: g.label || "Not labelled",
    count: g.count,
    total_kobo: g.total_kobo,
    rows: [] as Thing[],
  }));
  for (const t of things) {
    let g = out.find((x) => x.key === k(t.label_id));
    if (!g) {
      g = { key: k(t.label_id), label: t.label || "Not labelled", count: 0, total_kobo: 0, rows: [] };
      // A label with only rows no longer had: before Not labelled.
      const none = out.findIndex((x) => x.key === "");
      if (g.key !== "" && none >= 0) out.splice(none, 0, g);
      else out.push(g);
    }
    g.rows.push(t);
  }
  for (const g of out)
    g.rows.sort((a, b) => Number(b.status === "have") - Number(a.status === "have"));
  return out.filter((g) => g.rows.length > 0);
}

/** "3 things, ₦250,000". */
export const thingsCount = (n: number) => (n === 1 ? "1 thing" : `${n} things`);

/** The day it came, in Lagos ("2026-11-02"). */
export const cameOn = (t: Pick<Thing, "received_at" | "created_at">) =>
  lagosDate(new Date(t.received_at ?? t.created_at));

/** A row's line under its name: label, from whom, when it came. */
export function thingLine(t: Thing, dateLabel: (d: string) => string): string {
  return [
    t.supplier ? `From ${t.supplier}` : "",
    `came ${dateLabel(cameOn(t))}`,
    t.status !== "have" ? statusWords(t.status) : "",
  ]
    .filter(Boolean)
    .join(" · ")
    .replace(/^came/, "Came");
}

// --- Add something we have ---

export interface ThingForm {
  name: string;
  label: string;
  supplierId: string;
  cost: string;
  /** The day it came (YYYY-MM-DD); "" for today. */
  came: string;
  serial: string;
  warranty: string;
  note: string;
}

export const emptyThing = (): ThingForm => ({
  name: "",
  label: "",
  supplierId: "",
  cost: "",
  came: "",
  serial: "",
  warranty: "",
  note: "",
});

/** What is wrong with the form ("" when nothing). */
export function thingProblem(f: ThingForm, today: string): string {
  if (f.name.trim() === "") return "Say what it is.";
  if (f.name.trim().length > 200) return "A name is up to 200 characters.";
  if (Number.isNaN(feeKobo(f.cost))) return "What it cost is an amount, like 250,000.";
  if (f.came && f.came > today) return "When it came cannot be after today.";
  if (f.serial.trim().length > 100) return "A serial number is up to 100 characters.";
  if (f.note.trim().length > 500) return "A note is up to 500 characters.";
  if (f.label.trim().length > 60) return "A label is up to 60 characters.";
  return "";
}

/** POST /v1/things, the label already found or made (labelId). */
export function thingRequest(f: ThingForm, labelId?: string) {
  const out: Record<string, unknown> = { name: f.name.trim().replace(/\s+/g, " ") };
  if (labelId) out.label_id = labelId;
  if (f.supplierId) out.supplier_id = f.supplierId;
  if (f.cost.trim()) out.cost_kobo = feeKobo(f.cost);
  // Noon in Lagos, so the day stays the day it came.
  if (f.came) out.received_at = `${f.came}T12:00:00+01:00`;
  if (f.serial.trim()) out.serial = f.serial.trim();
  if (f.warranty) out.warranty_until = f.warranty;
  if (f.note.trim()) out.note = f.note.trim();
  return out;
}

// --- changing a row ---

/** PATCH /v1/things/{id} for a status: sold with a price (in naira, typed)
 * is money in, paid in cash or by transfer. */
export function statusRequest(
  status: ThingStatus,
  price = "",
  how: "cash" | "transfer" = "cash",
): Record<string, unknown> {
  const out: Record<string, unknown> = { status };
  if (status === "sold" && price.trim()) {
    out.sale_price_kobo = feeKobo(price);
    out.sale_paid_how = how;
  }
  return out;
}

/** Why a sale's price cannot go as typed ("" when it can). */
export function priceProblem(price: string, canEnterMoney: boolean): string {
  if (price.trim() === "") return "";
  const k = feeKobo(price);
  if (Number.isNaN(k) || k <= 0) return "What it sold for is an amount more than ₦0, like 150,000.";
  if (!canEnterMoney) return "Only someone who manages money can record what it sold for. Mark it sold without a price, or ask them.";
  return "";
}

/** What the row's change does, said back. */
export function statusSaid(name: string, status: ThingStatus, priceKobo?: number) {
  if (status === "have") return `${name} is had again`;
  if (status === "sold" && priceKobo) return `${name} sold for ${naira(priceKobo)}: money in`;
  return `${name}: ${statusWords(status).toLowerCase()}`;
}

/** PATCH /v1/things/{id} for its details: only what changed (an empty
 * warranty clears it). */
export function detailsRequest(
  t: Pick<Thing, "serial" | "warranty_until" | "note">,
  f: { serial: string; warranty: string; note: string },
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (f.serial.trim() !== (t.serial ?? "")) out.serial = f.serial.trim();
  if (f.warranty !== (t.warranty_until ?? "").slice(0, 10)) out.warranty_until = f.warranty;
  if (f.note.trim() !== (t.note ?? "")) out.note = f.note.trim();
  return out;
}

// --- spending by label (GET /v1/money/spending) ---

export interface SpendingLabel {
  label_id: string | null;
  name: string;
  kobo: number;
  bought_to_sell_kobo: number;
  bought_to_keep_kobo: number;
  running_costs_kobo: number;
  entries: number;
}

export interface Spending {
  from: string;
  to: string;
  total_kobo: number;
  bought_to_sell_kobo: number;
  bought_to_keep_kobo: number;
  running_costs_kobo: number;
  labels: SpendingLabel[];
  profit: { in_kobo: number; fees_kobo: number; costs_kobo: number; profit_kobo: number };
}

export const spendingPath = (p: Period) =>
  `/v1/money/spending?${periodQuery(p)}`;

/** One label's line: what of it was bought to sell, to keep, running. */
export function spendingLine(l: SpendingLabel): string {
  const parts: string[] = [];
  if (l.running_costs_kobo) parts.push(`${naira(l.running_costs_kobo)} running costs`);
  if (l.bought_to_sell_kobo) parts.push(`${naira(l.bought_to_sell_kobo)} bought to sell or make with`);
  if (l.bought_to_keep_kobo) parts.push(`${naira(l.bought_to_keep_kobo)} bought to keep`);
  return parts.join(" · ");
}

/** The labels' name, Not labelled for none. */
export const spendingName = (l: SpendingLabel) => (l.label_id ? l.name : "Not labelled");

/** Profit said plainly: "₦120,000 profit" or "₦30,000 loss". */
export const profitWords = (kobo: number) =>
  kobo < 0 ? `${naira(-kobo)} loss` : `${naira(kobo)} profit`;
