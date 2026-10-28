// The Money area (decision 0045, T6; docs/money-log.md "Money", docs/leaks.md;
// /v1/money/leaks, /assign, /close, /v1/money/fee-rates,
// /v1/money/statement-format, /v1/money/statements, /v1/money/entries;
// and T7, docs/money-reports.md: /v1/money/summary, /v1/money/exports):
// the API's shapes, leaks in words, the statement upload's column matching,
// cash and expenses as typed, the fees agreed with each connection, and the
// overview's period, figures and exports.

import { dateLabel, koboInput, naira, plainMessage, toKobo } from "./invoices";
import type { MoneyBucket } from "./moneyChart";

export type LeakKind = "unmatched" | "overdue" | "duplicate" | "overpaid" | "void_paid" | "fee" | "rider_owes";
export type LeakStatus = "open" | "recovered" | "written_off" | "not_a_leak" | "resolved";
export type CloseStatus = "recovered" | "written_off" | "not_a_leak";

/** One leak (MoneyLeak). */
export interface MoneyLeak {
  id: string;
  kind: LeakKind | string;
  entry_id?: string;
  invoice_id?: string;
  amount_kobo: number;
  detail: string;
  status: LeakStatus | string;
  assigned_to?: string;
  recovered_kobo?: number;
  note?: string;
  opened_at: string;
  closed_at?: string;
  closed_by?: string;
}

export interface LeakTotals {
  open: number;
  open_kobo: number;
  recovered_kobo: number;
}

export interface FeeRate {
  channel: string;
  percent_bp: number;
  flat_kobo: number;
  cap_kobo?: number | null;
}

/** A file's column for each field of the common format (StatementFormat). */
export interface StatementColumns {
  date: string;
  description: string;
  amount?: string;
  in?: string;
  out?: string;
  reference?: string;
  balance?: string;
}

/** The common format's own columns (engine/moneylog CommonColumns). */
export const COMMON_COLUMNS: StatementColumns = { date: "date", description: "description", amount: "amount", reference: "reference", balance: "balance" };

/** Whether the columns a file lacks are only the optional ones of the columns used (its reference or balance). */
export function onlyOptionalMissing(missing: string[], used: StatementColumns): boolean {
  const optional = [used.reference, used.balance].filter((c): c is string => !!c).map((c) => c.trim().toLowerCase());
  return missing.length > 0 && missing.every((m) => optional.includes(m.trim().toLowerCase()));
}

/** What an upload added (200). */
export interface StatementAdded {
  channel: string;
  added: number;
  skipped: number;
  columns: StatementColumns;
}

/** A line of the file Taskiem could not read. */
export interface StatementProblem {
  row: number;
  why: string;
}

/** A 422 statement_unreadable answer, as the page uses it. */
export interface StatementRefusal {
  message: string;
  missing: string[];
  problems: StatementProblem[];
}

export const LEAKS_PATH = "/v1/money/leaks";
export const FEE_RATES_PATH = "/v1/money/fee-rates";
export const STATEMENTS_PATH = "/v1/money/statements";
export const ENTRIES_PATH = "/v1/money/entries";
export const leakPath = (id: string, action: "assign" | "close") => `${LEAKS_PATH}/${encodeURIComponent(id)}/${action}`;
export const statementFormatPath = (channel: string) => `/v1/money/statement-format?${new URLSearchParams({ channel })}`;

/** The most a statement upload may be (engine/moneylog MaxStatementBytes). */
export const MAX_STATEMENT_BYTES = 5 << 20;

/** What a person may see and do in Money: the same permissions as invoices. */
export function moneyAccess(can: (perm: string) => boolean): { see: boolean; manage: boolean } {
  return { see: can("invoice.read"), manage: can("invoice.manage") };
}

// --- leaks ---

const LEAK_KINDS: Record<LeakKind, string> = {
  unmatched: "Money with no invoice",
  overdue: "Overdue invoice",
  duplicate: "Recorded twice",
  overpaid: "Invoice paid too much",
  void_paid: "Paid for a void invoice",
  fee: "Fee above the agreed rate",
  rider_owes: "Cash a rider owes",
};

/** A leak's kind in words. */
export const leakKindWords = (kind: string) => LEAK_KINDS[kind as LeakKind] ?? kind.replaceAll("_", " ");

const LEAK_STATUS: Record<LeakStatus, { label: string; tone: "ok" | "warn" | "muted" | "info" | "danger" }> = {
  open: { label: "Open", tone: "warn" },
  recovered: { label: "Recovered", tone: "ok" },
  written_off: { label: "Written off", tone: "muted" },
  not_a_leak: { label: "Not a leak", tone: "muted" },
  resolved: { label: "Resolved by itself", tone: "info" },
};

/** A leak's status as a pill shows it. */
export function leakStatus(s: string) {
  return LEAK_STATUS[s as LeakStatus] ?? { label: s.replaceAll("_", " "), tone: "muted" as const };
}

/** The leaks filter, as the page's ?status= holds it. */
export const LEAK_FILTERS = [
  { value: "open", label: "Open" },
  { value: "closed", label: "Closed" },
  { value: "all", label: "All" },
] as const;

export function leakFilter(v: string | null): "open" | "closed" | "all" {
  return v === "closed" || v === "all" ? v : "open";
}

export const leaksPath = (status: string) => `${LEAKS_PATH}?${new URLSearchParams({ status: leakFilter(status) })}`;

/** The leak's detail as a sentence: "₦25,000 came in …" stays, "a fee of …" gets its capital. */
export const leakSentence = (detail: string) => plainMessage(detail);

/** Who an actor is, in words: the signed-in person is "you", a member their name. */
export function whoWords(actor: string | undefined, names: Map<string, string>, me?: string): string {
  if (!actor) return "";
  if (me && actor === me) return "you";
  if (actor.startsWith("system:")) return "Taskiem";
  if (actor.startsWith("key:")) return "an API key";
  if (actor.startsWith("agent:")) return `field agent ${actor.slice(6)}`;
  return names.get(actor) ?? "a member";
}

/** How a closed leak was closed, in words: "Recovered ₦20,000, closed by you". */
export function closedLine(l: Pick<MoneyLeak, "status" | "recovered_kobo" | "closed_by">, names: Map<string, string>, me?: string): string {
  if (l.status === "open") return "";
  if (l.status === "resolved") return "Closed by itself: the cause went away (the money was matched or the invoice paid).";
  const by = whoWords(l.closed_by, names, me);
  const how = l.status === "recovered" ? `Recovered ${naira(l.recovered_kobo ?? 0)}` : leakStatus(l.status).label;
  return by ? `${how}, closed by ${by}` : how;
}

/** Close a leak, as the form holds it. */
export interface CloseForm {
  status: CloseStatus | "";
  recovered: string;
  note: string;
}

export const emptyClose = (): CloseForm => ({ status: "", recovered: "", note: "" });

export const CLOSE_CHOICES: { value: CloseStatus; label: string; help: string }[] = [
  { value: "recovered", label: "Recovered", help: "The money came back. Say how much." },
  { value: "written_off", label: "Written off", help: "It is lost, and you have stopped chasing it." },
  { value: "not_a_leak", label: "Not a leak", help: "Nothing was lost: Taskiem was wrong about this one." },
];

export const NOTE_LIMIT = 500;

/** Why closing would be refused, or null. */
export function closeProblem(f: CloseForm): string | null {
  if (!f.status) return "Choose how it was closed.";
  if (f.status === "recovered") {
    const k = toKobo(f.recovered);
    if (k === undefined) return "Say how much was recovered.";
    if (!Number.isFinite(k) || k <= 0) return "The amount recovered is a number of naira, such as 25000 or 25000.50.";
  }
  if (f.note.trim().length > NOTE_LIMIT) return `The note is at most ${NOTE_LIMIT} characters.`;
  return null;
}

/** The POST /close body. Call it once closeProblem is null. */
export function closeRequest(f: CloseForm): { status: CloseStatus; recovered_kobo?: number; note?: string } {
  const body: { status: CloseStatus; recovered_kobo?: number; note?: string } = { status: f.status as CloseStatus };
  if (f.status === "recovered") body.recovered_kobo = toKobo(f.recovered) ?? 0;
  if (f.note.trim()) body.note = f.note.trim();
  return body;
}

// --- statements ---

/** Splits one CSV record from the start of text; returns its fields. Quotes may hold commas and line breaks. */
function firstRecord(text: string): string[] {
  const fields: string[] = [];
  let field = "";
  let quoted = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (quoted) {
      if (c === '"') {
        if (text[i + 1] === '"') {
          field += '"';
          i++;
        } else quoted = false;
      } else field += c;
      continue;
    }
    if (c === '"') quoted = true;
    else if (c === ",") {
      fields.push(field);
      field = "";
    } else if (c === "\n" || c === "\r") {
      if (fields.length > 0 || field.trim() !== "") break;
      // blank lines before the header are skipped
    } else field += c;
  }
  fields.push(field);
  return fields;
}

/** The column names in a CSV file's first line, trimmed, without a byte-order mark, blanks left out. */
export function csvHeader(text: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of firstRecord(text.replace(/^\uFEFF/, ""))) {
    const h = raw.trim();
    if (h && !seen.has(h)) {
      seen.add(h);
      out.push(h);
    }
  }
  return out;
}

const norm = (s: string) => s.trim().toLowerCase().split(/\s+/).join(" ");

/** Matching a file's columns to the common format, as the form holds it. */
export interface ColumnsForm {
  date: string;
  description: string;
  /** One signed amount column, or money in and money out in two. */
  split: boolean;
  amount: string;
  in: string;
  out: string;
  reference: string;
  balance: string;
}

const GUESS: Record<"date" | "description" | "amount" | "in" | "out" | "reference" | "balance", RegExp[]> = {
  date: [/^(trans(action)?\.? ?)?date$/, /^(posting|post|value|txn) date$/, /date/],
  description: [/^description$/, /narration|narrative|remarks?|details|particulars|description/],
  amount: [/^amount$/, /^(transaction |txn )?amount( \(ngn\)| ngn)?$/],
  in: [/^(money in|credit|credits|cr|deposit|deposits|lodgement|lodgements|paid in|inflow)( \(ngn\)| amount)?$/, /credit|money in|deposit|inflow/],
  out: [/^(money out|debit|debits|dr|withdrawal|withdrawals|paid out|outflow)( \(ngn\)| amount)?$/, /debit|money out|withdrawal|outflow/],
  reference: [/^(reference|ref|ref\.? ?no\.?|reference number|transaction reference|session id)$/, /ref/],
  balance: [/^(balance|closing balance|running balance)$/, /balance/],
};

function guess(headers: string[], field: keyof typeof GUESS, taken: Set<string>): string {
  for (const re of GUESS[field]) {
    const h = headers.find((x) => !taken.has(x) && re.test(norm(x)));
    if (h) return h;
  }
  return "";
}

/** The header among headers that names the same column as name (the server compares them the same way), or "". */
function present(headers: string[], name: string | undefined): string {
  if (!name) return "";
  return headers.find((h) => norm(h) === norm(name)) ?? "";
}

/** The column matching to offer for a file: the channel's saved matching where the file has those columns, else a guess from the names. */
export function columnsForm(headers: string[], saved?: StatementColumns | null): ColumnsForm {
  const taken = new Set<string>();
  const pick = (field: keyof typeof GUESS) => {
    const h = present(headers, saved?.[field]) || guess(headers, field, taken);
    if (h) taken.add(h);
    return h;
  };
  const date = pick("date");
  const description = pick("description");
  const reference = pick("reference");
  const balance = pick("balance");
  // A saved split stays split; otherwise one amount column wins over two.
  const savedSplit = !!saved && !saved.amount && !!(saved.in || saved.out);
  const amount = savedSplit ? "" : pick("amount");
  let inCol = "";
  let outCol = "";
  if (!amount) {
    inCol = pick("in");
    outCol = pick("out");
  }
  const split = !amount && !!(inCol || outCol);
  return { date, description, split, amount, in: inCol, out: outCol, reference, balance };
}

/** Why the matching will not do, or null. */
export function columnsProblem(f: ColumnsForm): string | null {
  if (!f.date) return "Choose the column with each line's date.";
  if (!f.description) return "Choose the column that says what each line was.";
  if (f.split) {
    if (!f.in) return "Choose the column with money in.";
    if (!f.out) return "Choose the column with money out.";
  } else if (!f.amount) return "Choose the column with the amount.";
  const used = [f.date, f.description, ...(f.split ? [f.in, f.out] : [f.amount]), f.reference, f.balance].filter(Boolean);
  if (new Set(used).size !== used.length) return "Each column can be used for one thing only.";
  return null;
}

/** The columns sent with an upload. Call it once columnsProblem is null. */
export function columnsRequest(f: ColumnsForm): StatementColumns {
  const c: StatementColumns = { date: f.date, description: f.description };
  if (f.split) {
    c.in = f.in;
    c.out = f.out;
  } else c.amount = f.amount;
  if (f.reference) c.reference = f.reference;
  if (f.balance) c.balance = f.balance;
  return c;
}

/** The POST /v1/money/statements body. */
export function statementRequest(channel: string, csv: string, columns?: StatementColumns): { channel: string; csv: string; columns?: StatementColumns } {
  return columns ? { channel: channel.trim(), csv, columns } : { channel: channel.trim(), csv };
}

/** An account name the server takes: "gtbank/current", "cash", "petty-cash". */
export const CHANNEL_RE = /^[a-z0-9][a-z0-9_.-]{0,40}(\/[a-z0-9][a-z0-9_.-]{0,40})?$/;

/** Why the account's name will not do, or null (empty is allowed when optional). */
export function channelProblem(channel: string, optional = false): string | null {
  const c = channel.trim();
  if (!c) return optional ? null : "Name the account, such as gtbank/current.";
  if (!CHANNEL_RE.test(c)) return "The account is written in small letters, digits, - or _, with one / between the bank and the account: gtbank/current.";
  return null;
}

/** A 422 answer as the page shows it, or null when the error is something else. */
export function statementRefusal(status: number, body: Record<string, unknown> | undefined, message: string): StatementRefusal | null {
  if (status !== 422 || !body || body.code !== "statement_unreadable") return null;
  const missing = Array.isArray(body.missing) ? body.missing.filter((m): m is string => typeof m === "string") : [];
  const problems = Array.isArray(body.problems)
    ? body.problems.filter((p): p is StatementProblem => !!p && typeof p === "object" && typeof (p as StatementProblem).row === "number").map((p) => ({ row: p.row, why: String(p.why ?? "") }))
    : [];
  return { message: plainMessage(String(body.error ?? message)), missing, problems };
}

const lines = (n: number) => `${n} ${n === 1 ? "line" : "lines"}`;

/** What an upload did, in words. */
export function addedLine(r: Pick<StatementAdded, "added" | "skipped">): string {
  return `Added ${lines(r.added)}, skipped ${r.skipped} already in the log.`;
}

// --- cash and expenses ---

export type EntryKind = "expense" | "cash_in" | "cash_out";

export const ENTRY_KINDS: { value: EntryKind; label: string; help: string }[] = [
  { value: "expense", label: "Expense", help: "Money the business spent: diesel, rent, supplies." },
  { value: "cash_in", label: "Cash in", help: "Cash the business took." },
  { value: "cash_out", label: "Cash out", help: "Cash taken out of the till or the safe." },
];

export interface EntryForm {
  kind: EntryKind;
  amount: string;
  date: string;
  description: string;
  vendor: string;
  category: string;
  channel: string;
}

/** Today in the browser's time zone as YYYY-MM-DD. */
export function today(now = new Date()): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${now.getFullYear()}-${p(now.getMonth() + 1)}-${p(now.getDate())}`;
}

export const emptyEntry = (now = new Date()): EntryForm => ({ kind: "expense", amount: "", date: today(now), description: "", vendor: "", category: "", channel: "" });

export const ENTRY_LIMITS = { description: 300, vendor: 120, category: 60 } as const;

/** Why the entry will be refused, or null. */
export function entryProblem(f: EntryForm, now = new Date()): string | null {
  const k = toKobo(f.amount);
  if (k === undefined) return "Give the amount.";
  if (!Number.isFinite(k) || k <= 0) return "The amount is a number of naira, such as 12000 or 12000.50.";
  if (!f.description.trim()) return "Say what it was for.";
  if (f.description.trim().length > ENTRY_LIMITS.description) return `The description is at most ${ENTRY_LIMITS.description} characters.`;
  if (f.date && !/^\d{4}-\d{2}-\d{2}$/.test(f.date)) return "The date does not look right.";
  if (f.date && f.date > today(new Date(now.getTime() + 24 * 3600 * 1000))) return "The date is in the future.";
  if (f.kind === "expense") {
    if (f.vendor.trim().length > ENTRY_LIMITS.vendor) return `The vendor is at most ${ENTRY_LIMITS.vendor} characters.`;
    if (f.category.trim().length > ENTRY_LIMITS.category) return `The category is at most ${ENTRY_LIMITS.category} characters.`;
  }
  return channelProblem(f.channel, true);
}

export interface EntryRequest {
  kind: "cash" | "expense";
  direction?: "in" | "out";
  amount_kobo: number;
  occurred_at?: string;
  description: string;
  vendor?: string;
  category?: string;
  channel?: string;
}

/** The POST /v1/money/entries body. Call it once entryProblem is null. */
export function entryRequest(f: EntryForm): EntryRequest {
  const r: EntryRequest = f.kind === "expense" ? { kind: "expense", amount_kobo: toKobo(f.amount) ?? 0, description: f.description.trim() } : { kind: "cash", direction: f.kind === "cash_in" ? "in" : "out", amount_kobo: toKobo(f.amount) ?? 0, description: f.description.trim() };
  if (f.date) r.occurred_at = f.date;
  if (f.kind === "expense") {
    if (f.vendor.trim()) r.vendor = f.vendor.trim();
    if (f.category.trim()) r.category = f.category.trim();
  }
  if (f.channel.trim()) r.channel = f.channel.trim();
  return r;
}

/** What the form says once the entry is in the log (created false: the same form was sent before). */
export function enteredLine(f: EntryForm, created: boolean): string {
  const what = ENTRY_KINDS.find((k) => k.value === f.kind)?.label ?? "Entry";
  const amount = naira(toKobo(f.amount) ?? 0);
  return created ? `${what} of ${amount} added to the money log.` : `${what} of ${amount} was already in the money log; nothing was added twice.`;
}

// --- fees ---

/** A percent as typed ("1.5", "1.5%") in basis points (150); undefined when empty, NaN when not a percent from 0 to 100 with up to two decimals. */
export function percentToBp(text: string): number | undefined {
  const t = text.replace(/[%\s]/g, "");
  if (!t) return undefined;
  if (!/^\d+(\.\d{1,2})?$/.test(t)) return NaN;
  const bp = Math.round(Number(t) * 100);
  return bp <= 10000 ? bp : NaN;
}

/** Basis points as a percent for typing and reading: 150 is "1.5", 100 is "1", 5 is "0.05". */
export function bpToPercent(bp: number): string {
  const whole = Math.floor(bp / 100);
  const frac = bp % 100;
  if (!frac) return String(whole);
  return `${whole}.${String(frac).padStart(2, "0").replace(/0$/, "")}`;
}

/** A rate in words: "1.5% + ₦100, at most ₦2,000". */
export function rateWords(r: FeeRate): string {
  const parts = [r.percent_bp || !r.flat_kobo ? `${bpToPercent(r.percent_bp)}%` : "", r.flat_kobo ? naira(r.flat_kobo) : ""].filter(Boolean);
  const cap = r.cap_kobo !== undefined && r.cap_kobo !== null ? `, at most ${naira(r.cap_kobo)}` : "";
  return parts.join(" + ") + cap;
}

export interface FeeForm {
  channel: string;
  percent: string;
  flat: string;
  cap: string;
}

export const emptyFee = (): FeeForm => ({ channel: "", percent: "", flat: "", cap: "" });

export function feeForm(r: FeeRate): FeeForm {
  return { channel: r.channel, percent: bpToPercent(r.percent_bp), flat: r.flat_kobo ? koboInput(r.flat_kobo) : "", cap: r.cap_kobo !== undefined && r.cap_kobo !== null ? koboInput(r.cap_kobo) : "" };
}

/** Why the rate will be refused, or null. */
export function feeProblem(f: FeeForm): string | null {
  const c = f.channel.trim();
  if (!c) return "Name the connection, such as paystack/main.";
  if (c.length > 200) return "The connection's name is at most 200 characters.";
  const bp = percentToBp(f.percent);
  if (bp !== undefined && Number.isNaN(bp)) return "The percent is a number from 0 to 100, such as 1.5.";
  const flat = toKobo(f.flat);
  if (flat !== undefined && (!Number.isFinite(flat) || flat < 0)) return "The flat fee is a number of naira, such as 100.";
  const cap = toKobo(f.cap);
  if (cap !== undefined && (!Number.isFinite(cap) || cap < 0)) return "The cap is a number of naira, such as 2000.";
  if (bp === undefined && flat === undefined) return "Give the percent, the flat fee, or both.";
  return null;
}

/** The PUT body. Call it once feeProblem is null. */
export function feeRequest(f: FeeForm): FeeRate {
  const r: FeeRate = { channel: f.channel.trim(), percent_bp: percentToBp(f.percent) ?? 0, flat_kobo: toKobo(f.flat) ?? 0 };
  const cap = toKobo(f.cap);
  if (cap !== undefined) r.cap_kobo = cap;
  return r;
}

/** Account names to suggest: those already used, each once, in order. */
export function knownChannels(...lists: (readonly (string | undefined | null)[] | undefined)[]): string[] {
  const out = new Set<string>();
  for (const l of lists) for (const c of l ?? []) if (c && c !== "cash") out.add(c);
  return [...out].sort();
}

// --- overview and exports ---

/** One channel's money in a period (MoneySummary.channels). */
export interface ChannelSummary {
  channel: string;
  in_settled_kobo: number;
  in_pending_kobo: number;
  out_settled_kobo: number;
  out_pending_kobo: number;
  fees_kobo: number;
  fee_rate_bp?: number | null;
  movements: number;
}

/** A period's money (MoneySummary). */
export interface MoneySummary {
  from: string;
  to: string;
  in_settled_kobo: number;
  in_pending_kobo: number;
  out_settled_kobo: number;
  out_pending_kobo: number;
  fees_kobo: number;
  fee_rate_bp?: number | null;
  channels: ChannelSummary[] | null;
  invoices_paid: number;
  invoices_paid_kobo: number;
  leaks_opened: number;
  leaks_opened_kobo: number;
  leaks_open: number;
  leaks_open_kobo: number;
  recovered_kobo: number;
  /** Money in collected on others' behalf (pass-through lines), by body, and what is left as the business's own. */
  collected_for_others_kobo?: number;
  collected_for?: { body: string; kobo: number }[] | null;
  own_in_settled_kobo?: number;
  /** The period in stretches, for charts: hours for one day, days for up to 92, none beyond. */
  unit?: "hour" | "day" | "";
  buckets?: MoneyBucket[];
}

/** The money collected for others, in words: "₦36,000 for WAEC, ₦5,000 for NECO"; empty for none. */
export function collectedForLine(s: Pick<MoneySummary, "collected_for">): string {
  return (s.collected_for ?? [])
    .filter((b) => b.kobo > 0)
    .map((b) => `${naira(b.kobo)} for ${b.body}`)
    .join(", ");
}

export const SUMMARY_PATH = "/v1/money/summary";
export const EXPORTS_PATH = "/v1/money/exports";

export type PeriodChoice = "today" | "yesterday" | "week" | "month" | "last_month" | "custom";

export const PERIOD_CHOICES: { value: PeriodChoice; label: string }[] = [
  { value: "today", label: "Today" },
  { value: "yesterday", label: "Yesterday" },
  { value: "week", label: "This week" },
  { value: "month", label: "This month" },
  { value: "last_month", label: "Last month" },
  { value: "custom", label: "Choose dates" },
];

/** The period picker's choice, as the page's ?period= holds it. */
export function periodChoice(v: string | null): PeriodChoice {
  return PERIOD_CHOICES.some((c) => c.value === v) ? (v as PeriodChoice) : "today";
}

/** A period as Lagos days, YYYY-MM-DD, to inclusive. */
export interface Period {
  from: string;
  to: string;
}

/** The longest period the server sums up or exports, in days. */
export const MAX_PERIOD_DAYS = 400;

const lagosDayFmt = new Intl.DateTimeFormat("en-CA", { timeZone: "Africa/Lagos", year: "numeric", month: "2-digit", day: "2-digit" });

/** The date in Lagos at a moment: "2026-10-23". */
export const lagosDate = (now: Date) => lagosDayFmt.format(now);

const asUTC = (day: string) => new Date(`${day}T00:00:00Z`);
const dayOf = (d: Date) => d.toISOString().slice(0, 10);
const addDays = (day: string, n: number) => {
  const d = asUTC(day);
  d.setUTCDate(d.getUTCDate() + n);
  return dayOf(d);
};

/** Whether text is a real calendar date, YYYY-MM-DD. */
export function isDay(text: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(text)) return false;
  const d = asUTC(text);
  return !Number.isNaN(d.getTime()) && dayOf(d) === text;
}

/** The period a choice means at a moment, in Lagos days; a week starts on Monday. Custom takes its dates as given. */
export function periodFor(choice: PeriodChoice, now = new Date(), custom: Period = { from: "", to: "" }): Period {
  const today = lagosDate(now);
  switch (choice) {
    case "yesterday": {
      const y = addDays(today, -1);
      return { from: y, to: y };
    }
    case "week": {
      const weekday = (asUTC(today).getUTCDay() + 6) % 7; // Monday is 0
      return { from: addDays(today, -weekday), to: today };
    }
    case "month":
      return { from: `${today.slice(0, 8)}01`, to: today };
    case "last_month": {
      const end = addDays(`${today.slice(0, 8)}01`, -1);
      return { from: `${end.slice(0, 8)}01`, to: end };
    }
    case "custom":
      return { from: custom.from, to: custom.to };
    default:
      return { from: today, to: today };
  }
}

/** Why the dates will be refused, or null. */
export function periodProblem(p: Period): string | null {
  if (!p.from) return "Choose the first day.";
  if (!isDay(p.from)) return "The first day does not look right.";
  if (!p.to) return "Choose the last day.";
  if (!isDay(p.to)) return "The last day does not look right.";
  if (p.to < p.from) return "The last day is on or after the first.";
  const days = (asUTC(p.to).getTime() - asUTC(p.from).getTime()) / 86400000 + 1;
  if (days > MAX_PERIOD_DAYS) return `A period is at most ${MAX_PERIOD_DAYS} days.`;
  return null;
}

/** The period as the API's query: from and to, Lagos days, to inclusive. */
export const periodQuery = (p: Period) => new URLSearchParams({ from: p.from, to: p.to }).toString();

/** The period in words: "23 Oct 2026", or "1 Oct 2026 to 23 Oct 2026". */
export function periodWords(p: Period): string {
  return p.from === p.to ? dateLabel(p.from) : `${dateLabel(p.from)} to ${dateLabel(p.to)}`;
}

export const summaryPath = (p: Period) => `${SUMMARY_PATH}?${periodQuery(p)}`;

export type ExportKind = "log" | "invoices" | "journal";

export const EXPORTS: { kind: ExportKind; label: string; help: string }[] = [
  { kind: "log", label: "Money log", help: "Every movement of money in the period, as the log holds it." },
  { kind: "invoices", label: "Invoices", help: "The invoices sent in the period, with what was paid on each." },
  { kind: "journal", label: "Accounting journal", help: "Double-entry lines a bookkeeping tool can import; debits equal credits." },
];

/** The CSV download for a period; reveal names the customers (invoices only, needing pii.reveal). */
export function exportPath(kind: ExportKind, p: Period, reveal = false): string {
  const q = new URLSearchParams({ from: p.from, to: p.to });
  if (reveal && kind === "invoices") q.set("reveal", "true");
  return `${EXPORTS_PATH}/${kind}?${q}`;
}

/** The file name the server gives a download: "journal-2026-10-01-to-2026-10-23.csv". */
export const exportFileName = (kind: ExportKind, p: Period) => `${kind}-${p.from}-to-${p.to}.csv`;

/** An effective fee rate as a percent: 150 basis points is "1.50%"; "" when there is none. */
export function feeRatePercent(bp: number | null | undefined): string {
  if (bp === undefined || bp === null || !Number.isFinite(bp)) return "";
  return `${(bp / 100).toFixed(2)}%`;
}

/** A count with its noun: "1 invoice", "3 invoices". */
export const counted = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
