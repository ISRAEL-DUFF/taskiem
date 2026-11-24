// This month's run in Taskiem (P4b-3, docs/spec/chat-pay-people.md):
// preparing it, each person's breakdown, a bonus or a deduction, leaving
// someone out, recalculating and cancelling, the wallet against the
// total, and approving, rejecting and locking with Payrolla's code. The
// words these screens say. Payrolla's code is sent once and kept nowhere.

import { naira } from "./invoices";
import type { PayrollLine, PayrollRun, PayrollRunView } from "./payroll";

/** One line of a person's breakdown. */
export interface PayrollItem {
  kind: "earning" | "deduction" | "employer";
  code: string;
  label: string;
  amount_kobo: number;
}

/** A person's line with its breakdown (P4b-3). */
export interface PayrollLineFull extends PayrollLine {
  gross_kobo?: number;
  total_earnings_kobo?: number;
  total_deductions_kobo?: number;
  items?: PayrollItem[];
}

export interface RunAdjustment {
  id: string;
  employee_id: string;
  name: string;
  type: "bonus" | "deduction";
  amount_kobo: number;
  reason: string;
}

export interface RunExclusion {
  id: string;
  employee_id: string;
  name: string;
  reason: string;
}

export interface RunChanges {
  since?: string;
  new: string[];
  left: string[];
  pay_changed: { name: string; from_kobo: number; to_kobo: number }[];
}

export interface RunFunding {
  has_wallet: boolean;
  available_kobo: number;
  needed_kobo: number;
  short_by_kobo: number;
  words?: string;
}

export interface RunMay {
  prepare: boolean;
  approve: boolean;
  linked: boolean;
  reconnect: boolean;
}

/** GET /v1/payroll/runs/{run}, as P4b-3 has it. */
export interface PayrollRunFull extends Omit<PayrollRunView, "lines"> {
  lines: PayrollLineFull[];
  adjustments?: RunAdjustment[];
  exclusions?: RunExclusion[];
  changes?: RunChanges;
  funding?: RunFunding;
  may?: RunMay;
  not_worked_out?: string;
  pays_on?: string;
}

/** The Payroll page's This month panel. */
export interface ThisMonth {
  period: string;
  label: string;
  month: string;
  run?: PayrollRun;
}

/** A change's answer: the run as it now is. */
export interface RunChanged {
  run: PayrollRun;
  compute_error?: string;
}

/** POST /v1/payroll/runs/{run}/confirmations. */
export interface RunCodeSent {
  challenge: { challenge_id: string; channel: "TOTP" | "SMS" | "EMAIL"; destination_hint?: string; expires_at: string };
  sent_to: string;
  safer: boolean;
}

/** POST …/approve and …/lock. */
export interface RunDecided {
  run: PayrollRun;
  payment: { status: string; scheduled_for?: string; total_kobo: number };
  words: string;
}

export type RunAction = "approve" | "lock";

/** The run is open to changes (not approved yet). */
export const openToChange = (status: string) => status === "DRAFT" || status === "COMPUTED";

/** Which decision is next, for one who may decide: approve a run waiting
 * for approval, lock an approved one. */
export function nextDecision(status: string): RunAction | null {
  if (status === "COMPUTED") return "approve";
  if (status === "APPROVED") return "lock";
  return null;
}

/** "Approve November 2026 payroll", "Lock and pay". */
export const actionWords = (a: RunAction) => (a === "approve" ? "Approve" : "Lock and pay");

/** What locking does, said before the code is asked for. */
export function lockWarning(payDay: string): string {
  return `Locking pays everyone on ${payDay}. A locked payroll can't be unlocked.`;
}

/** A person's breakdown, by kind: what they earn, what is taken, and what
 * the business adds on top (not part of their pay). */
export function breakdown(items: PayrollItem[] | undefined): { earnings: PayrollItem[]; deductions: PayrollItem[]; employer: PayrollItem[] } {
  const all = items ?? [];
  return {
    earnings: all.filter((i) => i.kind === "earning"),
    deductions: all.filter((i) => i.kind === "deduction"),
    employer: all.filter((i) => i.kind === "employer"),
  };
}

/** "₦100,000 bonus, Q4 target" or "₦20,000 deduction, Advance repaid". */
export function adjustmentWords(a: RunAdjustment): string {
  return `${naira(a.amount_kobo)} ${a.type === "bonus" ? "bonus" : "deduction"}, ${a.reason}`;
}

/** What changed since last month, as lines to show ("New: Ada Obi"). */
export function changesWords(c: RunChanges | undefined): string[] {
  if (!c) return [];
  const out: string[] = [];
  if (c.new.length) out.push(`New this month: ${c.new.join(", ")}`);
  if (c.left.length) out.push(`Not paid this month: ${c.left.join(", ")}`);
  for (const p of c.pay_changed) out.push(`${p.name}: pay ${naira(p.from_kobo)} → ${naira(p.to_kobo)}`);
  return out;
}

/** Naira typed ("100,000", "100k", "1.5m") as kobo; null when unreadable. */
export function koboOf(typed: string): number | null {
  const t = typed.trim().toLowerCase().replace(/[₦,\s]/g, "").replace(/^n(?=\d)/, "");
  const m = /^(\d+(?:\.\d{1,2})?)([km]?)$/.exec(t);
  if (!m) return null;
  const n = Number(m[1]) * (m[2] === "k" ? 1_000 : m[2] === "m" ? 1_000_000 : 1);
  const kobo = Math.round(n * 100);
  return kobo >= 100 ? kobo : null;
}

/** The POST body of a bonus or a deduction. */
export function adjustmentBody(employee: string, type: "bonus" | "deduction", kobo: number, reason: string, key: string) {
  return { employee_id: employee, type, amount_kobo: kobo, reason: reason.trim(), key };
}

/** Refusals after which a new code is the way on. */
export const CODE_AGAIN = new Set(["code_expired", "code_locked", "code_used"]);

/** The chat's card, as the Taskiem conversation keeps it. */
export interface PayrollCardMsg {
  run_id: string;
  period: string;
}

export function parsePayrollCard(text: string): PayrollCardMsg | null {
  try {
    const v = JSON.parse(text) as Partial<PayrollCardMsg>;
    if (typeof v.run_id === "string" && v.run_id) return { run_id: v.run_id, period: typeof v.period === "string" ? v.period : "" };
  } catch {
    /* not a card */
  }
  return null;
}
