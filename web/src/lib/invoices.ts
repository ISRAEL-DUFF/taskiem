// Invoices a business sends its customers (decision 0045, T3;
// docs/invoices.md; GET/POST /v1/invoices, /v1/invoices/{id}, /send,
// /void, /payments/{payment}/undo, /v1/invoice-settings) and matching money
// to them (T5; docs/matching.md; /v1/money/suggestions, /confirm, /reject,
// /v1/money/entries/{id}/match): the API's shapes, the form a draft is made
// in, and how an invoice and a suggested match read.
// Nothing here marks an invoice paid: only payments counted towards it do.

import { formatPrice } from "./storefront";
import { parseNaira } from "./forms";
import type { Tone } from "./orders";

export type InvoiceStatus = "draft" | "sent" | "part_paid" | "paid" | "overdue" | "void";
export type FeePaidBy = "business" | "customer";

export interface InvoiceCustomer {
  name: string;
  phone?: string;
  email?: string;
  /** The business's own key for the customer ("student:42"): invoices with the same key are one customer's. */
  key?: string;
}

export interface InvoiceLine {
  description: string;
  quantity: number;
  /** Negative for a discount and for an amendment taking a line out. */
  unit_kobo: number;
  /** Who a pass-through line is collected for (WAEC): not the business's income. */
  pass_through_for?: string;
  kind?: "discount" | "removal";
  reason?: string;
  /** The amendment that added the line to a sent invoice. */
  amendment?: string;
}

/** A recorded change to a sent invoice (InvoiceAmendment). */
export interface InvoiceAmendment {
  id: string;
  kind: "add" | "remove" | "discount";
  line: InvoiceLine;
  amount_kobo: number;
  removes?: number;
  reason: string;
  by: string;
  created_at: string;
}

/** A payment counted towards an invoice (InvoicePayment). A row with
 * undoes set is the undoing of another; the undone one has undone_by. */
export interface InvoicePayment {
  id: string;
  amount_kobo: number;
  payment_id: string;
  connection?: string;
  how: "exact" | "suggested" | "suggested_auto" | "manual" | string;
  by: string;
  reasons: string[] | null;
  undoes?: string;
  undone_by?: string;
  created_at: string;
}

/** One invoice (CustomerInvoice). */
export interface CustomerInvoice {
  id: string;
  number: string;
  status: InvoiceStatus;
  customer: InvoiceCustomer;
  lines: InvoiceLine[] | null;
  total_kobo: number;
  paid_kobo: number;
  due_kobo: number;
  currency: string;
  due_on?: string;
  note?: string;
  connection: string;
  fee_paid_by: FeePaidBy;
  allow_part_payments: boolean;
  sent_at: string | null;
  paid_at: string | null;
  voided_at: string | null;
  void_reason?: string;
  created_by: string;
  created_at: string;
  payments?: InvoicePayment[] | null;
  amendments?: InvoiceAmendment[] | null;
}

/** The POST /v1/invoices and PUT /v1/invoices/{id} body (InvoiceDraft). */
export interface InvoiceDraft {
  customer: InvoiceCustomer;
  amount_kobo?: number;
  description?: string;
  lines?: InvoiceLine[];
  due_on?: string;
  note?: string;
  connection?: string;
  fee_paid_by?: FeePaidBy;
  allow_part_payments?: boolean;
}

/** What POST /v1/invoices/{id}/send answers (InvoiceSent). */
export interface InvoiceSent {
  invoice: CustomerInvoice;
  link?: string;
  queued: ("sms" | "email")[] | null;
  sms_over_allowance?: boolean;
  /** How much of the customer's credit this send counted towards the invoice. */
  credit_kobo?: number;
}

export interface InvoiceSettings {
  prefix: string;
  next_number: number;
  connection: string;
  fee_paid_by: FeePaidBy;
  allow_part_payments: boolean;
  /** A strong suggestion is matched without asking (off by default). */
  auto_match?: boolean;
}

/** The limits the server holds an invoice to (engine/invoicing, openapi InvoiceDraft). */
export const INVOICE_LIMITS = { name: 120, description: 200, note: 500, lines: 50, prefix: 12 } as const;

// --- money ---

/** Kobo as naira: ₦25,000 or ₦25,000.50. */
export const naira = (kobo: number) => formatPrice(kobo);

/** Typed naira ("25,000", "₦1,500.50") in kobo; undefined when empty, NaN when not an amount. */
export function toKobo(text: string): number | undefined {
  const n = parseNaira(text);
  if (n === undefined || Number.isNaN(n)) return n;
  return Math.round(n * 100);
}

/** Kobo as the form shows it for typing: 2500050 is "25000.50", 2500000 is "25000". */
export function koboInput(kobo: number): string {
  const whole = Math.floor(kobo / 100);
  const frac = kobo % 100;
  return frac ? `${whole}.${String(frac).padStart(2, "0")}` : String(whole);
}

// --- statuses ---

export const INVOICE_STATUS: Record<InvoiceStatus, { label: string; tone: Tone }> = {
  draft: { label: "Draft", tone: "muted" },
  sent: { label: "Sent", tone: "info" },
  part_paid: { label: "Part-paid", tone: "warn" },
  paid: { label: "Paid", tone: "ok" },
  overdue: { label: "Overdue", tone: "danger" },
  void: { label: "Void", tone: "muted" },
};

/** A status as a pill shows it. */
export function invoiceStatus(s: string): { label: string; tone: Tone } {
  return INVOICE_STATUS[s as InvoiceStatus] ?? { label: s.replaceAll("_", " "), tone: "muted" };
}

/** The list's filter, as the page's ?status= holds it ("" is all). */
export const INVOICE_FILTERS: { value: string; label: string }[] = [
  { value: "", label: "All" },
  ...(Object.keys(INVOICE_STATUS) as InvoiceStatus[]).map((s) => ({ value: s, label: INVOICE_STATUS[s].label })),
];

/** The GET /v1/invoices call for a filter. */
export function invoicesPath(status: string): string {
  return status && status in INVOICE_STATUS ? `/v1/invoices?${new URLSearchParams({ status })}` : "/v1/invoices";
}

/** An invoice's own path in the API ({id} is its id or number). */
export const invoicePath = (id: string, rest = "") => `/v1/invoices/${encodeURIComponent(id)}${rest}`;

/** Newest first, as the server sends them; kept so for a list put together here. */
export function newestFirst<T extends { created_at: string }>(list: T[]): T[] {
  return [...list].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
}

/** A YYYY-MM-DD date in words: "24 Oct 2026". */
export function dateLabel(d: string | undefined | null): string {
  if (!d || !/^\d{4}-\d{2}-\d{2}/.test(d)) return "";
  const t = new Date(`${d.slice(0, 10)}T12:00:00Z`);
  return t.toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });
}

/** Who the customer is, as the list shows them (masked by the server). */
export function invoiceCustomerLine(c: InvoiceCustomer): string {
  return [c.name, c.phone || c.email].filter(Boolean).join(" · ") || "—";
}

/** The server's refusal as a sentence: "the next number cannot go back below 7" reads "The next number cannot go back below 7." */
export function plainMessage(text: string): string {
  const t = text.trim();
  if (!t) return t;
  const s = t.charAt(0).toUpperCase() + t.slice(1);
  return /[.!?]$/.test(s) ? s : `${s}.`;
}

// --- what a person may do ---

/** What a person may see and do with invoices at all: the navigation's entry, New invoice, and the customer's details. */
export function invoiceAccess(can: (perm: string) => boolean): { see: boolean; manage: boolean; reveal: boolean } {
  return { see: can("invoice.read"), manage: can("invoice.manage"), reveal: can("pii.reveal") };
}

export interface InvoiceActions {
  edit: boolean;
  send: boolean;
  sendAgain: boolean;
  void: boolean;
  undo: boolean;
  reveal: boolean;
  /** A sent invoice changes only by amendment, with a reason. */
  amend: boolean;
}

/** The actions an invoice offers, by its status and the person's permissions. */
export function invoiceActions(status: InvoiceStatus | string, can: (perm: string) => boolean): InvoiceActions {
  const manage = can("invoice.manage");
  return {
    edit: manage && status === "draft",
    send: manage && status === "draft",
    sendAgain: manage && (status === "sent" || status === "part_paid" || status === "overdue"),
    void: manage && status !== "paid" && status !== "void",
    undo: manage,
    reveal: can("pii.reveal"),
    amend: manage && status !== "draft" && status !== "void",
  };
}

// --- amendments ---

export type AmendmentKind = "add" | "remove" | "discount";

/** The amendment form: amount as typed in naira; line the position of the item a removal takes out. */
export interface AmendmentForm {
  kind: AmendmentKind;
  description: string;
  amount: string;
  passThroughFor: string;
  line: string;
  reason: string;
}

export function emptyAmendment(): AmendmentForm {
  return { kind: "add", description: "", amount: "", passThroughFor: "", line: "", reason: "" };
}

/** The items a removal may take out: their position and words. Discounts, removals and lines taken out already are not offered. */
export function removableLines(x: Pick<CustomerInvoice, "lines" | "amendments">): { index: number; label: string }[] {
  const gone = new Set((x.amendments ?? []).map((a) => a.removes).filter((n): n is number => typeof n === "number"));
  return (x.lines ?? []).flatMap((l, index) =>
    !l.kind && l.unit_kobo > 0 && !gone.has(index) ? [{ index, label: `${l.description} (${naira(l.quantity * l.unit_kobo)})` }] : [],
  );
}

/** Why the amendment would be refused here, or null. A total below what was paid is the server's to refuse. */
export function amendmentProblem(f: AmendmentForm): string | null {
  if (!f.reason.trim()) return "Say why the invoice is amended. The customer's page shows the reason.";
  if (f.reason.trim().length > 300) return "The reason is at most 300 characters.";
  if (f.kind === "remove") return f.line === "" ? "Choose the item to take out." : null;
  const k = toKobo(f.amount);
  if (k === undefined || Number.isNaN(k) || k < 100) return "Give the amount, at least ₦1.";
  if (f.kind === "add" && !f.description.trim()) return "Say what the added line is for.";
  return null;
}

/** The POST /v1/invoices/{id}/amendments body. */
export function amendmentRequest(f: AmendmentForm): Record<string, unknown> {
  const reason = f.reason.trim();
  if (f.kind === "remove") return { kind: "remove", line: Number(f.line), reason };
  const body: Record<string, unknown> = { kind: f.kind, amount_kobo: toKobo(f.amount), reason };
  if (f.description.trim()) body.description = f.description.trim();
  if (f.kind === "add" && f.passThroughFor.trim()) body.pass_through_for = f.passThroughFor.trim();
  return body;
}

/** What a line says beside its description: who a pass-through line is collected for, and why a discount or an amendment was given. */
export function lineNote(l: InvoiceLine): string {
  const out: string[] = [];
  if (l.pass_through_for) out.push(`Collected on behalf of ${l.pass_through_for}.`);
  if (l.amendment && !l.kind) out.push("Added after the invoice was sent.");
  if (l.reason) out.push(l.reason);
  return out.join(" ");
}

/** Why a void would be refused here, or null. */
export function voidProblem(reason: string): string | null {
  if (!reason.trim()) return "Say why it is void. The customer's page shows the reason.";
  if (reason.trim().length > INVOICE_LIMITS.note) return `The reason is at most ${INVOICE_LIMITS.note} characters.`;
  return null;
}

// --- sending ---

/** What Send did, in words: the channels it queued, and SMS held back by the plan. */
export function sentLines(s: Pick<InvoiceSent, "queued" | "sms_over_allowance" | "credit_kobo">): string[] {
  const q = s.queued ?? [];
  const out: string[] = [];
  const by = [q.includes("sms") ? "SMS" : "", q.includes("email") ? "email" : ""].filter(Boolean);
  out.push(by.length ? `Sent by ${by.join(" and ")}.` : "Nothing was sent: the customer has no phone number or email we can use.");
  if (s.sms_over_allowance) out.push("SMS not sent: plan allowance used.");
  if (s.credit_kobo) out.push(`${naira(s.credit_kobo)} of the customer's credit counts towards it.`);
  return out;
}

/** A fresh Idempotency-Key: a retry with the same one makes or sends nothing more. */
export function newRequestKey(): string {
  const c = globalThis.crypto;
  if (c && typeof c.randomUUID === "function") return `web:${c.randomUUID()}`;
  return `web:${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

// --- payments ---

/** How a payment was matched to the invoice, in words. */
export function howMatched(how: string, by = ""): string {
  switch (how) {
    case "exact":
      return "Paid into the account Taskiem gave";
    case "suggested":
      return by ? `Suggested, confirmed by ${by}` : "Suggested, then confirmed";
    case "suggested_auto":
      return "Matched automatically";
    case "manual":
      return "Matched by hand";
    default:
      return how.replaceAll("_", " ");
  }
}

export interface PaymentRow {
  payment: InvoicePayment;
  /** The row that undid it, when it was undone. */
  undoneBy?: InvoicePayment;
}

/** The payments as the invoice lists them: each counted payment, with its undoing when there is one. */
export function paymentRows(payments: InvoicePayment[] | null | undefined): PaymentRow[] {
  const list = payments ?? [];
  const undoings = new Map(list.filter((p) => p.undoes).map((p) => [p.undoes as string, p]));
  return list
    .filter((p) => !p.undoes)
    .map((p) => {
      const u = undoings.get(p.id) ?? (p.undone_by ? list.find((x) => x.id === p.undone_by) : undefined);
      return u || p.undone_by ? { payment: p, undoneBy: u ?? { ...p, id: p.undone_by as string, undoes: p.id } } : { payment: p };
    });
}

// --- the form ---

export interface ItemInput {
  description: string;
  quantity: string;
  price: string;
}

/** A draft as the form holds it: text as typed. */
export interface InvoiceForm {
  name: string;
  phone: string;
  email: string;
  mode: "amount" | "items";
  amount: string;
  description: string;
  items: ItemInput[];
  dueOn: string;
  note: string;
  connection: string;
  feePaidBy: FeePaidBy;
  partPayments: boolean;
}

export const emptyItem = (): ItemInput => ({ description: "", quantity: "1", price: "" });

/** A new invoice's form, with the business's settings as its defaults. */
export function newInvoiceForm(s?: Pick<InvoiceSettings, "fee_paid_by" | "allow_part_payments"> | null): InvoiceForm {
  return {
    name: "",
    phone: "",
    email: "",
    mode: "amount",
    amount: "",
    description: "",
    items: [emptyItem()],
    dueOn: "",
    note: "",
    connection: "",
    feePaidBy: s?.fee_paid_by ?? "business",
    partPayments: s?.allow_part_payments ?? false,
  };
}

/** A draft's form: one line of quantity 1 is a single amount. */
export function formFromInvoice(x: CustomerInvoice): InvoiceForm {
  const lines = x.lines ?? [];
  const single = lines.length === 1 && lines[0]?.quantity === 1;
  return {
    name: x.customer.name ?? "",
    phone: x.customer.phone ?? "",
    email: x.customer.email ?? "",
    mode: single ? "amount" : "items",
    amount: single && lines[0] ? koboInput(lines[0].unit_kobo) : "",
    description: single && lines[0] ? lines[0].description : "",
    items: single || lines.length === 0 ? [emptyItem()] : lines.map((l) => ({ description: l.description, quantity: String(l.quantity), price: koboInput(l.unit_kobo) })),
    dueOn: x.due_on ? x.due_on.slice(0, 10) : "",
    note: x.note ?? "",
    connection: x.connection ?? "",
    feePaidBy: x.fee_paid_by ?? "business",
    partPayments: !!x.allow_part_payments,
  };
}

/** A whole number of at least 1, or NaN. */
function quantityOf(text: string): number {
  const t = text.trim();
  return /^\d+$/.test(t) && Number(t) >= 1 ? Number(t) : NaN;
}

/** An item's total in kobo; 0 while it is not yet a quantity and price. */
export function itemTotal(i: ItemInput): number {
  const q = quantityOf(i.quantity);
  const p = toKobo(i.price);
  return Number.isFinite(q) && p !== undefined && Number.isFinite(p) && p > 0 ? q * p : 0;
}

/** The form's total in kobo, as typed so far. */
export function formTotal(f: InvoiceForm): number {
  if (f.mode === "amount") {
    const k = toKobo(f.amount);
    return k !== undefined && Number.isFinite(k) && k > 0 ? k : 0;
  }
  return f.items.reduce((sum, i) => sum + itemTotal(i), 0);
}

const EMAIL = /^[^@\s]+@[^@\s]+\.[^@\s]+$/;

/** Why the form will be refused, or null. The server checks too; its words are shown as they come. */
export function invoiceProblem(f: InvoiceForm): string | null {
  if (!f.name.trim()) return "Give the customer's name.";
  if (f.name.trim().length > INVOICE_LIMITS.name) return `The customer's name is at most ${INVOICE_LIMITS.name} characters.`;
  if (!f.phone.trim() && !f.email.trim()) return "Give the customer's phone number or email, or both, so the invoice can be sent.";
  if (f.email.trim() && !EMAIL.test(f.email.trim())) return "The email does not look right.";
  if (f.mode === "amount") {
    const k = toKobo(f.amount);
    if (k === undefined) return "Give the amount.";
    if (!Number.isFinite(k) || k <= 0) return "The amount is a number of naira, such as 25000 or 25000.50.";
    if (f.description.trim().length > INVOICE_LIMITS.description) return `The description is at most ${INVOICE_LIMITS.description} characters.`;
  } else {
    if (f.items.length === 0) return "Add at least one item.";
    if (f.items.length > INVOICE_LIMITS.lines) return `An invoice has at most ${INVOICE_LIMITS.lines} items.`;
    for (const [n, i] of f.items.entries()) {
      const which = f.items.length > 1 ? `Item ${n + 1}` : "The item";
      if (!i.description.trim()) return `${which} needs a description.`;
      if (i.description.trim().length > INVOICE_LIMITS.description) return `${which}'s description is at most ${INVOICE_LIMITS.description} characters.`;
      if (!Number.isFinite(quantityOf(i.quantity))) return `${which}'s quantity is a whole number, 1 or more.`;
      const p = toKobo(i.price);
      if (p === undefined || !Number.isFinite(p) || p <= 0) return `${which}'s price is a number of naira, such as 1500 or 1500.50.`;
    }
  }
  if (f.dueOn && !/^\d{4}-\d{2}-\d{2}$/.test(f.dueOn)) return "The due date does not look right.";
  if (f.note.trim().length > INVOICE_LIMITS.note) return `The note is at most ${INVOICE_LIMITS.note} characters.`;
  return null;
}

/** The POST/PUT body for the form. Call it once invoiceProblem is null. */
export function draftRequest(f: InvoiceForm): InvoiceDraft {
  const customer: InvoiceCustomer = { name: f.name.trim() };
  if (f.phone.trim()) customer.phone = f.phone.trim();
  if (f.email.trim()) customer.email = f.email.trim();
  const d: InvoiceDraft = { customer };
  if (f.mode === "amount") {
    d.amount_kobo = toKobo(f.amount) ?? 0;
    if (f.description.trim()) d.description = f.description.trim();
  } else {
    d.lines = f.items.map((i) => ({ description: i.description.trim(), quantity: quantityOf(i.quantity), unit_kobo: toKobo(i.price) ?? 0 }));
  }
  if (f.dueOn) d.due_on = f.dueOn;
  if (f.note.trim()) d.note = f.note.trim();
  if (f.connection.trim()) d.connection = f.connection.trim();
  d.fee_paid_by = f.feePaidBy;
  d.allow_part_payments = f.partPayments;
  return d;
}

// --- settings ---

export interface SettingsForm {
  prefix: string;
  nextNumber: string;
  connection: string;
  feePaidBy: FeePaidBy;
  partPayments: boolean;
  autoMatch: boolean;
}

export function settingsForm(s: InvoiceSettings): SettingsForm {
  return {
    prefix: s.prefix,
    nextNumber: String(s.next_number),
    connection: s.connection,
    feePaidBy: s.fee_paid_by,
    partPayments: s.allow_part_payments,
    autoMatch: !!s.auto_match,
  };
}

/** Why the settings will be refused, or null; the next number only moves forward from what was saved. */
export function settingsProblem(f: SettingsForm, saved: Pick<InvoiceSettings, "next_number">): string | null {
  const prefix = f.prefix.trim();
  if (prefix.length > INVOICE_LIMITS.prefix) return `The prefix is at most ${INVOICE_LIMITS.prefix} characters.`;
  if (prefix && !/^[A-Za-z0-9_/-]+$/.test(prefix)) return "The prefix uses letters, digits, -, _ or / only.";
  const n = Number(f.nextNumber.trim());
  if (!/^\d+$/.test(f.nextNumber.trim()) || n < 1) return "The next number is a whole number, 1 or more.";
  if (n < saved.next_number) return `The next number can only move forward, so no number is given twice. It is ${saved.next_number} or more.`;
  return null;
}

export function settingsRequest(f: SettingsForm): InvoiceSettings {
  return {
    prefix: f.prefix.trim(),
    next_number: Number(f.nextNumber.trim()),
    connection: f.connection.trim() || "main",
    fee_paid_by: f.feePaidBy,
    allow_part_payments: f.partPayments,
    auto_match: f.autoMatch,
  };
}

/** The next invoice's number, as settings would give it: INV-0001. */
export function numberPreview(prefix: string, next: number): string {
  return `${prefix.trim()}${String(Number.isFinite(next) && next >= 1 ? Math.floor(next) : 1).padStart(4, "0")}`;
}

export const FEE_CHOICES: { value: FeePaidBy; label: string; help: string }[] = [
  { value: "business", label: "The business", help: "The customer pays the invoice's amount; the fee comes out of what you receive." },
  { value: "customer", label: "The customer", help: "The fee is added to what the customer pays, where the payments provider allows it." },
];

// --- matching money to invoices (T5) ---

/** One open suggestion: money received that may be for an invoice or a storefront order (MoneySuggestion). */
export interface MoneySuggestion {
  id: string;
  entry_id: string;
  /** invoice or order; target names it as money_matches does ("order:AK-7QX4KD"). */
  target_kind?: "invoice" | "order";
  target?: string;
  invoice_id: string | null;
  order_id?: string;
  /** The invoice's number, or the order's reference. */
  number: string;
  /** What is due on the invoice, or paid online for the order. */
  due_kobo?: number;
  /** The customer's name, masked. */
  customer?: string;
  score: number;
  reasons: string[] | null;
  status: string;
  amount_kobo: number | null;
  /** What is left of the payment to match. */
  left_kobo?: number | null;
  /** How the payment could be split across the customer's open invoices (this one first), whenever they have more than one, or it is more than is due. */
  split?: SplitProposal;
  channel: string;
  reference: string;
  received_at: string;
  kind: string;
}

/** A split the server offers (MoneySplitProposal): each invoice with what of the payment would count towards it (0 for one left to choose), a sibling's marked where the school shares family credit, what would be left as the customer's credit, and whether the payment is more than is due on the suggested invoice (over). */
export interface SplitProposal {
  parts: { invoice?: string; amount_kobo: number; number: string; due_kobo: number; sibling?: boolean }[] | null;
  credit_kobo: number;
  over?: boolean;
}

/** One editable row of a split: an invoice, what is due on it and the amount typed. */
export interface SplitRow {
  invoice: string;
  due_kobo: number;
  amount: string;
  sibling?: boolean;
}

/** A proposal as editable rows: an invoice offered nothing has an empty amount. */
export function splitRows(p: SplitProposal | undefined): SplitRow[] {
  return (p?.parts ?? []).map((x) => ({ invoice: x.number, due_kobo: x.due_kobo, amount: x.amount_kobo > 0 ? koboInput(x.amount_kobo) : "", ...(x.sibling ? { sibling: true } : {}) }));
}

/** A split row's label: the invoice, what is due, and that it is a sibling's where it is. */
export const splitRowLabel = (r: SplitRow) => `${r.invoice} (${naira(r.due_kobo)} due${r.sibling ? ", a sibling's" : ""})`;

/** Why the split is offered, for its hint: the payment is more than is due on the suggested invoice, or the customer has other open invoices. */
export function splitHint(p: SplitProposal | undefined, number: string): string {
  const family = (p?.parts ?? []).some((x) => x.sibling);
  const whose = family ? "the family's" : "the same customer's";
  if (p?.over) return `The payment is more than is due on ${number}. Offered across ${whose} open invoices, ${number} first then oldest first; change the amounts if you know better.`;
  return `${number} is offered all of it. If this payment is for several of ${whose} open invoices, give each its part. An invoice can take more of this payment later: a further part adds to the first.`;
}

/** What the rows add up to, in kobo; NaN when one is not an amount. */
export function splitTotal(rows: SplitRow[]): number {
  let sum = 0;
  for (const r of rows) {
    const k = toKobo(r.amount);
    if (k === undefined) continue;
    if (Number.isNaN(k)) return NaN;
    sum += k;
  }
  return sum;
}

/** Why rows cannot be matched, or "": every amount a number, at least one part, the parts no more than is left. */
export function splitProblem(rows: SplitRow[], leftKobo: number): string {
  const total = splitTotal(rows);
  if (Number.isNaN(total)) return "Type each amount as naira, like 20,000.";
  if (rows.some((r) => (toKobo(r.amount) ?? 0) < 0)) return "An amount cannot be less than nothing.";
  if (total <= 0) return "Give at least one invoice an amount.";
  if (total > leftKobo) return `The parts add up to ${naira(total)}, more than the ${naira(leftKobo)} received.`;
  return "";
}

/** The POST body confirming a suggestion in parts: rows with an amount, and whether the rest is the customer's credit. */
export function splitRequest(rows: SplitRow[], creditRest: boolean): { parts: { invoice: string; amount_kobo: number }[]; credit_rest: boolean } {
  return {
    parts: rows.map((r) => ({ invoice: r.invoice, amount_kobo: toKobo(r.amount) ?? 0 })).filter((x) => x.amount_kobo > 0),
    credit_rest: creditRest,
  };
}

/** What a payment split answers (MoneySplit). */
export interface MoneySplit {
  parts: { target: string; amount_kobo: number }[] | null;
  left_kobo: number;
  credit_kobo: number;
}

/** What the To match view says after a split. */
export function splitLine(s: MoneySplit): string {
  const parts = (s.parts ?? []).map((x) => `${x.target.replace(/^invoice:/, "").replace(/^order:/, "order ")} ${naira(x.amount_kobo)}`);
  let line = `Matched in parts: ${parts.join(", ")}.`;
  if (s.credit_kobo > 0) line += ` ${naira(s.credit_kobo)} is the customer's credit, counted towards their next invoice.`;
  if (s.left_kobo > 0) line += ` ${naira(s.left_kobo)} is left to match.`;
  return line;
}

export const SUGGESTIONS_PATH = "/v1/money/suggestions";
export const suggestionPath = (id: string, action: "confirm" | "reject") => `/v1/money/suggestions/${encodeURIComponent(id)}/${action}`;
export const entryMatchPath = (entry: string) => `/v1/money/entries/${encodeURIComponent(entry)}/match`;

/** Where a score's words change: 85 and up is strong (the least automatic matching takes), 60 and up likely. */
export const SCORE_STRONG = 85;
export const SCORE_LIKELY = 60;

/** A suggestion's score (0 to 100) in words. */
export function scoreWords(score: number): { label: string; tone: Tone } {
  if (score >= SCORE_STRONG) return { label: "Strong", tone: "ok" };
  if (score >= SCORE_LIKELY) return { label: "Likely", tone: "info" };
  return { label: "Possible", tone: "muted" };
}

const KIND_WORDS: Record<string, string> = {
  payment: "Payment",
  account_credit: "Transfer into an account",
  pos_sale: "POS sale",
  statement: "Line of a bank statement",
  cash: "Cash",
};

/** A money log entry's kind in words. */
export const kindWords = (kind: string) => KIND_WORDS[kind] ?? kind.replaceAll("_", " ");

/** Where the money came: "paystack/main" is "Paystack (main)"; "upload" an uploaded statement. */
export function channelWords(channel: string): string {
  if (channel === "upload") return "Uploaded statement";
  if (channel === "cash") return "Cash";
  const i = channel.indexOf("/");
  const rail = i < 0 ? channel : channel.slice(0, i);
  const name = i < 0 ? "" : channel.slice(i + 1);
  const r = rail ? rail.charAt(0).toUpperCase() + rail.slice(1) : channel;
  return name ? `${r} (${name})` : r;
}

/** A payment waiting to be matched, with the invoices it may be for, strongest first. */
export interface WaitingPayment {
  entry_id: string;
  amount_kobo: number | null;
  /** What is left of it to match, when part of it counts already. */
  left_kobo?: number | null;
  /** Why matching held it for a person. */
  held_reason?: string;
  channel: string;
  reference: string;
  received_at: string;
  kind: string;
  candidates: MoneySuggestion[];
}

/** The suggestions as the To match view shows them: one card per payment, in the order the server sent them (newest payment first). */
export function groupSuggestions(list: MoneySuggestion[] | null | undefined): WaitingPayment[] {
  const out: WaitingPayment[] = [];
  const by = new Map<string, WaitingPayment>();
  for (const s of list ?? []) {
    let g = by.get(s.entry_id);
    if (!g) {
      g = { entry_id: s.entry_id, amount_kobo: s.amount_kobo, left_kobo: s.left_kobo, channel: s.channel, reference: s.reference, received_at: s.received_at, kind: s.kind, candidates: [] };
      by.set(s.entry_id, g);
      out.push(g);
    }
    g.candidates.push(s);
  }
  for (const g of out) g.candidates.sort((a, b) => b.score - a.score);
  return out;
}

export const UNMATCHED_PATH = "/v1/money/unmatched";

/** Money received that matching left for a person (GET /v1/money/unmatched). */
export interface UnmatchedMoney {
  entry_id: string;
  amount_kobo: number | null;
  left_kobo?: number | null;
  held_reason?: string;
  channel: string;
  reference: string;
  received_at: string;
  kind: string;
}

/** Unmatched payments as To match cards, with nothing suggested. */
export function unmatchedPayments(list: UnmatchedMoney[] | null | undefined): WaitingPayment[] {
  return (list ?? []).map((u) => ({ ...u, candidates: [] }));
}

/** What is left of a payment to match, in words, when part of it counts already; "" when all of it is left. */
export function leftWords(p: Pick<WaitingPayment, "amount_kobo" | "left_kobo">): string {
  if (p.left_kobo === null || p.left_kobo === undefined || p.amount_kobo === null || p.left_kobo >= p.amount_kobo) return "";
  return `${naira(p.left_kobo)} of it is left to match; the rest counts towards other invoices or orders.`;
}

// --- the invoice's customer in matching ---

/** A customer's permanent account number (CustomerAccount). */
export interface CustomerAccount {
  id: string;
  channel: string;
  account_number: string;
  bank_name?: string;
  reference: string;
}

/** A payer remembered for a customer (MoneyPayer). */
export interface RememberedPayer {
  id: string;
  kind: "name" | "account";
  payer: string;
  times: number;
  last_at: string;
}

/** GET /v1/invoices/{id}/customer. */
export interface InvoiceCustomerView {
  credit_kobo: number;
  accounts: CustomerAccount[] | null;
  payers: RememberedPayer[] | null;
  revealed: boolean;
}

export const invoiceCustomerPath = (id: string) => `/v1/invoices/${encodeURIComponent(id)}/customer`;
export const payerPath = (id: string) => `/v1/money/payers/${encodeURIComponent(id)}`;

/** A remembered payer in words: "A••• (name), paid 2 times". */
export function payerWords(p: RememberedPayer): string {
  const what = p.kind === "account" ? "paying account" : "name";
  return `${p.payer} (${what}), paid ${p.times === 1 ? "once" : `${p.times} times`}`;
}

/** A customer's credit in words. */
export const creditWords = (kobo: number) =>
  kobo > 0 ? `${naira(kobo)}: paid and not yet counted towards an invoice. It counts towards the next invoice sent to this customer.` : "None";

/** An amount that may be missing, in words. */
export const amountWords = (kobo: number | null | undefined) => (kobo === null || kobo === undefined ? "Amount not known" : naira(kobo));

/** The invoices a payment can be matched to: sent and not yet fully paid. */
export function openInvoices(list: CustomerInvoice[] | null | undefined): CustomerInvoice[] {
  return (list ?? []).filter((x) => x.status === "sent" || x.status === "part_paid" || x.status === "overdue");
}

/** What the To match view says after a match. */
export function matchedLine(amountKobo: number | null, number: string, kind: "invoice" | "order" = "invoice"): string {
  if (kind === "order") return `${amountWords(amountKobo)} matched to order ${number}. If that is wrong, undo it on the order.`;
  return `${amountWords(amountKobo)} matched to ${number}. If that is wrong, undo it on the invoice.`;
}

/** What the To match view says after "Not this one". */
export function rejectedLine(number: string, kind: "invoice" | "order" = "invoice"): string {
  return `${kind === "order" ? `Order ${number}` : number} will not be suggested for this payment again.`;
}

/** A storefront order money can be matched to by hand (MoneyOpenOrder, in GET /v1/money/unmatched). */
export interface MoneyOpenOrder {
  target_kind: "order";
  target: string;
  number: string;
  due_kobo: number;
  customer: string;
  placed_at: string;
}

/** Whether a suggestion is for a storefront order. */
export const isOrder = (c: Pick<MoneySuggestion, "target_kind">) => c.target_kind === "order";

/** A suggestion's invoice or order as the To match view names it: "INV-0104", "Order AK-7QX4KD". */
export const targetLabel = (c: Pick<MoneySuggestion, "target_kind" | "number">) => (isOrder(c) ? `Order ${c.number}` : c.number);

/** Where a suggestion's invoice or order is shown in the app. */
export const targetPath = (c: Pick<MoneySuggestion, "target_kind" | "number">) =>
  isOrder(c) ? `/storefront/orders/${encodeURIComponent(c.number)}` : `/invoices/${encodeURIComponent(c.number)}`;

/** One choice of the "match by hand" picker: an invoice (value its id) or an order (value "order:<reference>"), with what is due on it. */
export interface MatchChoice {
  value: string;
  kind: "invoice" | "order";
  number: string;
  label: string;
  due_kobo: number;
}

/** The invoices and orders a payment can be matched to by hand, those already suggested for it left out. */
export function matchChoices(invoices: CustomerInvoice[], orders: MoneyOpenOrder[] | null | undefined, candidates: MoneySuggestion[]): MatchChoice[] {
  const out: MatchChoice[] = invoices
    .filter((x) => !candidates.some((c) => !isOrder(c) && c.invoice_id === x.id))
    .map((x) => ({ value: x.id, kind: "invoice", number: x.number, label: `${x.number} · ${naira(x.due_kobo)} due · ${x.customer.name}`, due_kobo: x.due_kobo }));
  for (const o of orders ?? []) {
    if (candidates.some((c) => isOrder(c) && c.number === o.number)) continue;
    const who = o.customer ? ` · ${o.customer}` : "";
    out.push({ value: `order:${o.number}`, kind: "order", number: o.number, label: `Order ${o.number} · ${naira(o.due_kobo)} to pay${who}`, due_kobo: o.due_kobo });
  }
  return out;
}

/** The POST /v1/money/entries/{id}/match body for a picker choice. */
export function matchRequest(value: string): { invoice: string } | { order: string } {
  return value.startsWith("order:") ? { order: value.slice("order:".length) } : { invoice: value };
}

// --- splitting by hand ---

/** One row of a split by hand: an invoice or order the person may pick, and the amount typed for it. */
export interface HandRow extends MatchChoice {
  picked: boolean;
  amount: string;
}

/** The picker's choices as rows to pick from, none picked. */
export const handRows = (choices: MatchChoice[]): HandRow[] => choices.map((c) => ({ ...c, picked: false, amount: "" }));

/** What the picked rows add up to, in kobo; NaN when one is not an amount. */
export function handTotal(rows: HandRow[]): number {
  let sum = 0;
  for (const r of rows) {
    if (!r.picked) continue;
    const k = toKobo(r.amount);
    if (k === undefined) continue;
    if (Number.isNaN(k)) return NaN;
    sum += k;
  }
  return sum;
}

/** What is left of the payment after the picked parts, never below nothing. */
export function handRest(rows: HandRow[], leftKobo: number): number {
  const total = handTotal(rows);
  return Number.isNaN(total) ? leftKobo : Math.max(leftKobo - total, 0);
}

/** Why the picked rows cannot be matched, or "": at least one picked, each picked one an amount above nothing, and the parts no more than is left of the payment (the rule the database keeps too). */
export function handProblem(rows: HandRow[], leftKobo: number): string {
  const picked = rows.filter((r) => r.picked);
  if (picked.length === 0) return "Tick the invoices or orders this payment is for.";
  for (const r of picked) {
    const k = toKobo(r.amount);
    if (k === undefined || Number.isNaN(k) || k <= 0) return `Give ${r.kind === "order" ? `order ${r.number}` : r.number} its part as naira, like 20,000.`;
  }
  const total = handTotal(rows);
  if (total > leftKobo) return `The parts add up to ${naira(total)}, more than the ${naira(leftKobo)} left of this payment.`;
  return "";
}

/** The POST /v1/money/entries/{id}/match body for the picked rows: each a part of the payment; the rest waits to be matched. */
export function handRequest(rows: HandRow[]): { parts: (({ invoice: string } | { order: string }) & { amount_kobo: number })[] } {
  return {
    parts: rows.filter((r) => r.picked).map((r) => ({ ...matchRequest(r.value), amount_kobo: toKobo(r.amount) ?? 0 })),
  };
}

/** What a split by hand leaves, in words. */
export const handRestWords = (rest: number) => (rest > 0 ? `${naira(rest)} left to match afterwards.` : "All of the payment counts towards these.");

/** The picker's words: about invoices only while the business has no order waiting for money. */
export function pickerWords(orders: boolean) {
  return orders
    ? {
        summary: "Match to another invoice or order…",
        label: "Invoice or order",
        hint: "Invoices sent and not yet fully paid, and storefront orders waiting for their payment.",
        empty: "Choose an invoice or order",
        submit: "Match to this one",
        none: "No other invoice or order is waiting for payment.",
        missing: "Choose the invoice or order this payment is for.",
      }
    : {
        summary: "Match to another invoice…",
        label: "Invoice",
        hint: "Invoices sent and not yet fully paid.",
        empty: "Choose an invoice",
        submit: "Match to this invoice",
        none: "No other invoice is waiting for payment.",
        missing: "Choose the invoice this payment is for.",
      };
}

/** A payment matched to a storefront order (StorefrontOrderPayment, in GET /v1/storefront/orders/{reference}). */
export interface OrderPayment {
  id: string;
  entry_id: string;
  amount_kobo: number;
  how: string;
  by: string;
  reasons: string[] | null;
  undoes?: string;
  undone_by?: string;
  undoable: boolean;
  created_at: string;
}

export const orderPaymentUndoPath = (id: string) => `/v1/money/order-payments/${encodeURIComponent(id)}/undo`;

/** How money was matched to an order, in words. */
export const howMatchedOrder = (how: string, by = "") => (how === "exact" ? "Paid through the order's checkout page" : howMatched(how, by));

/** The money matched to an order, as the order lists it: each match, not its undoings, and whether it was undone. */
export function orderPaymentRows(list: OrderPayment[] | null | undefined): { payment: OrderPayment; undone: boolean }[] {
  return (list ?? []).filter((p) => !p.undoes).map((p) => ({ payment: p, undone: !!p.undone_by }));
}

/** The top of an invoice's page: the figure that matters for its status,
 * what it is, a line under it and how much of the total is paid (0 to 1). */
export function invoiceHero(x: { status: string; total_kobo: number; paid_kobo: number; due_kobo: number }): { label: string; kobo: number; sub: string; paid: number } {
  const paid = x.total_kobo > 0 ? Math.min(1, Math.max(0, x.paid_kobo / x.total_kobo)) : 0;
  switch (x.status) {
    case "draft":
      return { label: "To ask for", kobo: x.total_kobo, sub: "Not sent yet", paid };
    case "void":
      return { label: "Void", kobo: x.total_kobo, sub: x.paid_kobo > 0 ? `${naira(x.paid_kobo)} was paid before it was voided` : "It takes no payment", paid };
    case "paid":
      return { label: "Paid in full", kobo: x.total_kobo, sub: "Nothing left to pay", paid: 1 };
    default:
      return { label: x.status === "overdue" ? "Overdue" : "Still due", kobo: x.due_kobo, sub: `of ${naira(x.total_kobo)} · ${naira(x.paid_kobo)} paid`, paid };
  }
}

// --- giving a customer an account number ---

/** A connection that can issue customers' account numbers (GET /v1/customer-accounts/issuers). */
export interface AccountIssuer {
  connection: string;
  rail: string;
  needs_email: boolean;
  needs_bvn: boolean;
  needs_account: boolean;
  names_itself: boolean;
}

export const ISSUERS_PATH = "/v1/customer-accounts/issuers";
export const customerAccountsPath = (id: string) => `/v1/invoices/${encodeURIComponent(id)}/customer-accounts`;

/** The "Give this customer an account number" form: issued on a connection, or one the business has, typed in. */
export interface AccountForm {
  by_hand: boolean;
  connection: string;
  account_number: string;
  reference: string;
  bank_name: string;
  /** The wallet, on a rail that holds several. */
  account: string;
  bvn: string;
  nin: string;
}

/** A form for the issuers the business has: the first issuer, else by hand. */
export function newAccountForm(issuers: AccountIssuer[]): AccountForm {
  return { by_hand: issuers.length === 0, connection: issuers[0]?.connection ?? "", account_number: "", reference: "", bank_name: "", account: "", bvn: "", nin: "" };
}

/** The issuer the form names, when it issues. */
export const issuerOf = (f: AccountForm, issuers: AccountIssuer[]): AccountIssuer | undefined => (f.by_hand ? undefined : issuers.find((i) => i.connection === f.connection));

/** What stops the form being sent, in words; undefined when nothing does. */
export function accountProblem(f: AccountForm, issuers: AccountIssuer[]): string | undefined {
  if (f.by_hand) {
    if (!/^[a-z0-9_]{1,60}\/[A-Za-z0-9_.-]{1,100}$/.test(f.connection.trim())) return "Name the connection the account is on, like paystack/main.";
    if (!/^[0-9A-Za-z-]{4,40}$/.test(f.account_number.trim())) return "The account number has 4 to 40 letters and digits.";
    if (!f.reference.trim()) return "The account's reference is required: the payments provider's reference for it, which its payments carry.";
    return undefined;
  }
  const i = issuerOf(f, issuers);
  if (!i) return "Choose the connection to issue the account on.";
  if (i.needs_account && !f.account.trim()) return `${i.rail} needs the wallet the number pays into.`;
  if (i.needs_bvn && !/^\d{11}$/.test(f.bvn.trim()) && !/^\d{11}$/.test(f.nin.trim())) return `${i.rail} needs the customer's BVN or NIN (11 digits).`;
  return undefined;
}

/** The request body (POST /v1/invoices/{id}/customer-accounts). */
export function accountRequest(f: AccountForm): Record<string, unknown> {
  if (f.by_hand) {
    const body: Record<string, unknown> = { connection: f.connection.trim(), account_number: f.account_number.trim(), reference: f.reference.trim() };
    if (f.bank_name.trim()) body.bank_name = f.bank_name.trim();
    return body;
  }
  const body: Record<string, unknown> = { connection: f.connection, issue: true };
  if (f.account.trim()) body.account = f.account.trim();
  if (f.bvn.trim()) body.bvn = f.bvn.trim();
  if (f.nin.trim()) body.nin = f.nin.trim();
  return body;
}

/** What an issuer needs, in words, for its choice. */
export function issuerWords(i: AccountIssuer): string {
  const needs: string[] = [];
  if (i.needs_email) needs.push("the customer's email");
  if (i.needs_bvn) needs.push("their BVN or NIN (typed in, never kept)");
  if (i.needs_account) needs.push("the wallet the number pays into");
  const name = i.names_itself ? `${i.rail} names the account itself` : "named for your business and the customer";
  return `${i.rail} (${i.connection}): ${name}${needs.length > 0 ? `; needs ${needs.join(", ")}` : ""}.`;
}

/** An account in words: "9900000001 · Wema Bank (iswallet/main), reference tsk_ab12". */
export function accountWords(a: CustomerAccount): string {
  return `${a.account_number}${a.bank_name ? ` · ${a.bank_name}` : ""} (${a.channel}), reference ${a.reference}`;
}
