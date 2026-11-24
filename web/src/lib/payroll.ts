// Payroll in Taskiem, on Payrolla (P4a): Payrolla works out, approves and
// pays the payroll; these are the words Taskiem's own screens show it in.
// Nothing here moves money.

export type PayrollTone = "ok" | "warn" | "danger" | "info" | "muted";

/** A run as Payrolla sums it up. */
export interface PayrollRun {
  run_id: string;
  period_start: string;
  period_end: string;
  pay_date: string;
  run_type: string;
  status: string;
  employee_count: number;
  total_gross_kobo: number;
  total_net_kobo: number;
  awaiting_approval: boolean;
}

/** GET /v1/payroll. */
export interface PayrollList {
  connected: boolean;
  /** The Payrolla connection ended (revoked in Payrolla): owners reconnect. */
  ended?: boolean;
  is_owner?: boolean;
  /** Connected in one step, or with a pasted key (whose owner reconnects). */
  auth?: "oauth" | "key";
  connection?: string;
  test?: boolean;
  company?: { name: string; pay_frequency: string };
  runs: PayrollRun[];
  next_cursor?: string;
}

export type LineState = "paid" | "waiting" | "failed" | "returned";

export interface PayrollLine {
  payslip_id: string;
  employee_id: string;
  name: string;
  net_kobo: number;
  status: string;
  state: LineState;
  reference?: string;
  reason?: string;
  staff: { user_id: string; name: string } | null;
}

/** GET /v1/payroll/runs/{run}. */
export interface PayrollRunView {
  connection?: string;
  run: { summary: PayrollRun; created_at?: string | null; updated_at?: string | null; approved_at?: string | null; locked_at?: string | null };
  lines: PayrollLine[];
  counts: Record<LineState, number>;
  approval_url?: string;
}

/** Who a staff member is in Payrolla. */
export interface StaffPayrolla {
  employee_id: string;
  name: string;
  linked_by_name?: string;
  linked_at?: string;
}

export interface PayrollStaffMember {
  user_id: string;
  name: string;
  payrolla: StaffPayrolla | null;
  suggested: { employee_id: string; name: string; by: "email" | "phone" } | null;
}

export interface PayrollaEmployee {
  id: string;
  name: string;
  department?: string;
  status: string;
  linked_to: string | null;
}

/** GET /v1/payroll/staff. */
export interface PayrollStaffView {
  connection?: string;
  staff: PayrollStaffMember[];
  employees: PayrollaEmployee[];
}

const RUN_STATUS: Record<string, { label: string; tone: PayrollTone }> = {
  DRAFT: { label: "Draft", tone: "muted" },
  COMPUTED: { label: "Waiting for approval", tone: "warn" },
  APPROVED: { label: "Approved", tone: "info" },
  LOCKED: { label: "Locked", tone: "info" },
  DISBURSING: { label: "Paying", tone: "info" },
  COMPLETED: { label: "Paid", tone: "ok" },
  PARTIALLY_COMPLETED: { label: "Partly paid", tone: "warn" },
  CANCELLED: { label: "Cancelled", tone: "muted" },
  FAILED: { label: "Failed", tone: "danger" },
};

/** A run's status in plain words, with its tone. */
export function runStatus(status: string): { label: string; tone: PayrollTone } {
  return RUN_STATUS[status] ?? { label: "In Payrolla", tone: "muted" };
}

const LINE_STATE: Record<LineState, { label: string; tone: PayrollTone }> = {
  paid: { label: "Paid", tone: "ok" },
  waiting: { label: "Waiting", tone: "warn" },
  failed: { label: "Not paid", tone: "danger" },
  returned: { label: "Returned", tone: "danger" },
};

/** Whether one person's pay arrived, in plain words. */
export function lineState(state: string): { label: string; tone: PayrollTone } {
  return LINE_STATE[state as LineState] ?? LINE_STATE.waiting;
}

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

const ymd = (d: string) => /^(\d{4})-(\d{2})-(\d{2})/.exec(d ?? "");

/** "November 2026" for a run of a whole month (or within one month);
 * "16 Nov to 15 Dec 2026" otherwise. */
export function periodLabel(r: Pick<PayrollRun, "period_start" | "period_end">): string {
  const a = ymd(r.period_start);
  const b = ymd(r.period_end);
  if (!a) return "";
  if (!b || (a[1] === b[1] && a[2] === b[2])) return `${MONTHS[Number(a[2]) - 1]} ${a[1]}`;
  const short = (m: RegExpExecArray) => `${Number(m[3])} ${(MONTHS[Number(m[2]) - 1] ?? "").slice(0, 3)}`;
  return a[1] === b[1] ? `${short(a)} to ${short(b)} ${b[1]}` : `${short(a)} ${a[1]} to ${short(b)} ${b[1]}`;
}

/** "1 person", "12 people". */
export const peopleCount = (n: number) => (n === 1 ? "1 person" : `${n} people`);

const FREQUENCY: Record<string, string> = {
  MONTHLY: "Paid monthly",
  WEEKLY: "Paid weekly",
  BIWEEKLY: "Paid every two weeks",
  FORTNIGHTLY: "Paid every two weeks",
  SEMI_MONTHLY: "Paid twice a month",
};

/** How often the company pays, or "" when Payrolla says something else. */
export const frequencyWords = (f: string | undefined) => FREQUENCY[(f ?? "").toUpperCase()] ?? "";

const ORDER: Record<LineState, number> = { failed: 0, returned: 1, waiting: 2, paid: 3 };

/** The lines with those not paid and returned first, then by name. */
export function sortLines(lines: PayrollLine[]): PayrollLine[] {
  return [...lines].sort((a, b) => (ORDER[a.state] ?? 2) - (ORDER[b.state] ?? 2) || a.name.localeCompare(b.name));
}

/** What happened to each person's pay: "10 paid · 2 not paid". */
export function countsWords(c: Record<LineState, number>): string {
  const parts: string[] = [];
  if (c.paid) parts.push(`${c.paid} paid`);
  if (c.waiting) parts.push(`${c.waiting} waiting`);
  if (c.failed) parts.push(`${c.failed} not paid`);
  if (c.returned) parts.push(`${c.returned} returned`);
  return parts.join(" · ");
}

/** "Looks like Musa Bello in Payrolla (same email)". */
export function suggestionWords(s: NonNullable<PayrollStaffMember["suggested"]>): string {
  return `Looks like ${s.name} in Payrolla (same ${s.by === "phone" ? "phone number" : "email"})`;
}

/** The employees a staff member may be linked to: those not already
 * someone else's, those still employed first. */
export function choosable(employees: PayrollaEmployee[], user: string): PayrollaEmployee[] {
  return employees
    .filter((e) => e.linked_to === null || e.linked_to === user)
    .sort((a, b) => Number(a.status === "TERMINATED") - Number(b.status === "TERMINATED") || a.name.localeCompare(b.name));
}

/** An employee's line in the picker: their department, and whether they left. */
export function employeeSub(e: PayrollaEmployee): string {
  return [e.department ?? "", e.status === "TERMINATED" ? "Has left" : ""].filter(Boolean).join(" · ");
}

/** The PUT /v1/staff/{user}/payrolla body: an employee, or nothing to unlink. */
export const payrollaLinkBody = (employeeId: string | null) => (employeeId ? { employee_id: employeeId } : {});

// --- Connecting in one step and linking approvers (P4b-1) ---

/** GET /v1/payroll/connection. */
export interface PayrollConnection {
  connected: boolean;
  ended?: boolean;
  is_owner: boolean;
  may_link: boolean;
  /** The modes Payrolla can be connected in one step here. */
  modes: string[];
  connection?: {
    id: string;
    name: string;
    status: "active" | "expired";
    auth: "oauth" | "key";
    test: boolean;
    company?: { id?: string; name?: string };
    scopes?: string[];
  };
}

/** A Payrolla user who may approve or pay, and who they are in Taskiem. */
export interface PayrollApprover {
  user_id: string;
  name: string;
  email: string;
  role: string;
  can_approve: boolean;
  can_pay: boolean;
  can_confirm: boolean;
  linked: { user_id: string; name: string; since?: string } | null;
  suggested: boolean;
  me: boolean;
}

/** GET /v1/payroll/approvers. */
export interface PayrollApprovers {
  approvers: PayrollApprover[];
  me: PayrollApprover | null;
  may_link: boolean;
  is_owner: boolean;
}

/** POST /v1/payroll/approvers/{user}/link. */
export interface LinkCodeSent {
  challenge: { challenge_id: string; channel: "TOTP" | "SMS" | "EMAIL"; destination_hint?: string; expires_at: string };
  sent_to: string;
  safer: boolean;
}

/** What the connection is, in the owner's terms. */
export type ConnectionState = "none" | "ended" | "key" | "one_step";

export function connectionState(c: PayrollConnection | undefined): ConnectionState {
  if (!c?.connection) return "none";
  if (c.ended || c.connection.status === "expired") return "ended";
  return c.connection.auth === "key" ? "key" : "one_step";
}

/** Whether Payrolla lets this connection approve and pay (so its
 * approvers can be listed and linked). */
export const canApproveHere = (c: PayrollConnection | undefined) =>
  connectionState(c) === "one_step" && (c?.connection?.scopes ?? []).includes("payroll:approve");

const ROLE: Record<string, string> = { OWNER: "Owner", ADMIN: "Admin", APPROVER: "Approver" };

/** An approver's line: their role and what Payrolla lets them do. */
export function approverSub(a: PayrollApprover): string {
  const can = [a.can_approve ? "approves" : "", a.can_pay ? "pays" : ""].filter(Boolean);
  const parts = [ROLE[a.role] ?? "In Payrolla"];
  if (can.length) {
    const words = can.join(" and ");
    parts.push(words.charAt(0).toUpperCase() + words.slice(1));
  }
  if (!a.can_confirm) parts.push("No phone for codes yet");
  return parts.join(" · ");
}

/** Who an approver is linked to in Taskiem, in words. */
export function linkedWords(a: PayrollApprover): string {
  if (!a.linked) return a.suggested ? "Looks like you (same email)" : "Not linked";
  return a.me ? "Linked to you" : `Linked to ${a.linked.name}`;
}

/** The approvers with this person's own first, then the one that looks
 * like them. */
export function sortApprovers(list: PayrollApprover[]): PayrollApprover[] {
  return [...list].sort((a, b) => Number(b.me) - Number(a.me) || Number(b.suggested) - Number(a.suggested));
}

/** A code as typed: its digits only (spaces allowed), at most six. */
export const codeDigits = (s: string) => s.replace(/\D/g, "").slice(0, 6);

/** The refusals after which only a new code helps. */
export const NEW_CODE_NEEDED = new Set(["code_expired", "code_locked", "code_used"]);

/** An id for one sheet's submission, kept through its retries: the
 * server makes Payrolla's Idempotency-Key from it. */
export function newKey(): string {
  const c = globalThis.crypto;
  if (c && "randomUUID" in c) return c.randomUUID();
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

/** The POST /v1/payroll/connect body: the mode, and Payrolla's sign-up for
 * a business not on Payrolla yet. */
export function connectBody(mode: string, signUp: boolean, rcNumber: string): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  if (mode) body.mode = mode;
  if (signUp) {
    body.sign_up = true;
    if (rcNumber.trim()) body.rc_number = rcNumber.trim();
  }
  return body;
}
