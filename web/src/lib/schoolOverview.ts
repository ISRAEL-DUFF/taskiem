import { dateLabel, naira } from "./invoices";
import type { Debtor, BucketTotal } from "./schoolChasing";
import type { ClearanceTotals } from "./schoolClearance";

// The proprietor (docs/spec/schools.md 13, B9; docs/schools.md
// "Overview", "The daily close"): the shapes the API answers, the words
// the Overview and the close use, kept here to be tested.

export interface HowPaid {
  how: "account" | "card" | "cash" | "transfer" | "other";
  payments: number;
  kobo: number;
}

export interface Matched {
  auto_kobo: number;
  person_kobo: number;
  auto_percent: number;
}

export interface CloseSum {
  count: number;
  kobo: number;
}

export type CloseItemKind = "unmatched" | "pending_posting" | "pending_discount" | "claim" | "credit_without_entry" | "receipt_not_voided" | "part_without_receipt";

export interface CloseItem {
  kind: CloseItemKind;
  message: string;
  path: string;
  amount_kobo?: number;
  ref?: string;
}

/** A close as the list shows it (GET /v1/schools/closes). */
export interface CloseBrief {
  id: string;
  day: string;
  ran_at: string;
  ran_by: string;
  reconciles: boolean;
  problems: number;
}

/** A day's close (GET /v1/schools/closes/{day}, POST /v1/schools/close). */
export interface SchoolClose extends CloseBrief {
  term_id?: string;
  term?: string;
  money_in: CloseSum;
  into_accounts: CloseSum;
  at_the_desk: CloseSum;
  matched_auto: CloseSum;
  matched_person: CloseSum;
  unmatched: CloseSum;
  unmatched_list: CloseItem[];
  lookback_found: CloseSum;
  pending: CloseItem[];
  claims: CloseItem[];
  problem_list: CloseItem[];
}

/** Schools > Overview (GET /v1/schools/overview). */
export interface SchoolOverview {
  term_id: string;
  term: string;
  term_state: "planned" | "current" | "past";
  today: string;
  enrolled: number;
  invoiced: number;
  expected_kobo: number;
  expected_by_now_kobo: number;
  collected_kobo: number;
  outstanding_kobo: number;
  overdue_kobo: number;
  collected_percent: number;
  this_week_kobo: number;
  pass_through_kobo: number;
  pass_through: { body: string; kobo: number }[];
  matched: Matched;
  how_paid: HowPaid[];
  top_debtors: Debtor[];
  aging: Record<string, BucketTotal>;
  debtors: number;
  debtors_kobo: number;
  clearance: ClearanceTotals;
  last_close?: CloseBrief;
}

export interface OverviewResponse {
  overview: SchoolOverview;
  revealed: boolean;
}

export const HOW_PAID_WORDS: Record<HowPaid["how"], string> = {
  account: "Account number",
  card: "Card or POS",
  cash: "Cash",
  transfer: "Transfer to the school's account",
  other: "Other",
};

export const ITEM_WORDS: Record<CloseItemKind, string> = {
  unmatched: "Not matched",
  pending_posting: "Cash posting waiting for a second person",
  pending_discount: "Discount waiting for a second person",
  claim: "A family says it paid",
  credit_without_entry: "A payment with no money log entry",
  receipt_not_voided: "A receipt that should be voided",
  part_without_receipt: "A payment with no receipt",
};

/** GET /v1/schools/overview with its filters. */
export function overviewPath(term: string, reveal = false): string {
  const q = new URLSearchParams();
  if (term) q.set("term", term);
  if (reveal) q.set("reveal", "true");
  const s = q.toString();
  return `/v1/schools/overview${s ? `?${s}` : ""}`;
}

/** GET /v1/schools/closes between two days (either empty). */
export function closesPath(from = "", to = ""): string {
  const q = new URLSearchParams();
  if (from) q.set("from", from);
  if (to) q.set("to", to);
  const s = q.toString();
  return `/v1/schools/closes${s ? `?${s}` : ""}`;
}

/** part of whole as a whole percentage, 0 to 100; 0 when the whole is nothing. */
export function percentOf(part: number, whole: number): number {
  if (whole <= 0) return 0;
  return Math.min(100, Math.max(0, Math.floor((part * 100) / whole)));
}

/** "₦140,000 of ₦300,000 collected (46%)". */
export function collectedLine(ov: Pick<SchoolOverview, "collected_kobo" | "expected_kobo" | "collected_percent">): string {
  return `${naira(ov.collected_kobo)} of ${naira(ov.expected_kobo)} collected (${ov.collected_percent}%)`;
}

/** What was due by today and what of it is still unpaid. */
export function dueByNowLine(ov: Pick<SchoolOverview, "expected_by_now_kobo" | "overdue_kobo">): string {
  if (ov.expected_by_now_kobo <= 0) return "Nothing is due yet.";
  if (ov.overdue_kobo <= 0) return `${naira(ov.expected_by_now_kobo)} was due by today, all of it paid.`;
  return `${naira(ov.expected_by_now_kobo)} was due by today; ${naira(ov.overdue_kobo)} of it is still unpaid.`;
}

/** "72% by itself (₦…); ₦… by a person". */
export function matchedLine(m: Matched): string {
  if (m.auto_kobo + m.person_kobo <= 0) return "Nothing counted yet.";
  return `${naira(m.auto_kobo)} matched by itself, ${naira(m.person_kobo)} by a person.`;
}

/** "1 cleared, 1 partly paid, 1 not paid of 3". */
export function clearanceLine(c: ClearanceTotals): string {
  return `${c.cleared} cleared, ${c.partly_paid} partly paid, ${c.not_paid} not paid of ${c.students}`;
}

/** Whether a close reconciles, in words and a tone. */
export function closeStatus(c: Pick<CloseBrief, "reconciles" | "problems">): { label: string; tone: string } {
  if (c.reconciles) return { label: "Reconciles", tone: "ok" };
  return { label: `Does not reconcile: ${c.problems} problem${c.problems === 1 ? "" : "s"}`, tone: "warn" };
}

/** "₦150,000 in on students (2 payments): ₦100,000 into account numbers, ₦50,000 at the desk". */
export function closeMoneyLine(c: Pick<SchoolClose, "money_in" | "into_accounts" | "at_the_desk">): string {
  return `${naira(c.money_in.kobo)} in on students (${c.money_in.count} payment${c.money_in.count === 1 ? "" : "s"}): ${naira(c.into_accounts.kobo)} into account numbers, ${naira(c.at_the_desk.kobo)} at the desk`;
}

/** "₦100,000 matched by itself, ₦50,000 by a person; ₦0 not matched". */
export function closeMatchedLine(c: Pick<SchoolClose, "matched_auto" | "matched_person" | "unmatched">): string {
  return `${naira(c.matched_auto.kobo)} matched by itself, ${naira(c.matched_person.kobo)} by a person; ${naira(c.unmatched.kobo)} not matched`;
}

/** The close's lists, the problems first, each with a heading; empty lists left out. */
export function closeSections(c: Pick<SchoolClose, "problem_list" | "pending" | "claims" | "unmatched_list">): { head: string; items: CloseItem[]; problem: boolean }[] {
  return [
    { head: "Does not reconcile", items: c.problem_list, problem: true },
    { head: "Waiting for a second person", items: c.pending, problem: false },
    { head: "Families who say they paid", items: c.claims, problem: false },
    { head: "Money not matched", items: c.unmatched_list, problem: false },
  ].filter((s) => s.items.length > 0);
}

/** A close's day for people: "24 Oct 2026". */
export function closeDayLabel(day: string): string {
  return dateLabel(day) || day;
}

/** The last close for the Overview: "24 Oct 2026, reconciles" or that none was taken. */
export function lastCloseLine(c: CloseBrief | undefined): string {
  if (!c) return "No close taken yet: the first is taken after midnight.";
  return `${closeDayLabel(c.day)}: ${closeStatus(c).label.toLowerCase()}.`;
}
