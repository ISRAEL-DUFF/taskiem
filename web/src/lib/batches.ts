// Paying several people (P3; docs/spec/chat-pay-people.md, "Pay several
// people"): the list being filled (picked saved accounts, riders with what
// is due, staff with an account linked, a pasted list read a line at a time), each row as the body of a
// payment (POST /v1/payment-batches takes what POST /v1/payments does),
// and the words a batch shows before it goes and as each person is paid.

import { naira } from "./invoices";
import { amountTyped } from "./paymessage";
import { payAccountWords, type StaffPayAccount } from "./team";
import {
  accountDigits,
  payAmountKobo,
  paymentStatus,
  purposeOf,
  type Payment,
} from "./payments";

/** Up to this many people a batch (the server's limit). */
export const MAX_ROWS = 100;

/** A row held out when the batch was made: which row, who, and why. */
export interface BatchHeld {
  row: number;
  payee: string;
  code: string;
  problem: string;
}

/** What became of a row when the batch was confirmed or approved. */
export interface BatchResult {
  payment: string;
  code?: string;
  problem?: string;
}

export interface PaymentBatch {
  id: string;
  title: string;
  kind: "list" | "riders" | "messages";
  /** preparing while its rows are checked; prepared once all are in. */
  status: "preparing" | "prepared" | "sent" | "cancelled";
  created_by: string;
  created_at: string;
  confirmed_at: string | null;
  /** Each row; empty in the list of batches (open one to see them). */
  payments: Payment[];
  total_kobo: number;
  count: number;
  held?: BatchHeld[];
  results?: BatchResult[];
  paid_kobo: number;
  waiting: number;
  failed: number;
}

/** One line of a pasted list, as POST /v1/payment-batches/read reads it. */
export interface BatchLine {
  line: number;
  text: string;
  account_number?: string;
  bank_name?: string;
  amount_kobo?: number;
  name?: string;
  /** The business's saved account with that number, by name. */
  payee?: string;
  problem?: string;
}

/** A row of the list being filled: a saved account, a rider, a staff
 * member, or an account number (pasted). Amounts and "what for" are as typed. */
export interface DraftRow {
  key: string;
  kind: "saved" | "rider" | "staff" | "account";
  /** Who, as the row is titled. */
  who: string;
  /** The line under it: the bank and account as they may be shown. */
  sub?: string;
  saved_id?: string;
  /** A saved account by its name (a pasted line the business has saved). */
  payee?: string;
  rider_id?: string;
  /** A staff member, paid to the saved account linked to them. */
  staff_id?: string;
  number?: string;
  bank?: string;
  amount: string;
  for: string;
  /** Why it cannot go as it is: read from the list, or held out. */
  problem?: string;
}

/** The choices of "What for", as the Pay sheet words them. */
export const FOR_CHOICES = ["a bill", "wages", "a delivery", "a refund", "stock bought", "other"];

let seq = 0;
const nextKey = () => `r${++seq}`;

/** A saved bank account as a row. */
export function rowFromSaved(x: { id: string; name: string; title: string; hint: string; check?: { account_name: string | null } }): DraftRow {
  return {
    key: nextKey(),
    kind: "saved",
    who: x.check?.account_name || x.title || `@${x.name}`,
    sub: x.hint ? `@${x.name} · ${x.hint}` : `@${x.name}`,
    saved_id: x.id,
    amount: "",
    for: "",
  };
}

/** A rider's line on Riders > Pay as a row: what is due to them, for a
 * delivery. A rider with no bank account kept cannot be a row. */
export function rowFromRider(l: { rider_id: string; rider: string; net_kobo: number; bank: string }): DraftRow {
  return {
    key: nextKey(),
    kind: "rider",
    who: l.rider,
    sub: l.bank,
    rider_id: l.rider_id,
    amount: amountTyped(l.net_kobo),
    for: "a delivery",
  };
}

/** A staff member with an account linked as a row: for wages, the amount
 * to type. The account is masked as their page shows it. */
export function rowFromStaff(p: { user_id: string; name: string; email?: string; pay_account: StaffPayAccount }): DraftRow {
  return {
    key: nextKey(),
    kind: "staff",
    who: p.name || p.email || "A staff member",
    sub: payAccountWords(p.pay_account),
    staff_id: p.user_id,
    amount: "",
    for: "wages",
  };
}

/** A pasted line as a row: a saved account when the business has that
 * number saved, else the number and bank read; what could not be read
 * is said, to fix in the row. */
export function rowFromLine(l: BatchLine): DraftRow {
  const amount = amountTyped(l.amount_kobo);
  // Saved already: paid as the saved account (its bank is known).
  if (l.payee)
    return { key: nextKey(), kind: "saved", who: l.name || `@${l.payee}`, sub: l.bank_name ? `@${l.payee} · ${l.bank_name}` : `@${l.payee}`, payee: l.payee, amount, for: "" };
  return {
    key: nextKey(),
    kind: "account",
    who: l.name || `Line ${l.line}`,
    sub: l.problem ? `As pasted: ${l.text}` : undefined,
    number: l.account_number ?? "",
    bank: l.bank_name ?? "",
    amount,
    for: "",
    problem: l.problem,
  };
}

/** The same person twice in a list: the second is not added again. */
export function sameRow(a: DraftRow, b: DraftRow): boolean {
  if (a.saved_id && a.saved_id === b.saved_id) return true;
  if (a.rider_id && a.rider_id === b.rider_id) return true;
  if (a.staff_id && a.staff_id === b.staff_id) return true;
  if (a.payee && a.payee === b.payee) return true;
  return false;
}

/** Rows added to a list, leaving out those already in it, and never more
 * than MAX_ROWS. */
export function addRows(list: DraftRow[], more: DraftRow[]): { rows: DraftRow[]; left: number } {
  const out = [...list];
  let left = 0;
  for (const r of more) {
    if (out.some((x) => sameRow(x, r))) continue;
    if (out.length >= MAX_ROWS) {
      left++;
      continue;
    }
    out.push(r);
  }
  return { rows: out, left };
}

/** What is wrong with a row as it is, or null. */
export function rowProblem(r: DraftRow): string | null {
  if (r.kind === "account") {
    if (!/^\d{10}$/.test(accountDigits(r.number ?? ""))) return "Type the account number: 10 digits.";
    if (!(r.bank ?? "").trim()) return "Which bank is it with?";
  }
  const k = payAmountKobo(r.amount);
  if (k === undefined) return "How much?";
  if (Number.isNaN(k)) return "Type an amount, like 45,000 or 45k.";
  if (k < 100) return "Pay at least ₦1.";
  if (!r.for.trim()) return "What is it for?";
  return null;
}

/** A row as one payment's body in the batch. */
export function rowBody(r: DraftRow): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  if (r.kind === "rider") body.rider_id = r.rider_id;
  else if (r.kind === "staff") body.staff_id = r.staff_id;
  else if (r.kind === "account") {
    body.account_number = accountDigits(r.number ?? "");
    body.bank_name = (r.bank ?? "").trim();
  } else if (r.saved_id) body.saved_id = r.saved_id;
  else body.payee = r.payee;
  const k = payAmountKobo(r.amount);
  if (k !== undefined && !Number.isNaN(k)) body.amount_kobo = k;
  const purpose = purposeOf(r.for);
  body.purpose = purpose ?? "other";
  if (!purpose && r.for.trim()) body.purpose_note = r.for.trim().slice(0, 80);
  return body;
}

/** "1 person", "3 people". */
export const peopleWords = (n: number) => (n === 1 ? "1 person" : `${n} people`);

/** The list's total, of the rows whose amount reads. */
export function draftTotal(rows: DraftRow[]): number {
  return rows.reduce((t, r) => {
    const k = payAmountKobo(r.amount);
    return k && !Number.isNaN(k) ? t + k : t;
  }, 0);
}

/** The foot of the list: "3 people · ₦135,000". */
export const footWords = (count: number, totalKobo: number) => `${peopleWords(count)} · ${naira(totalKobo)}`;

/** The rows held out, mapped back to the list as their problems. */
export function heldOnto(rows: DraftRow[], held: BatchHeld[] = []): DraftRow[] {
  return rows.map((r, i) => {
    const h = held.find((x) => x.row === i);
    return h ? { ...r, problem: h.problem } : { ...r, problem: undefined };
  });
}

/** How the rows stand, counted. */
export function batchCounts(b: Pick<PaymentBatch, "payments">) {
  const c = { paid: 0, sending: 0, waiting: 0, failed: 0, prepared: 0, cancelled: 0 };
  for (const x of b.payments) {
    if (x.status === "paid") c.paid++;
    else if (x.status === "sending" || x.status === "unknown") c.sending++;
    else if (x.status === "waiting_ok") c.waiting++;
    else if (x.status === "failed" || x.status === "rejected") c.failed++;
    else if (x.status === "prepared") c.prepared++;
    else c.cancelled++;
  }
  return c;
}

/** How the rows stand in words: "3 paid · 1 sending · 1 not paid". */
export function progressWords(b: Pick<PaymentBatch, "payments">): string {
  const c = batchCounts(b);
  const parts = [
    c.paid && `${c.paid} paid`,
    c.sending && `${c.sending} sending`,
    c.waiting && `${c.waiting} waiting for the owner's OK`,
    c.failed && `${c.failed} not paid`,
    c.prepared && `${c.prepared} to confirm`,
  ].filter(Boolean);
  return parts.length ? parts.join(" · ") : "Nothing to pay";
}

/** A batch's status in a word or two, with its tone. */
export function batchTag(b: Pick<PaymentBatch, "status" | "payments">): { label: string; tone: "ok" | "warn" | "danger" | "info" | "muted" } {
  if (b.status === "cancelled") return { label: "Cancelled", tone: "muted" };
  if (b.status === "preparing") return { label: "Getting ready", tone: "info" };
  const c = batchCounts(b);
  if (b.status === "prepared" && c.prepared > 0) return { label: "To confirm", tone: "warn" };
  if (c.sending > 0) return { label: "Sending", tone: "info" };
  if (c.waiting > 0) return { label: "Waiting for an OK", tone: "warn" };
  if (c.failed > 0) return { label: c.paid > 0 ? "Some not paid" : "Not paid", tone: "danger" };
  if (c.paid > 0) return { label: "All paid", tone: "ok" };
  return { label: "Cancelled", tone: "muted" };
}

/** How soon to read the batch again: soon while money is moving, less
 * often while rows wait for the owner, never once all have ended. */
export function pollAfter(b: Pick<PaymentBatch, "status" | "payments">): number | null {
  if (b.status === "preparing") return 2000;
  const c = batchCounts(b);
  if (c.sending > 0) return 3000;
  if (c.waiting > 0) return 10000;
  return null;
}

/** A batch's name: its title, else what kind of list it is. */
export function batchName(b: Pick<PaymentBatch, "title" | "kind">): string {
  if (b.title.trim()) return b.title.trim();
  return b.kind === "riders" ? "Riders' pay" : b.kind === "messages" ? "From messages" : "A list of people";
}

/** A batch in the list of batches, which carries its counts but not its
 * rows: its status in a word or two, with its tone. */
export function listTag(b: Pick<PaymentBatch, "status" | "waiting" | "failed" | "paid_kobo" | "total_kobo">): { label: string; tone: "ok" | "warn" | "danger" | "info" | "muted" } {
  switch (b.status) {
    case "cancelled":
      return { label: "Cancelled", tone: "muted" };
    case "preparing":
      return { label: "Getting ready", tone: "info" };
    case "prepared":
      return { label: "To confirm", tone: "warn" };
  }
  if (b.waiting > 0) return { label: "Waiting for an OK", tone: "warn" };
  if (b.failed > 0) return { label: b.paid_kobo > 0 ? "Some not paid" : "Not paid", tone: "danger" };
  if (b.paid_kobo >= b.total_kobo && b.total_kobo > 0) return { label: "All paid", tone: "ok" };
  return { label: "Sending", tone: "info" };
}

/** The line under a batch in the list: "₦386,000 paid · 1 not paid". */
export function listWords(b: Pick<PaymentBatch, "status" | "waiting" | "failed" | "paid_kobo">): string {
  if (b.status !== "sent") return "";
  return [`${naira(b.paid_kobo)} paid`, b.waiting && `${b.waiting} waiting for an OK`, b.failed && `${b.failed} not paid`].filter(Boolean).join(" · ");
}

/** The ids a confirm is for: the rows prepared, as the person sees them. */
export const preparedIds = (b: Pick<PaymentBatch, "payments">) => b.payments.filter((x) => x.status === "prepared").map((x) => x.id);

/** The ids an OK is for: the rows waiting, as the owner sees them. */
export const waitingIds = (b: Pick<PaymentBatch, "payments">) => b.payments.filter((x) => x.status === "waiting_ok").map((x) => x.id);

/** Each row's result by its payment, for the words under it. */
export function resultsBy(results: BatchResult[] = []): Map<string, BatchResult> {
  return new Map(results.filter((r) => r.code).map((r) => [r.payment, r]));
}

/** Rows the checks asked about again (the same payment in the last day):
 * they stay to confirm, and go with a yes for each. */
export const againIds = (results: BatchResult[] = []) => results.filter((r) => r.code === "pay_again").map((r) => r.payment);

/** The confirming button: "Pay 3 people ₦135,000". */
export function confirmWords(b: Pick<PaymentBatch, "payments">): string {
  const left = b.payments.filter((x) => x.status === "prepared");
  return `Pay ${peopleWords(left.length)} ${naira(left.reduce((t, x) => t + x.amount_kobo, 0))}`;
}

/** A row's status pill, as one payment's. */
export const rowStatus = (x: Payment) => paymentStatus(x.status);
