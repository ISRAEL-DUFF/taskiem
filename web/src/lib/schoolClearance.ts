import { dateLabel } from "./invoices";
import type { PlanStanding } from "./schoolChasing";

// Clearance and the gate (docs/spec/schools.md 12, B8; docs/schools.md
// "Clearance"): the shapes the API answers, the words the pages use, and
// the gate screen's small logic (a search needs three letters; the "just
// checked" list is short and lives in this browser session only), kept
// here to be tested.

export type ClearanceStatus = "cleared" | "partly_paid" | "not_paid";
export type ClearanceReason = "paid" | "plan" | "override" | "no_fees" | "balance" | "no_invoice";
export type GateStatus = "cleared" | "see_bursar";

/** An override (GET /v1/schools/clearance-overrides). */
export interface ClearanceOverride {
  id: string;
  student_id: string;
  admission_number: string;
  term_id: string;
  term: string;
  reason: string;
  starts_on: string;
  ends_on: string;
  created_by: string;
  created_at: string;
  withdrawn_at?: string;
  withdrawn_by?: string;
  in_force: boolean;
}

/** Where a student stands for a term (GET /v1/schools/students/{id}/clearance). */
export interface Clearance {
  student_id: string;
  term_id: string;
  status: ClearanceStatus;
  reason: ClearanceReason;
  invoiced: boolean;
  total_kobo: number;
  paid_kobo: number;
  balance_kobo: number;
  plan?: PlanStanding;
  override?: ClearanceOverride;
}

/** One student on the clearance list (GET /v1/schools/clearance). */
export interface ClearanceRow extends Clearance {
  admission_number: string;
  name: string;
  student_status: string;
  class_id: string;
  class: string;
}

export interface ClearanceTotals {
  students: number;
  cleared: number;
  partly_paid: number;
  not_paid: number;
}

export interface ClearanceList {
  term_id: string;
  term: string;
  students: ClearanceRow[];
  totals: ClearanceTotals;
  today: string;
  revealed: boolean;
}

export interface StudentClearance {
  clearance: Clearance;
  overrides: ClearanceOverride[];
}

/** A student the gate's search found (GET /v1/gate/search). */
export interface GateHit {
  student_id: string;
  name: string;
  class: string;
}

/** The gate's answer (GET /v1/gate/students/{id}). */
export interface GateAnswer {
  student_id: string;
  name: string;
  class: string;
  status: GateStatus;
}

// --- words ---

export const CLEARANCE_STATUS: Record<ClearanceStatus, { label: string; tone: string }> = {
  cleared: { label: "Cleared", tone: "ok" },
  partly_paid: { label: "Partly paid", tone: "warn" },
  not_paid: { label: "Not paid", tone: "danger" },
};

export function clearanceStatus(s: string): { label: string; tone: string } {
  return CLEARANCE_STATUS[s as ClearanceStatus] ?? { label: s, tone: "muted" };
}

const REASONS: Record<ClearanceReason, string> = {
  paid: "paid in full",
  plan: "payment plan in good standing",
  override: "cleared by override",
  no_fees: "nothing to pay for the class",
  balance: "a balance is left",
  no_invoice: "fees set for the class, but no invoice yet",
};

/** Why a student stands where they do, in words. */
export function reasonWords(c: Pick<Clearance, "reason" | "override" | "plan">): string {
  const base = REASONS[c.reason] ?? c.reason;
  if (c.reason === "override" && c.override) return `${base} until ${dateLabel(c.override.ends_on)}`;
  if (c.reason === "plan" && c.plan?.next_due_on) return `${base} (next instalment ${dateLabel(c.plan.next_due_on)})`;
  return base;
}

/** The clearance list's totals in one line. */
export function totalsLine(t: ClearanceTotals | undefined): string {
  if (!t) return "";
  if (t.students === 0) return "No students enrolled in this term.";
  const s = (n: number) => `${n} student${n === 1 ? "" : "s"}`;
  return `${s(t.students)}: ${t.cleared} cleared, ${t.partly_paid} partly paid, ${t.not_paid} not paid.`;
}

/** The path of the clearance list for a term and class, as JSON or CSV. */
export function clearancePath(filter: { term?: string; class?: string }, format?: "csv", reveal?: boolean): string {
  const q = new URLSearchParams();
  if (filter.term) q.set("term", filter.term);
  if (filter.class) q.set("class", filter.class);
  if (format) q.set("format", format);
  if (reveal) q.set("reveal", "true");
  const s = q.toString();
  return "/v1/schools/clearance" + (s ? "?" + s : "");
}

/** What is wrong with an override before it is sent, or "" when nothing. */
export function overrideProblem(f: { reason: string; ends_on: string }, today = new Date()): string {
  const reason = f.reason.trim();
  if (!reason) return "Give the reason the student is cleared (it is kept with the override).";
  if (reason.length > 300) return "The reason has up to 300 characters.";
  if (f.ends_on) {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(f.ends_on)) return "The end date is a date, YYYY-MM-DD.";
    const d = new Date(f.ends_on + "T12:00:00");
    const start = new Date(today.getFullYear(), today.getMonth(), today.getDate());
    const limit = new Date(start.getFullYear() + 1, start.getMonth(), start.getDate());
    if (d < start) return "The override cannot end in the past.";
    if (d > limit) return "The override ends within a year.";
  }
  return "";
}

/** An override in one line. */
export function overrideLine(o: ClearanceOverride): string {
  const span = `${dateLabel(o.starts_on)} to ${dateLabel(o.ends_on)}`;
  if (o.withdrawn_at) return `${o.term}: ${o.reason} (${span}; withdrawn)`;
  if (!o.in_force) return `${o.term}: ${o.reason} (${span}; ended)`;
  return `${o.term}: ${o.reason} (${span})`;
}

// --- the gate ---

/** The fewest letters a gate search takes. */
export const GATE_MIN_LETTERS = 3;

/** Whether the letters typed are enough to search. */
export function canSearch(q: string): boolean {
  return q.trim().replace(/\s+/g, " ").length >= GATE_MIN_LETTERS;
}

/** The gate's two answers, in the words on the screen. */
export const GATE_WORDS: Record<GateStatus, { label: string; mark: string; tone: string; hint: string }> = {
  cleared: { label: "CLEARED", mark: "✓", tone: "ok", hint: "Come in." },
  see_bursar: { label: "SEE THE BURSAR", mark: "⚠", tone: "warn", hint: "Please see the bursar before class." },
};

/** A recent check on the gate screen: who, and the answer. */
export interface RecentCheck {
  student_id: string;
  name: string;
  class: string;
  status: GateStatus;
  at: string;
}

/** How many recent checks the gate keeps. */
export const RECENT_MAX = 8;

/** Adds a check to the front of the recent list, once per student, at most RECENT_MAX. */
export function remember(list: RecentCheck[], a: GateAnswer, at = new Date().toISOString()): RecentCheck[] {
  const rest = list.filter((r) => r.student_id !== a.student_id);
  return [{ student_id: a.student_id, name: a.name, class: a.class, status: a.status, at }, ...rest].slice(0, RECENT_MAX);
}

const RECENT_KEY = "taskiem-gate-recent";

/** The recent checks kept in this browser tab's session (cleared on sign-out). */
export function loadRecent(storage: Pick<Storage, "getItem"> | undefined = typeof sessionStorage === "undefined" ? undefined : sessionStorage): RecentCheck[] {
  try {
    const raw = storage?.getItem(RECENT_KEY);
    const v: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(v) ? (v as RecentCheck[]).filter((r) => r && typeof r.student_id === "string" && (r.status === "cleared" || r.status === "see_bursar")).slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

export function saveRecent(list: RecentCheck[], storage: Pick<Storage, "setItem"> | undefined = typeof sessionStorage === "undefined" ? undefined : sessionStorage): void {
  try {
    storage?.setItem(RECENT_KEY, JSON.stringify(list));
  } catch {
    // Storage blocked: the list lives in memory only.
  }
}

export function clearRecent(storage: Pick<Storage, "removeItem"> | undefined = typeof sessionStorage === "undefined" ? undefined : sessionStorage): void {
  try {
    storage?.removeItem(RECENT_KEY);
  } catch {
    // Nothing to clear.
  }
}

/** The time of a check, for the recent list: HH:MM. */
export function checkTime(at: string): string {
  const d = new Date(at);
  if (Number.isNaN(d.getTime())) return "";
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** The gate screen never shows an amount: a guard the tests and the screen share. */
export function hasAmountField(v: unknown): boolean {
  const bad = /amount|kobo|naira|balance|paid|due|total|percent|invoice|payment/i;
  if (Array.isArray(v)) return v.some(hasAmountField);
  if (v && typeof v === "object") return Object.entries(v as Record<string, unknown>).some(([k, child]) => bad.test(k) || hasAmountField(child));
  return false;
}
