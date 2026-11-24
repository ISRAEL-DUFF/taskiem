// After locking (P4b-4, docs/spec/chat-pay-people.md): a payroll's
// Payment (pays on the date, waiting for money, paying, paid, some not
// paid), Pay now and Pay the failed ones again with Payrolla's code, and
// payslips as Taskiem's own cards: the full breakdown, private to the
// person, read from Payrolla when opened and kept nowhere.

import type { PayrollItem } from "./payrollrun";
import type { PayrollTone } from "./payroll";

/** A locked payroll's payment, as GET /v1/payroll/runs/{run} has it. */
export interface RunPayment {
  status: "NOT_SCHEDULED" | "SCHEDULED" | "AWAITING_FUNDS" | "PAYING" | "COMPLETED" | "PARTIALLY_COMPLETED" | "FAILED";
  words: string;
  tone: "ok" | "warn" | "bad" | "info" | "";
  scheduled_for?: string;
  total_kobo: number;
  paid: number;
  failed: number;
  returned: number;
  pending: number;
  short_by_kobo: number;
  may_pay_now: boolean;
  may_retry: boolean;
  may_payslips: boolean;
  cannot_pay: boolean;
}

/** The tone a payment's words are shown in. */
export function paymentTone(p: Pick<RunPayment, "tone">): PayrollTone {
  switch (p.tone) {
    case "ok":
      return "ok";
    case "warn":
      return "warn";
    case "bad":
      return "danger";
    case "info":
      return "info";
  }
  return "muted";
}

/** "Paid 46 of 48 people", "2 not paid, 1 returned by the bank". */
export function paymentCounts(p: RunPayment): string {
  const parts: string[] = [];
  const total = p.paid + p.failed + p.returned + p.pending;
  if (p.paid > 0) parts.push(`Paid ${p.paid} of ${total} ${total === 1 ? "person" : "people"}`);
  if (p.failed > 0) parts.push(`${p.failed} not paid`);
  if (p.returned > 0) parts.push(`${p.returned} returned by the bank`);
  return parts.join(", ");
}

/** A payslip as Taskiem's own card. */
export interface Payslip {
  payslip_id: string;
  run_id: string;
  period: string;
  month: string;
  period_start: string;
  pay_date: string;
  employee: { id: string; name: string; department?: string };
  employer: { name: string; rc_number?: string };
  gross_kobo: number;
  total_earnings_kobo: number;
  total_deductions_kobo: number;
  net_kobo: number;
  items: PayrollItem[];
  payment: { status: string; state: "paid" | "waiting" | "failed" | "returned"; words: string };
}

/** One of my payslips, listed on My pay. */
export interface MyPayslipRow {
  payslip_id: string;
  run_id: string;
  period: string;
  month: string;
  pay_date: string;
  gross_kobo: number;
  net_kobo: number;
  state: "paid" | "waiting" | "failed" | "returned";
  words: string;
}

/** Where a payslip is read: anyone's (payroll.read) or one's own. */
export const payslipPath = (id: string, own: boolean, rest = "") =>
  `/v1/payroll/${own ? "me/payslips" : "payslips"}/${encodeURIComponent(id)}${rest}`;

/** The PDF's file name: payslip-november-2026.pdf. */
export function payslipFile(period: string): string {
  const slug = period
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
  return `payslip-${slug || "payroll"}.pdf`;
}

/** The chat's payslip card, as the Taskiem conversation keeps it. */
export interface PayslipCardMsg {
  payslip_id: string;
  period: string;
}

export function parsePayslipCard(text: string): PayslipCardMsg | null {
  try {
    const v = JSON.parse(text) as Partial<PayslipCardMsg>;
    if (typeof v.payslip_id === "string" && v.payslip_id) return { payslip_id: v.payslip_id, period: typeof v.period === "string" ? v.period : "" };
  } catch {
    /* not a card */
  }
  return null;
}

/** "Your payslip for November is ready". */
export const payslipReady = (month: string) => `Your payslip for ${month || "this month"} is ready`;
