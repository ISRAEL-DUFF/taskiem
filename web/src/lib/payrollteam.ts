// Staff and pay from Taskiem (P4b-2, docs/spec/chat-pay-people.md):
// Payroll → Team, adding someone with their bank account checked,
// changing their pay or details, a changed salary account held for a
// second approver, Payroll → Account changes, and a staff member's own
// My pay. Payrolla keeps the pay and the accounts; Taskiem shows them and
// keeps none.

import { nairaKobo } from "./toolforms";

export interface TeamBank {
  bank: string;
  bank_code: string;
  account_name: string;
  last4: string;
}

export type ChangeVia = "in_taskiem" | "by_employee" | "in_payrolla";

/** A salary account change waiting for an approver. */
export interface BankChange {
  id: string;
  employee_id: string;
  employee?: string;
  bank: string;
  account_name: string;
  last4: string;
  via: ChangeVia;
  asked_by?: string;
  mine: boolean;
  requested_at: string;
}

export interface TeamPay {
  gross_kobo: number;
  basic_kobo: number;
  housing_kobo: number;
  transport_kobo: number;
  pensionable: boolean;
  from: string;
}

/** Someone in Payrolla, as Payroll → Team shows them. */
export interface TeamMember {
  id: string;
  name: string;
  first_name: string;
  last_name: string;
  email?: string;
  phone?: string;
  department?: string;
  start_date?: string;
  status: "active" | "inactive" | "left";
  bank: TeamBank | null;
  pay?: TeamPay;
  waiting_change: BankChange | null;
  staff: { user_id: string; name: string } | null;
}

export interface TeamMay {
  prepare: boolean;
  approve: boolean;
  see_pay: boolean;
}

export interface TeamStaff {
  user_id: string;
  name: string;
  email?: string;
  phone?: string;
}

/** GET /v1/payroll/team. */
export interface TeamView {
  team: TeamMember[];
  staff: TeamStaff[];
  may: TeamMay;
  connection: string;
}

/** GET /v1/payroll/bank-changes. */
export interface BankChangesView {
  changes: BankChange[];
  may: TeamMay;
}

/** GET /v1/payroll/me. */
export interface MyPay {
  linked: boolean;
  me?: { name: string; phone?: string; status: string; bank: TeamBank | null; waiting_change: BankChange | null };
}

export interface PayrollBank {
  code: string;
  name: string;
}

/** POST /v1/payroll/me/bank-change. */
export interface MyCodeSent {
  challenge: { challenge_id: string; channel: string; destination_hint?: string; expires_at: string };
  account_name: string;
  last4: string;
  bank: string;
  sent_to: string;
}

/** "••6789". */
export const masked = (last4: string) => `••${last4}`;

/** "Guaranty Trust Bank ••6789". */
export const bankLine = (b: Pick<TeamBank, "bank" | "last4"> | null | undefined) => (b ? `${b.bank} ${masked(b.last4)}` : "No account yet");

/** How a change was asked for, in words. */
export function viaWords(c: Pick<BankChange, "via" | "asked_by">): string {
  switch (c.via) {
    case "in_taskiem":
      return c.asked_by ? `Asked by ${c.asked_by} in Taskiem` : "Asked in Taskiem";
    case "by_employee":
      return "Asked by the employee";
    default:
      return "Asked in Payrolla";
  }
}

/** A person's line on Team: their department, their account, and
 * whether they left or have a change waiting. */
export function memberSub(m: TeamMember): string {
  const parts = [m.department ?? "", bankLine(m.bank)];
  if (m.status === "left") parts.push("Has left");
  if (m.waiting_change) parts.push("Account change waiting");
  return parts.filter(Boolean).join(" · ");
}

/** Today as YYYY-MM-DD, in the browser's own day. */
export function today(now = new Date()): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${now.getFullYear()}-${p(now.getMonth() + 1)}-${p(now.getDate())}`;
}

/** A full name split for Payrolla: the last word is the last name. */
export function splitName(full: string): { first: string; last: string } {
  const words = full.trim().split(/\s+/).filter(Boolean);
  if (words.length < 2) return { first: words[0] ?? "", last: "" };
  return { first: words.slice(0, -1).join(" "), last: words[words.length - 1] ?? "" };
}

/** A 10-digit account number as typed (spaces and dashes allowed). */
export const accountDigits = (s: string) => s.replace(/[\s-]/g, "");
export const isAccountNumber = (s: string) => /^\d{10}$/.test(accountDigits(s));

/** The Add someone form, as typed. */
export interface AddForm {
  first: string;
  last: string;
  phone: string;
  email: string;
  start: string;
  department: string;
  gross: string;
  basic: string;
  housing: string;
  transport: string;
  pensionable: boolean;
  bankCode: string;
  account: string;
  staffUser?: string;
}

export const emptyAdd = (): AddForm => ({
  first: "",
  last: "",
  phone: "",
  email: "",
  start: today(),
  department: "",
  gross: "",
  basic: "",
  housing: "",
  transport: "",
  pensionable: true,
  bankCode: "",
  account: "",
});

const optKobo = (s: string) => (s.trim() ? nairaKobo(s) : 0);

/** The pay as typed, in kobo, or what is wrong with it. */
export function payOf(f: Pick<AddForm, "gross" | "basic" | "housing" | "transport">): { gross: number; basic: number; housing: number; transport: number } | string {
  const gross = nairaKobo(f.gross);
  if (!(gross > 0)) return "Give their monthly pay.";
  const [basic, housing, transport] = [optKobo(f.basic), optKobo(f.housing), optKobo(f.transport)];
  if ([basic, housing, transport].some((n) => Number.isNaN(n))) return "Basic, housing and transport are amounts, like 150,000.";
  if (basic + housing + transport > gross) return "Basic, housing and transport can't add up to more than the monthly pay.";
  return { gross, basic, housing, transport };
}

/** What is missing or wrong in the Add someone form, or "". */
export function addProblem(f: AddForm): string {
  if (!f.first.trim() || !f.last.trim()) return "Give their first and last names.";
  if (!f.phone.trim() && !f.email.trim()) return "Give a phone number or an email, for their payslip.";
  if (!f.start) return "Give the date they start.";
  const pay = payOf(f);
  if (typeof pay === "string") return pay;
  if (!f.bankCode || !isAccountNumber(f.account)) return "Choose their bank and type the 10-digit account number.";
  return "";
}

/** The POST /v1/payroll/team body. */
export function addBody(f: AddForm, key: string): Record<string, unknown> {
  const pay = payOf(f);
  if (typeof pay === "string") throw new Error(pay);
  const body: Record<string, unknown> = {
    key,
    first_name: f.first.trim(),
    last_name: f.last.trim(),
    start_date: f.start,
    pay: { gross_kobo: pay.gross, basic_kobo: pay.basic, housing_kobo: pay.housing, transport_kobo: pay.transport, pensionable: f.pensionable },
    bank: { bank_code: f.bankCode, account_number: accountDigits(f.account) },
  };
  if (f.phone.trim()) body.phone = f.phone.trim();
  if (f.email.trim()) body.email = f.email.trim();
  if (f.department.trim()) body.department = f.department.trim();
  if (f.staffUser) body.staff_user = f.staffUser;
  return body;
}

/** The Change form, as typed. */
export interface ChangeForm {
  first: string;
  last: string;
  email: string;
  department: string;
  gross: string;
  from: string;
}

export function changeFormOf(m: TeamMember, now = new Date()): ChangeForm {
  return {
    first: m.first_name,
    last: m.last_name,
    email: m.email ?? "",
    department: m.department ?? "",
    gross: m.pay ? String(m.pay.gross_kobo / 100) : "",
    from: today(now),
  };
}

/** The PATCH body: only what changed (never a phone or a job title);
 * null when nothing did. */
export function changeBody(m: TeamMember, f: ChangeForm, key: string): Record<string, unknown> | null | string {
  const body: Record<string, unknown> = { key };
  if (f.first.trim() !== m.first_name) body.first_name = f.first.trim();
  if (f.last.trim() !== m.last_name) body.last_name = f.last.trim();
  if (f.email.trim() !== (m.email ?? "")) body.email = f.email.trim();
  if (f.department.trim() !== (m.department ?? "")) body.department = f.department.trim();
  if (f.gross.trim()) {
    const gross = nairaKobo(f.gross);
    if (!(gross > 0)) return "Give their monthly pay.";
    if (!m.pay || gross !== m.pay.gross_kobo) {
      // A new monthly pay: the parts Payrolla had are not kept beside it.
      body.pay = { gross_kobo: gross, from: f.from || today() };
    }
  }
  return Object.keys(body).length > 1 ? body : null;
}

/** A pay change's start, in words: "from 1 January 2027". */
export function fromWords(d: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(d);
  if (!m) return "";
  const months = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
  return `from ${Number(m[3])} ${months[Number(m[2]) - 1]} ${m[1]}`;
}

/** The refusals after which only a new code helps. */
export const NEW_CODE = new Set(["code_expired", "code_locked", "code_used"]);
