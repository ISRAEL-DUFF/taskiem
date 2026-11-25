// Money › Transactions (docs/spec/transactions-page.md): every movement of
// the business's money log, newest first, filtered, searched and summed
// up (GET /v1/money/entries?movements=true), and each on its own page
// (GET /v1/money/entries/{id}). Pure helpers, tested on their own.

import { channelWords, dateLabel, naira, otherMoney } from "./invoices";
import { lagosDate, periodFor, periodProblem, periodWords, type Period, type PeriodChoice } from "./money";

/** One row of the list: a movement of money, its latest status. */
export interface Movement {
  /** The entry that counts (its first settled money in); matching takes it. */
  id: string;
  movement: string;
  kind: string;
  direction: "in" | "out";
  amount_kobo?: number;
  fee_kobo?: number;
  status: "settled" | "pending" | "failed" | "reversed";
  channel?: string;
  reference?: string;
  when: string;
  description?: string;
  vendor?: string;
  category?: string;
  left_kobo?: number;
  towards?: string;
  to_match: boolean;
  currency?: string;
  statuses?: number;
}

export interface MovementTotals {
  count: number;
  in_settled_kobo: number;
  in_pending_kobo: number;
  out_settled_kobo: number;
  out_pending_kobo: number;
  fees_kobo: number;
  other_currencies: number;
}

export interface MovementPage {
  entries: Movement[];
  next?: string;
  totals?: MovementTotals;
  channels?: string[];
}

export interface MovementDetail extends Movement {
  recorded_at: string;
  note?: string;
  source: string;
  history: { status: string; kind: string; amount_kobo?: number; at: string; source: string }[];
  counts: { what: "invoice" | "order" | "sale"; id: string; number: string; amount_kobo: number }[];
}

export interface MovementAnswer {
  entry: MovementDetail;
  payer?: string;
  payer_account?: string;
  revealed: boolean;
}

// --- the filters, kept in the page's address ---

/** The periods offered: those of Money, and everything. */
export type TxPeriod = PeriodChoice | "all";

export const TX_PERIODS: { value: TxPeriod; label: string }[] = [
  { value: "today", label: "Today" },
  { value: "week", label: "This week" },
  { value: "month", label: "This month" },
  { value: "last_month", label: "Last month" },
  { value: "all", label: "Everything" },
  { value: "custom", label: "Choose dates" },
];

export const DIRECTIONS = [
  { value: "", label: "In and out" },
  { value: "in", label: "Money in" },
  { value: "out", label: "Money out" },
] as const;

export const MATCHED = [
  { value: "", label: "Matched or not" },
  { value: "yes", label: "Matched" },
  { value: "no", label: "Not matched" },
] as const;

export interface TxFilter {
  period: TxPeriod;
  from: string;
  to: string;
  direction: "" | "in" | "out";
  channel: string;
  matched: "" | "yes" | "no";
  q: string;
}

/** The filters in the page's address; This month unless it says. */
export function txFilter(p: URLSearchParams): TxFilter {
  const period = TX_PERIODS.some((c) => c.value === p.get("period")) ? (p.get("period") as TxPeriod) : "month";
  const dir = p.get("direction");
  const m = p.get("matched");
  return {
    period,
    from: p.get("from") ?? "",
    to: p.get("to") ?? "",
    direction: dir === "in" || dir === "out" ? dir : "",
    channel: p.get("channel") ?? "",
    matched: m === "yes" || m === "no" ? m : "",
    q: p.get("q") ?? "",
  };
}

/** The filters as the page's address: what is not the default is said. */
export function txParams(f: TxFilter): Record<string, string> {
  const out: Record<string, string> = {};
  if (f.period !== "month") out.period = f.period;
  if (f.period === "custom") {
    out.from = f.from;
    out.to = f.to;
  }
  if (f.direction) out.direction = f.direction;
  if (f.channel) out.channel = f.channel;
  if (f.matched) out.matched = f.matched;
  if (f.q.trim()) out.q = f.q;
  return out;
}

/** The period the filter means, or null for everything. */
export function txPeriod(f: TxFilter, now = new Date()): Period | null {
  if (f.period === "all") return null;
  return periodFor(f.period, now, { from: f.from, to: f.to });
}

/** Why the chosen dates will be refused, or null. */
export function txProblem(f: TxFilter, now = new Date()): string | null {
  const p = txPeriod(f, now);
  return p && f.period === "custom" ? periodProblem(p) : null;
}

export const TX_PATH = "/v1/money/entries";

/** The list's address: a page of movements, from cursor when paging. */
export function txPath(f: TxFilter, cursor?: string, now = new Date()): string {
  const q = new URLSearchParams({ movements: "true" });
  const p = txPeriod(f, now);
  if (p) {
    q.set("from", p.from);
    q.set("to", p.to);
  }
  if (f.direction) q.set("direction", f.direction);
  if (f.channel) q.set("channel", f.channel);
  if (f.matched) q.set("matched", f.matched);
  if (f.q.trim()) q.set("q", f.q.trim());
  if (cursor) q.set("cursor", cursor);
  return `${TX_PATH}?${q}`;
}

/** One movement's address; reveal shows who paid in full. */
export const txEntryPath = (id: string, reveal = false) => `${TX_PATH}/${encodeURIComponent(id)}${reveal ? "?reveal=true" : ""}`;

/** The period in words: "This month", "1 Oct 2026 to 31 Oct 2026", "Everything". */
export function txPeriodWords(f: TxFilter, now = new Date()): string {
  const p = txPeriod(f, now);
  if (!p) return "Everything";
  if (f.period === "custom") return periodWords(p);
  return TX_PERIODS.find((c) => c.value === f.period)?.label ?? periodWords(p);
}

/** Transactions for the period Money shows: the same choice, or its dates. */
export function transactionsLink(choice: PeriodChoice, p: Period): string {
  let q = "";
  if (choice === "custom" || !TX_PERIODS.some((c) => c.value === choice)) q = new URLSearchParams({ period: "custom", from: p.from, to: p.to }).toString();
  else if (choice !== "month") q = new URLSearchParams({ period: choice }).toString();
  return q ? `/money/transactions?${q}` : "/money/transactions";
}

/** Whether any filter but the period is set. */
export const narrowed = (f: TxFilter) => !!(f.direction || f.channel || f.matched || f.q.trim());

// --- words ---

const KIND_WORDS: Record<string, string> = {
  payment: "Payment",
  account_credit: "Transfer to an account",
  payout: "Paid out",
  refund: "Refund",
  pos_sale: "Card sale",
  fee: "Fee",
  statement: "From a statement",
  cash: "Cash",
  expense: "Expense",
  correction: "Correction",
};

/** What kind of money it is, in words. */
export const kindWords = (kind: string) => KIND_WORDS[kind] ?? kind.replaceAll("_", " ");

/** Where it moved, in words; "Taskiem wallet" for Taskiem's own. */
export function whereWords(channel: string | undefined): string {
  if (!channel) return "";
  if (channel === "iswallet/taskiem") return "Taskiem wallet";
  return channelWords(channel);
}

/** A row's title: what was typed or the vendor, else the kind. */
export function txTitle(m: Pick<Movement, "description" | "vendor" | "kind">): string {
  return m.description || m.vendor || kindWords(m.kind);
}

/** Its amount with which way it went: "+₦4,500", "−₦1,200", "No amount". */
export function txAmount(m: Pick<Movement, "amount_kobo" | "direction" | "currency">): string {
  if (m.amount_kobo === undefined || m.amount_kobo === null) return "No amount";
  const amount = m.currency ? otherMoney(m.amount_kobo, m.currency) : naira(m.amount_kobo);
  return `${m.direction === "in" ? "+" : "−"}${amount}`;
}

/** A row's line: "Paystack (main) · P-1 · invoice INV-0104". */
export function txSub(m: Movement): string {
  const kind = m.description || m.vendor ? kindWords(m.kind) : "";
  const parts = [kind, whereWords(m.channel), m.reference && m.reference !== m.description ? m.reference : "", m.towards ?? ""].filter(Boolean);
  return [...new Set(parts)].join(" · ");
}

export type TxTone = "ok" | "warn" | "danger" | "info" | "muted";

/** A movement's status in a word or two, and its tone. */
export function txStatus(m: Pick<Movement, "status" | "to_match" | "towards" | "direction" | "currency">): { label: string; tone: TxTone } {
  switch (m.status) {
    case "failed":
      return { label: "Failed", tone: "danger" };
    case "reversed":
      return { label: "Reversed", tone: "muted" };
    case "pending":
      return { label: "Pending", tone: "info" };
  }
  if (m.to_match) return { label: "To match", tone: "warn" };
  if (m.towards) return { label: "Matched", tone: "ok" };
  if (m.direction === "in" && !m.currency) return { label: "Not matched", tone: "muted" };
  return { label: m.direction === "in" ? "Received" : "Paid", tone: "ok" };
}

/** The list's hero: money out when only money out is shown, else money in,
 * with the other side, the fees and the count under it. */
export function totalsHero(t: MovementTotals, f: TxFilter): { label: string; amount: string; sub: string } {
  const out = f.direction === "out";
  const count = t.count === 1 ? "1 transaction" : `${t.count} transactions`;
  const other = out ? "" : f.direction === "in" ? "" : `${naira(t.out_settled_kobo)} out`;
  return {
    label: out ? "Money out" : "Money in",
    amount: naira(out ? t.out_settled_kobo : t.in_settled_kobo),
    sub: [other, t.fees_kobo ? `${naira(t.fees_kobo)} in fees` : "", count].filter(Boolean).join(" · "),
  };
}

/** What is still pending, or "". */
export function pendingLine(t: MovementTotals): string {
  const p = [t.in_pending_kobo ? `${naira(t.in_pending_kobo)} in` : "", t.out_pending_kobo ? `${naira(t.out_pending_kobo)} out` : ""].filter(Boolean);
  return p.length ? `${p.join(" and ")} still pending` : "";
}

/** Money in another currency, counted apart, or "". */
export function otherLine(t: MovementTotals): string {
  if (!t.other_currencies) return "";
  return t.other_currencies === 1 ? "1 in another currency is not in these sums." : `${t.other_currencies} in other currencies are not in these sums.`;
}

/** The day a row falls on, for the list's day headings: "Today", "25 Nov 2026". */
export function dayHeading(iso: string, now = new Date()): string {
  const d = lagosDate(new Date(iso));
  const today = lagosDate(now);
  if (d === today) return "Today";
  const y = new Date(`${today}T12:00:00Z`);
  y.setUTCDate(y.getUTCDate() - 1);
  if (d === y.toISOString().slice(0, 10)) return "Yesterday";
  return dateLabel(d);
}

/** The rows grouped by their Lagos day, newest first as they come. */
export function byDay<T extends { when: string }>(list: T[], now = new Date()): { day: string; rows: T[] }[] {
  const out: { day: string; rows: T[] }[] = [];
  for (const m of list) {
    const day = dayHeading(m.when, now);
    const last = out[out.length - 1];
    if (last && last.day === day) last.rows.push(m);
    else out.push({ day, rows: [m] });
  }
  return out;
}

const SOURCE_WORDS: Record<string, string> = {
  event: "Told by the payments account",
  lookup: "Checked with the payments account",
  step: "From a workflow",
  upload: "From an uploaded statement",
  entered: "Entered by hand",
};

/** How it came into the log, in words. */
export const sourceWords = (s: string) => SOURCE_WORDS[s] ?? s;

const STATUS_WORDS: Record<string, string> = { settled: "Settled", pending: "Pending", failed: "Failed", reversed: "Reversed" };

/** A status of the history, in a word. */
export const statusWord = (s: string) => STATUS_WORDS[s] ?? s;

/** The hero's figure and lines for one movement. */
export function txHero(m: MovementDetail): { label: string; amount: string; tag: string; sub: string } {
  const label = m.direction === "in" ? "Money in" : "Money out";
  const sub = [kindWords(m.kind), whereWords(m.channel)].filter(Boolean).join(" · ");
  return { label, amount: txAmount(m), tag: txStatus(m).label, sub };
}

/** What it counts towards, as a row's address on the web. */
export function countPath(c: MovementDetail["counts"][number]): string | null {
  switch (c.what) {
    case "invoice":
      return `/invoices/${encodeURIComponent(c.number)}`;
    case "order":
      return `/storefront/orders/${encodeURIComponent(c.number)}`;
    default:
      return null;
  }
}

/** A count's title: "Invoice INV-0104", "Order AK-7QX4KD", "Sale S-7QX4KD". */
export const countTitle = (c: MovementDetail["counts"][number]) => `${c.what === "invoice" ? "Invoice" : c.what === "order" ? "Order" : "Sale"} ${c.number}`;
