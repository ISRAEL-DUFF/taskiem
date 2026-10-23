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
}

export interface InvoiceLine {
  description: string;
  quantity: number;
  unit_kobo: number;
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
  };
}

/** Why a void would be refused here, or null. */
export function voidProblem(reason: string): string | null {
  if (!reason.trim()) return "Say why it is void. The customer's page shows the reason.";
  if (reason.trim().length > INVOICE_LIMITS.note) return `The reason is at most ${INVOICE_LIMITS.note} characters.`;
  return null;
}

// --- sending ---

/** What Send did, in words: the channels it queued, and SMS held back by the plan. */
export function sentLines(s: Pick<InvoiceSent, "queued" | "sms_over_allowance">): string[] {
  const q = s.queued ?? [];
  const out: string[] = [];
  const by = [q.includes("sms") ? "SMS" : "", q.includes("email") ? "email" : ""].filter(Boolean);
  out.push(by.length ? `Sent by ${by.join(" and ")}.` : "Nothing was sent: the customer has no phone number or email we can use.");
  if (s.sms_over_allowance) out.push("SMS not sent: plan allowance used.");
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

/** One open suggestion: money received that may be for an invoice (MoneySuggestion). */
export interface MoneySuggestion {
  id: string;
  entry_id: string;
  invoice_id: string;
  number: string;
  score: number;
  reasons: string[] | null;
  status: string;
  amount_kobo: number | null;
  channel: string;
  reference: string;
  received_at: string;
  kind: string;
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
      g = { entry_id: s.entry_id, amount_kobo: s.amount_kobo, channel: s.channel, reference: s.reference, received_at: s.received_at, kind: s.kind, candidates: [] };
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
  channel: string;
  reference: string;
  received_at: string;
  kind: string;
}

/** Unmatched payments as To match cards, with nothing suggested. */
export function unmatchedPayments(list: UnmatchedMoney[] | null | undefined): WaitingPayment[] {
  return (list ?? []).map((u) => ({ ...u, candidates: [] }));
}

/** An amount that may be missing, in words. */
export const amountWords = (kobo: number | null | undefined) => (kobo === null || kobo === undefined ? "Amount not known" : naira(kobo));

/** The invoices a payment can be matched to: sent and not yet fully paid. */
export function openInvoices(list: CustomerInvoice[] | null | undefined): CustomerInvoice[] {
  return (list ?? []).filter((x) => x.status === "sent" || x.status === "part_paid" || x.status === "overdue");
}

/** What the To match view says after a match. */
export function matchedLine(amountKobo: number | null, number: string): string {
  return `${amountWords(amountKobo)} matched to ${number}. If that is wrong, undo it on the invoice.`;
}

/** What the To match view says after "Not this one". */
export function rejectedLine(number: string): string {
  return `${number} will not be suggested for this payment again.`;
}
