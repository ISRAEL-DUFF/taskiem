import { dateLabel, naira } from "./invoices";
import type { Instalment } from "./schoolFamily";

// Chasing (docs/spec/schools.md 11, B7; docs/schools.md#debtors): the
// shapes Debtors, the reminder ladder, promises to pay and plan standing
// answer, and the words and checks the pages use, kept here to be tested.

export type Bucket = "not_due" | "0_7" | "7_14" | "14_30" | "over_30";
export type PromiseStatus = "open" | "kept" | "missed" | "withdrawn";
export type PlanState = "good" | "arrears" | "settled";

export const BUCKETS: { value: Bucket; label: string }[] = [
  { value: "not_due", label: "Not yet due" },
  { value: "0_7", label: "0 to 7 days" },
  { value: "7_14", label: "7 to 14 days" },
  { value: "14_30", label: "14 to 30 days" },
  { value: "over_30", label: "Over 30 days" },
];

export const HAS_FILTERS: { value: string; label: string }[] = [
  { value: "", label: "Everyone" },
  { value: "promise", label: "With a promise to pay" },
  { value: "missed", label: "Promise missed" },
  { value: "plan", label: "Plan in good standing" },
  { value: "arrears", label: "Plan in arrears" },
  { value: "none", label: "No promise or plan" },
];

/** The most students one send may reach (the server refuses more). */
export const MAX_REMIND = 50;

export const RUNGS: { value: number; label: string; tone: string }[] = [
  { value: 1, label: "1: gentle", tone: "A reminder of the balance and the account number" },
  { value: 2, label: "2: firmer", tone: "The balance and the due date passed" },
  { value: 3, label: "3: final", tone: "The balance and what happens next" },
];

export const PLACEHOLDERS = ["balance", "child", "account_number", "due_date", "link"] as const;

export interface DebtorInvoice {
  invoice_id: string;
  number: string;
  term_id: string;
  term: string;
  due_on?: string;
  due_kobo: number;
}

export interface ReminderBrief {
  rung: number;
  at: string;
  nobody?: boolean;
}

/** A promise to pay (GET /v1/schools/promises). */
export interface PromiseToPay {
  id: string;
  student_id: string;
  admission_number: string;
  family_id?: string;
  promised_on: string;
  note?: string;
  balance_kobo: number;
  status: PromiseStatus;
  created_by: string;
  created_at: string;
  resolved_at?: string;
  resolved_by?: string;
}

export interface MissedInstalment {
  instalment: number;
  due_on: string;
  amount_kobo: number;
  short_kobo: number;
  days_past: number;
}

/** Where a student's approved plan stands. */
export interface PlanStanding {
  plan_id: string;
  student_id: string;
  state: PlanState;
  approved_at: string;
  instalments: Instalment[];
  grace_days: number;
  paid_since_kobo: number;
  due_so_far_kobo: number;
  missed: MissedInstalment[];
  next_due_on?: string;
  next_amount_kobo?: number;
}

export interface PlanArrear {
  plan_id: string;
  instalment: number;
  student_id: string;
  due_on: string;
  amount_kobo: number;
  short_kobo: number;
  marked_at: string;
}

/** A student with a balance (GET /v1/schools/debtors). */
export interface Debtor {
  student_id: string;
  admission_number: string;
  name: string;
  status: string;
  class_id: string;
  class: string;
  family_id: string | null;
  balance_kobo: number;
  due_on?: string;
  days_past_due: number;
  bucket: Bucket;
  invoices: DebtorInvoice[];
  last_reminder?: ReminderBrief;
  promise?: PromiseToPay;
  promise_missed?: boolean;
  plan?: PlanStanding;
}

export interface BucketTotal {
  students: number;
  balance_kobo: number;
}

export interface DebtorsResponse {
  debtors: Debtor[];
  totals: Record<string, BucketTotal>;
  students: number;
  balance_kobo: number;
  today: string;
  revealed: boolean;
}

export interface RemindOutcome {
  student_id: string;
  admission_number: string;
  family_id?: string;
  rung?: number;
  status: "sent" | "skipped";
  reason?: string;
  balance_kobo?: number;
  channels?: Record<string, number>;
  nobody?: boolean;
}

/** What a send did (POST /v1/schools/reminders). */
export interface Reminded {
  key: string;
  sent: number;
  skipped: number;
  by_rung: Record<string, number>;
  results: RemindOutcome[];
  replayed?: boolean;
}

export interface Reminder {
  id: string;
  student_id: string;
  family_id: string;
  rung: number;
  balance_kobo: number;
  sent_on: string;
  channels: Record<string, number>;
  nobody: boolean;
  created_by: string;
  created_at: string;
}

/** A student's chasing (GET /v1/schools/students/{id}/chasing). */
export interface StudentChasing {
  balance_kobo: number;
  due_on?: string;
  days_past_due: number;
  bucket: Bucket;
  promises: PromiseToPay[];
  reminders: Reminder[];
  plan?: PlanStanding | null;
  arrears: PlanArrear[];
  invoices: DebtorInvoice[];
}

export interface Checked {
  promises_kept: number;
  promises_missed: number;
  arrears: number;
}

export interface DebtorFilter {
  class?: string;
  bucket?: string;
  has?: string;
}

/** The Debtors request for a filter, as JSON or as the CSV file. */
export function debtorsPath(f: DebtorFilter, format?: "csv"): string {
  const q = new URLSearchParams();
  if (f.class) q.set("class", f.class);
  if (f.bucket) q.set("bucket", f.bucket);
  if (f.has) q.set("has", f.has);
  if (format) q.set("format", format);
  const s = q.toString();
  return `/v1/schools/debtors${s ? `?${s}` : ""}`;
}

export function bucketLabel(b: string): string {
  return BUCKETS.find((x) => x.value === b)?.label ?? b;
}

/** "Not yet due", "Due today", "10 days past due". */
export function daysWords(d: Pick<Debtor, "days_past_due" | "due_on">): string {
  if (d.days_past_due <= 0) return d.due_on ? `Due ${dateLabel(d.due_on)}` : "Not yet due";
  return `${d.days_past_due} day${d.days_past_due === 1 ? "" : "s"} past due`;
}

/** Adds or removes a student from the selection; null when adding would pass the cap. */
export function toggleSelection(selected: string[], id: string): string[] | null {
  if (selected.includes(id)) return selected.filter((x) => x !== id);
  if (selected.length >= MAX_REMIND) return null;
  return [...selected, id];
}

/** "3 of 50 selected". */
export function selectionCounter(n: number): string {
  return `${n} of ${MAX_REMIND} selected`;
}

/** Selects the listed students up to the cap, keeping those already chosen. */
export function selectUpToCap(selected: string[], ids: string[]): string[] {
  const out = [...selected];
  for (const id of ids) {
    if (out.length >= MAX_REMIND) break;
    if (!out.includes(id)) out.push(id);
  }
  return out;
}

export function rungLabel(r: number | undefined): string {
  return RUNGS.find((x) => x.value === r)?.label ?? (r ? `${r}` : "");
}

/** "Sent by SMS to 1 guardian", "On WhatsApp to 2 guardians", "No guardian could be reached". */
export function channelsLine(channels: Record<string, number> | undefined, nobody: boolean | undefined): string {
  const words: Record<string, string> = { whatsapp: "on WhatsApp", telegram: "on Telegram", sms: "by SMS" };
  const parts: string[] = [];
  for (const [ch, n] of Object.entries(channels ?? {})) {
    if (n > 0) parts.push(`${words[ch] ?? ch} to ${n} guardian${n === 1 ? "" : "s"}`);
  }
  if (parts.length === 0 || nobody) return "No guardian could be reached (no phone, or messages are off)";
  const s = parts.join(", ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** "2 sent, 1 skipped." */
export function remindedLine(r: Pick<Reminded, "sent" | "skipped" | "replayed">): string {
  const s = `${r.sent} sent, ${r.skipped} skipped.`;
  return r.replayed ? `${s} (This send was made before: nothing went out again.)` : s;
}

/** One student's outcome: "GCA/001: sent (rung 1), by SMS to 1 guardian" or "GCA/003: skipped: a promise to pay by 25 Oct 2026". */
export function outcomeLine(o: RemindOutcome): string {
  if (o.status === "sent") return `${o.admission_number}: sent (rung ${o.rung}), ${channelsLine(o.channels, o.nobody).toLowerCase()}`;
  return `${o.admission_number}: skipped: ${o.reason ?? ""}`;
}

export function promiseStatus(s: PromiseStatus): { label: string; tone: string } {
  switch (s) {
    case "kept":
      return { label: "Kept", tone: "ok" };
    case "missed":
      return { label: "Missed", tone: "danger" };
    case "withdrawn":
      return { label: "Withdrawn", tone: "muted" };
    default:
      return { label: "Open", tone: "warn" };
  }
}

/** "Promised by 25 Oct 2026: salary on the 25th". */
export function promiseLine(p: Pick<PromiseToPay, "promised_on" | "note">): string {
  return `Promised by ${dateLabel(p.promised_on)}${p.note ? `: ${p.note}` : ""}`;
}

export function planStateWords(p: PlanStanding | null | undefined): { label: string; tone: string } | null {
  if (!p) return null;
  switch (p.state) {
    case "arrears":
      return { label: "Plan in arrears", tone: "danger" };
    case "settled":
      return { label: "Plan settled", tone: "ok" };
    default:
      return { label: "Plan in good standing", tone: "ok" };
  }
}

/** "Next instalment ₦50,000 by 15 Nov 2026" or "Instalment 1 of ₦50,000 due 1 Nov 2026, ₦50,000 short (3 days)". */
export function planLine(p: PlanStanding): string {
  if (p.state === "arrears" && p.missed.length > 0) {
    const m = p.missed[0];
    if (m) return `Instalment ${m.instalment} of ${naira(m.amount_kobo)} due ${dateLabel(m.due_on)}, ${naira(m.short_kobo)} short (${m.days_past} day${m.days_past === 1 ? "" : "s"})`;
  }
  if (p.state === "settled") return `${naira(p.paid_since_kobo)} paid since the plan was approved`;
  if (p.next_due_on) return `Next instalment ${naira(p.next_amount_kobo ?? 0)} by ${dateLabel(p.next_due_on)}`;
  return `${naira(p.paid_since_kobo)} paid since the plan was approved`;
}

/** The flags a Debtors row shows. */
export function debtorFlags(d: Pick<Debtor, "promise" | "promise_missed" | "plan">): { label: string; tone: string }[] {
  const out: { label: string; tone: string }[] = [];
  if (d.promise_missed) out.push({ label: "Promise missed", tone: "danger" });
  else if (d.promise?.status === "open") out.push({ label: `Promised by ${dateLabel(d.promise.promised_on)}`, tone: "warn" });
  const plan = planStateWords(d.plan);
  if (plan) out.push(plan);
  return out;
}

/** Why a promise cannot be recorded yet, or null. */
export function promiseProblem(date: string, note: string, today = new Date()): string | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return "Choose the date the family promised to pay by.";
  const t = today.toISOString().slice(0, 10);
  if (date < t) return "The date is today or later.";
  const limit = new Date(today);
  limit.setFullYear(limit.getFullYear() + 1);
  if (date > limit.toISOString().slice(0, 10)) return "The date is within a year.";
  if (note.length > 300) return "A note has up to 300 characters.";
  return null;
}

/** Why a rung's wording cannot be saved, or null: unknown placeholders, length. Empty is the default. */
export function wordingProblem(text: string): string | null {
  if (text.trim() === "") return null;
  if ([...text].length > 600) return "A rung's wording has up to 600 characters.";
  const unknown: string[] = [];
  for (const m of text.matchAll(/\{([a-z_]*)\}/g)) {
    const name = m[1] ?? "";
    if (!(PLACEHOLDERS as readonly string[]).includes(name)) unknown.push(`{${name}}`);
  }
  if (unknown.length > 0) return `${unknown.join(", ")} is not a placeholder. Use {balance}, {child}, {account_number}, {due_date} or {link}.`;
  return null;
}

export const SAMPLE_VARS = { balance: "₦40,000", child: "Ebube", account_number: "0123456789 (Wema Bank)", due_date: "7 Sep 2026", link: "https://pay.example/s/abc" };

/** A rung's wording as a family would read it, with sample values; the link added when missing, as the server adds it. */
export function renderPreview(text: string, vars: Record<string, string> = SAMPLE_VARS): string {
  let t = text.trim();
  if (t === "") return "";
  if (!t.includes("{link}")) t += " Your fees page: {link}";
  return t
    .replace(/\{(balance|child|account_number|due_date|link)\}/g, (_, k: string) => vars[k] ?? "")
    .replace(/\s+/g, " ")
    .trim();
}

/** Why the plan grace cannot be saved, or null. */
export function graceProblem(v: string): string | null {
  const n = Number(v.trim());
  if (v.trim() === "" || !Number.isInteger(n) || n < 0 || n > 60) return "The grace is a whole number of days, 0 to 60.";
  return null;
}

/** The totals line of a bucket: "3 students · ₦300,000". */
export function totalLine(t: BucketTotal | undefined): string {
  if (!t || t.students === 0) return "None";
  return `${t.students} student${t.students === 1 ? "" : "s"} · ${naira(t.balance_kobo)}`;
}

/** What the check did: "1 promise missed, 1 kept; 2 instalments in arrears." */
export function checkedLine(c: Checked): string {
  const parts = [`${c.promises_missed} promise${c.promises_missed === 1 ? "" : "s"} missed`, `${c.promises_kept} kept`, `${c.arrears} instalment${c.arrears === 1 ? "" : "s"} newly in arrears`];
  return `Checked: ${parts.join(", ")}.`;
}
