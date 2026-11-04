import { naira } from "./invoices";

// Cash at the desk, approvals and receipts (docs/spec/schools.md 8 and 9,
// B5; docs/schools.md#cash-and-transfers, #receipts): the words and
// checks the Payments, Receipts and Approvals pages use, kept here so
// they can be tested.

export type PostingKind = "cash_posting" | "cash_match" | "discount" | "discount_amendment" | "term_invoice";
export type PostingStatus = "pending" | "posted" | "declined";
export type PaymentHow = "cash" | "transfer" | "pos" | "other";

export interface PostedPart {
  invoice_id: string;
  number: string;
  amount_kobo: number;
  receipt_id?: string;
  receipt_number?: string;
}

export interface PostingResult {
  entry_id?: string;
  parts?: PostedPart[];
  credit_kobo?: number;
  discount_id?: string;
  amendment_id?: string;
  invoice_id?: string;
  invoice_number?: string;
}

/** A cash posting, or a discount, amendment or held term invoice waiting for a second person (GET /v1/schools/payments, /v1/schools/approvals). */
export interface Posting {
  id: string;
  kind: PostingKind;
  status: PostingStatus;
  student_id?: string;
  admission_number?: string;
  name?: string;
  class?: string;
  invoice_id?: string;
  invoice_number?: string;
  term_id?: string;
  amount_kobo: number;
  details: Record<string, unknown>;
  requested_by: string;
  requested_email?: string;
  requested_at: string;
  decided_by?: string;
  decided_email?: string;
  decided_at?: string;
  decision_reason?: string;
  result?: PostingResult;
  yours?: boolean;
}

/** A receipt (GET /v1/schools/receipts). */
export interface Receipt {
  id: string;
  number: string;
  term_id: string;
  term: string;
  student_id: string;
  admission_number: string;
  name: string;
  class_id: string;
  class: string;
  invoice_id: string;
  invoice_number: string;
  part_id: string;
  amount_kobo: number;
  balance_after_kobo: number;
  how: string;
  paid_at: string;
  issued_by: string;
  issued_at: string;
  voided: boolean;
  voided_at?: string;
  void_reason?: string;
  link?: string;
}

/** What the server answers when a request waits for a second person (202). */
export interface ApprovalNeeded {
  code: "needs_approval";
  error: string;
  request: Posting;
}

export const HOWS: { value: PaymentHow; label: string }[] = [
  { value: "cash", label: "Cash" },
  { value: "transfer", label: "Transfer to the school's account" },
  { value: "pos", label: "POS" },
  { value: "other", label: "Another way" },
];

export const howWords = (how: string): string => HOWS.find((h) => h.value === how)?.label ?? how;

export const KIND_WORDS: Record<PostingKind, string> = {
  cash_posting: "Cash posting",
  cash_match: "Money entered by hand, matched to an invoice",
  discount: "Standing discount",
  discount_amendment: "Discount on an invoice",
  term_invoice: "Term invoice with a discount",
};

export const kindWords = (kind: string): string => KIND_WORDS[kind as PostingKind] ?? kind;

export function postingStatus(s: PostingStatus): { label: string; tone: string } {
  switch (s) {
    case "pending":
      return { label: "Waiting for a second person", tone: "pending" };
    case "posted":
      return { label: "Posted", tone: "ok" };
    case "declined":
      return { label: "Declined", tone: "cancelled" };
  }
}

/** The form for posting money at the desk. */
export interface PaymentForm {
  student: string;
  amount: string;
  how: PaymentHow;
  date: string;
  note: string;
}

export const emptyPayment = (student = ""): PaymentForm => ({ student, amount: "", how: "cash", date: "", note: "" });

/** The amount typed, in kobo; NaN when it is not an amount. */
export function amountKobo(s: string): number {
  const t = s.replace(/[,₦\s]/g, "");
  if (!/^\d+(\.\d{1,2})?$/.test(t)) return NaN;
  return Math.round(Number(t) * 100);
}

/** Why the form cannot be sent, or null. */
export function paymentProblem(f: PaymentForm): string | null {
  if (!f.student.trim()) return "Say which student paid (their admission number).";
  const k = amountKobo(f.amount);
  if (Number.isNaN(k) || k < 100) return "The amount is in naira, at least ₦1.";
  if (f.date && !/^\d{4}-\d{2}-\d{2}$/.test(f.date)) return "The date is written 2026-10-23.";
  if (f.note.length > 200) return "The note is up to 200 characters.";
  return null;
}

export function paymentRequest(f: PaymentForm): Record<string, unknown> {
  const out: Record<string, unknown> = { student: f.student.trim(), amount_kobo: amountKobo(f.amount), how: f.how };
  if (f.date) out.date = f.date;
  if (f.note.trim()) out.note = f.note.trim();
  return out;
}

/** Whether an answer is a request recorded for a second person. */
export function isApprovalNeeded(v: unknown): v is ApprovalNeeded {
  return typeof v === "object" && v !== null && (v as { code?: string }).code === "needs_approval" && typeof (v as { request?: unknown }).request === "object";
}

/** What a match answered when it waits for a second person (202): the sentence to show, else null. */
export function heldMatchLine(v: unknown): string | null {
  if (!isApprovalNeeded(v)) return null;
  return `Not matched yet: this money was entered by hand and ${naira(v.request.amount_kobo)} is at or above the school's cash threshold, so the match waits for a second person to approve it under Schools, Approvals.`;
}

/** What posting did, in a sentence. */
export function postedLine(p: Posting): string {
  const r = p.result;
  if (!r) return `${naira(p.amount_kobo)} recorded.`;
  const parts = r.parts ?? [];
  const bits: string[] = [];
  const one = parts[0];
  if (parts.length === 1 && one) bits.push(`${naira(one.amount_kobo)} towards ${one.number}${one.receipt_number ? ` (receipt ${one.receipt_number})` : ""}`);
  else if (parts.length > 1) bits.push(`${parts.length} invoices paid: ${parts.map((x) => `${x.number} ${naira(x.amount_kobo)}`).join(", ")}`);
  if (r.credit_kobo) bits.push(`${naira(r.credit_kobo)} kept as the family's credit`);
  if (bits.length === 0) return `${naira(p.amount_kobo)} recorded; nothing was open to count it towards, so it is the family's credit.`;
  return `${naira(p.amount_kobo)} posted: ${bits.join("; ")}.`;
}

/** A request's first line: what it asks for and for whom. */
export function requestTitle(p: Posting): string {
  const who = p.admission_number ? ` for ${p.admission_number}${p.class ? ` (${p.class})` : ""}` : p.invoice_number ? ` on ${p.invoice_number}` : "";
  return `${kindWords(p.kind)} of ${naira(p.amount_kobo)}${who}`;
}

/** The details a second person sees before deciding. */
export function requestDetails(p: Posting): string[] {
  const d = p.details ?? {};
  const out: string[] = [];
  switch (p.kind) {
    case "cash_posting":
      out.push(`Paid ${howWords(String(d.how ?? "")).toLowerCase()}${d.date ? ` on ${String(d.date)}` : ""}.`);
      if (d.note) out.push(`Note: ${String(d.note)}`);
      break;
    case "cash_match":
      out.push(`Money entered by hand on Money, matched to invoice ${p.invoice_number ?? ""}. It counts towards the invoice once approved.`);
      break;
    case "discount": {
      const kind = String(d.kind ?? "").replace(/_/g, " ");
      const as = d.percent !== undefined ? `${String(d.percent)}% of the school's own fees` : "an amount";
      out.push(`A ${kind} discount, ${as}: ${String(d.reason ?? "")}`);
      break;
    }
    case "discount_amendment": {
      const removal = (d.amendment as { kind?: string } | undefined)?.kind === "remove";
      out.push(`${removal ? "A line taken out of" : "A discount on"} invoice ${p.invoice_number ?? ""}: ${String(d.reason ?? "")}`);
      break;
    }
    case "term_invoice":
      out.push(`The student's term invoice carries a discount at or above the school's threshold${d.send ? "; it is sent on approval" : "; it is kept as a draft on approval"}.`);
      break;
  }
  const by = p.requested_email || p.requested_by;
  out.push(`Asked by ${by}.`);
  return out;
}

/** What a decided request did, for the Payments list. */
export function decisionLine(p: Posting): string {
  const by = p.decided_email || p.decided_by || "";
  if (p.status === "declined") return `Declined${by ? ` by ${by}` : ""}${p.decision_reason ? `: ${p.decision_reason}` : "."}`;
  if (p.status === "posted" && by) return `Approved by ${by}.`;
  return "";
}

/** A receipt's state in words. */
export function receiptStatus(r: Receipt): { label: string; tone: string } {
  return r.voided ? { label: "Voided", tone: "cancelled" } : { label: "Valid", tone: "ok" };
}

export const receiptPDF = (r: { id: string }) => `/v1/schools/receipts/${encodeURIComponent(r.id)}/pdf`;

/** How a receipt's payment was counted, for people. */
export function receiptHow(how: string): string {
  switch (how) {
    case "exact":
      return "Paid into the student's account";
    case "suggested":
    case "suggested_auto":
      return "Matched to the invoice";
    case "manual":
      return "Received by the school";
  }
  return how;
}

/** A receipt number as typed, trimmed and upper-cased; empty when nothing is typed. */
export const receiptQuery = (s: string): string => s.trim().toUpperCase();
