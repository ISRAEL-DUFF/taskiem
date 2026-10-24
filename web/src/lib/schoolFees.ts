// Fees and term invoices (decision 0046, B3; docs/schools.md#fees): the
// API's shapes for a class's fee structure, a student's optional items and
// standing discounts, invoicing a term and its invoices, and the logic the
// pages share, kept here so it is tested.

import { koboInput, naira, toKobo, type InvoiceLine, type InvoiceStatus } from "./invoices";
import type { SchoolTerm } from "./schools";

export type FeeKind = "required" | "optional";

export interface FeeItem {
  id: string;
  term_id: string;
  class_id: string;
  name: string;
  amount_kobo: number;
  kind: FeeKind;
  pass_through_for?: string;
  position: number;
}

export interface ClassFees {
  class_id: string;
  class: string;
  items: FeeItem[];
  required_kobo: number;
  pass_through_kobo: number;
}

export interface FeesSet {
  fees: ClassFees;
  drafts_updated: number;
}

export type DiscountKind = "sibling" | "staff_child" | "scholarship" | "hardship" | "other";

export const DISCOUNT_KINDS: { value: DiscountKind; label: string }[] = [
  { value: "sibling", label: "Sibling" },
  { value: "staff_child", label: "Staff child" },
  { value: "scholarship", label: "Scholarship" },
  { value: "hardship", label: "Hardship" },
  { value: "other", label: "Other" },
];

export interface Discount {
  id: string;
  student_id: string;
  kind: DiscountKind;
  percent?: number;
  amount_kobo?: number;
  reason: string;
  created_by: string;
  created_at: string;
  ended_at?: string;
}

export interface PlanStudent {
  id: string;
  admission_number: string;
  name: string;
  class: string;
  total_kobo?: number;
}

export interface TermPlan {
  term: SchoolTerm;
  enrolled: number;
  to_invoice: number;
  total_kobo: number;
  pass_through_kobo: number;
  classes: { class_id: string; class: string; students: number; total_kobo: number }[];
  already_invoiced: number;
  no_contact: PlanStudent[];
  no_fees: PlanStudent[];
  nothing_to_pay: PlanStudent[];
  needs_approval: PlanStudent[];
  hash: string;
}

export interface TermInvoiced extends TermPlan {
  created: number;
  send: boolean;
  sent: number;
  failed: number;
}

export interface TermInvoice {
  invoice_id: string;
  number: string;
  status: InvoiceStatus;
  term_id: string;
  term: string;
  student_id: string;
  admission_number: string;
  name: string;
  class_id: string;
  class: string;
  total_kobo: number;
  paid_kobo: number;
  due_kobo: number;
  sent_at: string | null;
}

export interface StudentFees {
  student_id: string;
  term_id: string;
  class_id: string;
  items: FeeItem[];
  chosen: string[];
  discounts: Discount[];
  lines: InvoiceLine[];
  total_kobo: number;
  invoice?: TermInvoice;
}

// --- the structure form ---

/** One fee item as typed: the amount in naira. */
export interface FeeRow {
  name: string;
  amount: string;
  kind: FeeKind;
  passThroughFor: string;
}

export function emptyFeeRow(): FeeRow {
  return { name: "", amount: "", kind: "required", passThroughFor: "" };
}

export function feeRows(items: FeeItem[]): FeeRow[] {
  return items.map((it) => ({ name: it.name, amount: koboInput(it.amount_kobo), kind: it.kind, passThroughFor: it.pass_through_for ?? "" }));
}

/** Why the structure would be refused here, or null. Empty rows are left out. */
export function feeRowsProblem(rows: FeeRow[]): string | null {
  const seen = new Set<string>();
  const kept = rows.filter((r) => r.name.trim() || r.amount.trim());
  if (kept.length > 40) return "A class has at most 40 fee items.";
  for (const [i, r] of kept.entries()) {
    const name = r.name.trim();
    if (!name) return `Item ${i + 1} needs a name, such as Tuition.`;
    if (name.length > 80) return `${name}: a name is at most 80 characters.`;
    if (seen.has(name.toLowerCase())) return `The class has ${name} twice.`;
    seen.add(name.toLowerCase());
    const k = toKobo(r.amount);
    if (k === undefined || Number.isNaN(k) || k < 100) return `${name} needs an amount, at least ₦1.`;
    if (r.passThroughFor.trim().length > 60) return `${name}: who it is collected for is at most 60 characters.`;
  }
  return null;
}

/** The PUT /v1/schools/terms/{id}/fees/{class} body. */
export function feeRowsRequest(rows: FeeRow[]): { items: { name: string; amount_kobo: number; kind: FeeKind; pass_through_for?: string }[] } {
  return {
    items: rows
      .filter((r) => r.name.trim() || r.amount.trim())
      .map((r) => {
        const it: { name: string; amount_kobo: number; kind: FeeKind; pass_through_for?: string } = { name: r.name.trim(), amount_kobo: toKobo(r.amount) ?? 0, kind: r.kind };
        if (r.passThroughFor.trim()) it.pass_through_for = r.passThroughFor.trim();
        return it;
      }),
  };
}

/** What every student of the class pays before optional items and discounts, and of it what is collected for others. */
export function feeRowsTotals(rows: FeeRow[]): { required: number; passThrough: number; optional: number } {
  let required = 0;
  let passThrough = 0;
  let optional = 0;
  for (const r of rows) {
    const k = toKobo(r.amount);
    if (!k || Number.isNaN(k)) continue;
    if (r.kind === "optional") optional += k;
    else {
      required += k;
      if (r.passThroughFor.trim()) passThrough += k;
    }
  }
  return { required, passThrough, optional };
}

export function savedFeesLine(s: FeesSet): string {
  const base = `Saved ${s.fees.class}'s fees: ${naira(s.fees.required_kobo)} for every student.`;
  if (!s.drafts_updated) return base;
  return `${base} ${s.drafts_updated} draft ${s.drafts_updated === 1 ? "invoice follows" : "invoices follow"} the change; sent invoices change only by amendment.`;
}

// --- discounts ---

export interface DiscountForm {
  kind: DiscountKind;
  by: "percent" | "amount";
  value: string;
  reason: string;
}

export function emptyDiscount(): DiscountForm {
  return { kind: "sibling", by: "percent", value: "", reason: "" };
}

export function discountProblem(f: DiscountForm): string | null {
  if (f.by === "percent") {
    const n = Number(f.value.trim());
    if (!Number.isInteger(n) || n < 1 || n > 100) return "A percent is a whole number from 1 to 100.";
  } else {
    const k = toKobo(f.value);
    if (k === undefined || Number.isNaN(k) || k < 100) return "Give the amount, at least ₦1.";
  }
  if (!f.reason.trim()) return "Say why the discount is given.";
  if (f.reason.trim().length > 200) return "The reason is at most 200 characters.";
  return null;
}

export function discountRequest(f: DiscountForm): Record<string, unknown> {
  const body: Record<string, unknown> = { kind: f.kind, reason: f.reason.trim() };
  if (f.by === "percent") body.percent = Number(f.value.trim());
  else body.amount_kobo = toKobo(f.value);
  return body;
}

/** A discount in words: "Sibling, 10% of the school's own fees". */
export function discountWords(d: Pick<Discount, "kind" | "percent" | "amount_kobo">): string {
  const kind = DISCOUNT_KINDS.find((k) => k.value === d.kind)?.label ?? d.kind;
  if (d.percent) return `${kind}, ${d.percent}% of the school's own fees`;
  return `${kind}, ${naira(d.amount_kobo ?? 0)} off`;
}

/** A refusal that waits for a second person (409 needs_approval): its words, else null. */
export function needsApproval(status: number, body: Record<string, unknown> | undefined): string | null {
  if (status !== 409 || body?.code !== "needs_approval") return null;
  return "This discount is at or above the school's discount threshold, so it needs a second person to approve it. Approvals are coming; until then give a smaller discount, or ask the owner to change the threshold.";
}

// --- invoicing the term ---

/** The plan in sentences, for the preview. */
export function planSummary(p: TermPlan): string[] {
  const out: string[] = [];
  if (p.to_invoice === 0) out.push(p.already_invoiced > 0 ? "Every student who can be invoiced has their invoice for the term." : "Nobody can be invoiced yet.");
  else {
    out.push(`${p.to_invoice} ${p.to_invoice === 1 ? "student" : "students"} to invoice, ${naira(p.total_kobo)} in all.`);
    if (p.pass_through_kobo) out.push(`${naira(p.pass_through_kobo)} of it is collected for others (such as exam bodies), not the school's own.`);
  }
  if (p.already_invoiced) out.push(`${p.already_invoiced} already invoiced for the term: they are not invoiced again.`);
  const left = (n: number, why: string) => n && out.push(`${n} left out: ${why}.`);
  left(p.no_contact.length, "no guardian to send it to (add one on the Families page)");
  left(p.no_fees.length, "their class has no fees for the term");
  left(p.nothing_to_pay.length, "nothing to pay after discounts");
  left(p.needs_approval.length, "a standing discount waits for a second person");
  return out;
}

export function invoicedLine(r: TermInvoiced): string {
  if (r.created === 0 && !r.send) return "Nobody new to invoice: every student has their invoice for the term.";
  if (r.created === 0) return `Nobody new to invoice; ${r.sent} ${r.sent === 1 ? "draft" : "drafts"} sent${r.failed ? `, ${r.failed} could not be` : ""}.`;
  const made = `${r.created} ${r.created === 1 ? "invoice" : "invoices"} made`;
  if (!r.send) return `${made} as drafts: check them, then send them from here.`;
  const failed = r.failed ? `; ${r.failed} could not be sent` : "";
  return `${made}; ${r.sent} sent${failed}.`;
}

/** The term invoices' totals: billed, paid, still due. */
export function termTotals(list: Pick<TermInvoice, "total_kobo" | "paid_kobo" | "due_kobo" | "status">[]): { total: number; paid: number; due: number } {
  let total = 0;
  let paid = 0;
  let due = 0;
  for (const x of list) {
    if (x.status === "void") continue;
    total += x.total_kobo;
    paid += x.paid_kobo;
    due += x.due_kobo;
  }
  return { total, paid, due };
}
