// The storefront's forms and the orders they bring (Phase 5, S3;
// GET/PUT /v1/storefront/actions, GET /v1/storefront/orders): the API's
// shapes, setting a form up from its template, and how an order reads.
// Bookings (S4) are orders of kind booking, with a service and a time.

import { whenLabel } from "./bookings";
import { formatPrice } from "./storefront";

export type FormKind = "order" | "enquiry" | "callback" | "booking";
export type PaymentMode = "after_accept" | "at_order" | "none";

export interface ActionOptions {
  /** The Paystack connection payments use (default main). */
  connection?: string;
  pickup?: boolean;
  delivery?: boolean;
  delivery_note?: string;
  /** A booking's online part of the price, 1 to 100 per cent; unset is all of it. */
  deposit_percent?: number;
  /** What a customer cancelling a paid booking gets back. */
  cancel_refund?: "full" | "keep_deposit";
}

/** One of the storefront's forms (StorefrontAction). */
export interface StorefrontAction {
  kind: FormKind;
  enabled: boolean;
  label: string;
  payment: PaymentMode;
  options: ActionOptions;
  /** The workflow deployed in prod for this form. */
  workflow_id: string | null;
}

export interface OrderLine {
  id?: string;
  name?: string;
  quantity?: number;
  price_kobo?: number;
  line_kobo?: number;
}

export type OrderStatus =
  | "received"
  | "awaiting_payment"
  | "paid"
  | "accepted"
  | "declined"
  | "expired"
  | "completed"
  | "cancelled"
  | "refund_due"
  | "no_show"
  | "price_changed"
  | "refunded";

/** What an order costs at the table's prices now, waiting for the customer to agree (status price_changed). */
export interface PriceChange {
  basket: OrderLine[] | null;
  total_kobo: number;
  deposit_kobo?: number;
  /** Items no longer offered, by name. */
  gone?: string[];
}

export interface Customer {
  name?: string;
  phone?: string;
  email?: string;
  address?: string;
  message?: string;
  best_time?: string;
}

/** An order, enquiry or call-back (StorefrontOrder). */
export interface Order {
  id: string;
  reference: string;
  kind: FormKind;
  status: OrderStatus;
  customer: Customer;
  basket: OrderLine[] | null;
  total_kobo: number;
  fulfilment?: string;
  payment: PaymentMode;
  payment_url?: string;
  paid_at?: string | null;
  /** How it was paid: card (its Paystack page), transfer (the account number given for it), other (money matched to it). */
  paid_by?: "card" | "transfer" | "other";
  run_id?: string | null;
  note?: string;
  decided_by?: string;
  decided_at?: string | null;
  expires_at?: string | null;
  created_at: string;
  /** A booking's service and time. */
  service_id?: string;
  service_name?: string;
  starts_at?: string;
  ends_at?: string;
  version?: number;
  /** What a booking pays online when that is a deposit. */
  deposit_kobo?: number;
  price_change?: PriceChange | null;
}

export const FORM_KINDS: FormKind[] = [
  "order",
  "enquiry",
  "callback",
  "booking",
];

/** The limits the server holds a form's text to (engine/storefront SaveAction). */
export const ACTION_LIMITS = { label: 40, delivery_note: 120 } as const;

/** What each form is, and the default workflow its Set up button adds. */
export const FORM_INFO: Record<
  FormKind,
  { title: string; help: string; template: string; workflow: string }
> = {
  order: {
    title: "Orders",
    help: "Customers order from your catalogue.",
    template: "storefront-order",
    workflow: "Storefront orders",
  },
  enquiry: {
    title: "Questions",
    help: "Customers send you a question.",
    template: "storefront-enquiry",
    workflow: "Storefront questions",
  },
  callback: {
    title: "Call-backs",
    help: "Customers ask you to call them back.",
    template: "storefront-callback",
    workflow: "Storefront call-backs",
  },
  booking: {
    title: "Bookings",
    help: "Customers book a time for one of your services.",
    template: "storefront-booking",
    workflow: "Storefront bookings",
  },
};

export const PAYMENT_CHOICES: {
  value: PaymentMode;
  label: string;
  help: string;
}[] = [
  {
    value: "after_accept",
    label: "Customer pays after you accept (recommended)",
    help: "Nobody pays for an order you cannot fill.",
  },
  {
    value: "at_order",
    label: "Customer pays when ordering",
    help: "A declined paid order needs a refund in Paystack.",
  },
  {
    value: "none",
    label: "No online payment",
    help: "Take payment yourself, in cash or by transfer.",
  },
];

/** A booking's payment choices: the same modes, said for appointments. */
export const BOOKING_PAYMENT_CHOICES: {
  value: PaymentMode;
  label: string;
  help: string;
}[] = [
  {
    value: "at_order",
    label: "Pay when booking",
    help: "The time is held until they pay. A booking you decline needs a refund in Paystack.",
  },
  {
    value: "after_accept",
    label: "Pay after I confirm",
    help: "Nobody pays for a time you cannot take.",
  },
  {
    value: "none",
    label: "Pay at the appointment",
    help: "Take payment yourself, in cash or by transfer.",
  },
];

/** The payment choices for a form. */
export const paymentChoices = (kind: FormKind) =>
  kind === "booking" ? BOOKING_PAYMENT_CHOICES : PAYMENT_CHOICES;

/** The POST /v1/templates/{id}/instantiate call that sets a form up: its
 * default template, named for the form. approver_role is left to the
 * template's default (the owner). */
export function setupRequest(kind: FormKind): {
  path: string;
  body: { params: Record<string, unknown>; name: string };
} {
  const info = FORM_INFO[kind];
  return {
    path: `/v1/templates/${encodeURIComponent(info.template)}/instantiate`,
    body: { params: {}, name: info.workflow },
  };
}

/** A form's settings as the editor holds them. */
export interface ActionForm {
  enabled: boolean;
  label: string;
  payment: PaymentMode;
  connection: string;
  pickup: boolean;
  delivery: boolean;
  deliveryNote: string;
  /** A booking's deposit in per cent; blank is the full price. */
  deposit: string;
  /** Whether a customer cancelling a paid booking keeps their deposit back. */
  keepDeposit: boolean;
}

export function actionForm(a: StorefrontAction): ActionForm {
  return {
    enabled: a.enabled,
    label: a.label,
    payment: a.payment,
    connection: a.options.connection ?? "",
    pickup: !!a.options.pickup,
    delivery: !!a.options.delivery,
    deliveryNote: a.options.delivery_note ?? "",
    deposit: a.options.deposit_percent ? String(a.options.deposit_percent) : "",
    keepDeposit: a.options.cancel_refund === "keep_deposit",
  };
}

export interface ActionRequest {
  enabled: boolean;
  label: string;
  payment: PaymentMode;
  options: ActionOptions;
}

/** The PUT /v1/storefront/actions/{kind} body: the whole form, as the
 * server replaces it. Only an order and a booking have payment and options. */
export function actionRequest(kind: FormKind, f: ActionForm): ActionRequest {
  if (kind === "booking") {
    const options: ActionOptions = {};
    if (f.payment !== "none") {
      const connection = f.connection.trim();
      if (connection) options.connection = connection;
      const deposit = Number(f.deposit.trim());
      if (
        f.deposit.trim() &&
        Number.isInteger(deposit) &&
        deposit >= 1 &&
        deposit <= 100
      )
        options.deposit_percent = deposit;
      if (f.keepDeposit) options.cancel_refund = "keep_deposit";
    }
    return {
      enabled: f.enabled,
      label: f.label.trim(),
      payment: f.payment,
      options,
    };
  }
  if (kind !== "order")
    return {
      enabled: f.enabled,
      label: f.label.trim(),
      payment: "none",
      options: {},
    };
  const options: ActionOptions = {};
  const connection = f.connection.trim();
  if (connection) options.connection = connection;
  if (f.pickup) options.pickup = true;
  if (f.delivery) options.delivery = true;
  const note = f.deliveryNote.trim();
  if (f.delivery && note) options.delivery_note = note;
  return {
    enabled: f.enabled,
    label: f.label.trim(),
    payment: f.payment,
    options,
  };
}

/** Why a form's settings will be refused, or null. */
export function actionProblem(kind: FormKind, f: ActionForm): string | null {
  if (f.label.trim().length > ACTION_LIMITS.label)
    return `The button's label is at most ${ACTION_LIMITS.label} characters.`;
  if (
    kind === "order" &&
    f.deliveryNote.trim().length > ACTION_LIMITS.delivery_note
  )
    return `The delivery note is at most ${ACTION_LIMITS.delivery_note} characters.`;
  if (kind === "order" && f.enabled && !f.pickup && !f.delivery)
    return "Choose pickup, delivery or both.";
  if (
    kind === "booking" &&
    f.payment !== "none" &&
    f.deposit.trim() &&
    !/^(100|[1-9][0-9]?)$/.test(f.deposit.trim())
  )
    return "The deposit is 1 to 100 per cent, or leave it empty for the full price.";
  return null;
}

/** A form's public address on the published page. */
export const formURL = (previewURL: string, kind: FormKind) =>
  `${previewURL.replace(/\/+$/, "")}/a/${kind}`;

// --- orders ---

export type Tone = "ok" | "info" | "warn" | "danger" | "muted";

export const ORDER_STATUS: Record<OrderStatus, { label: string; tone: Tone }> =
  {
    received: { label: "New", tone: "warn" },
    awaiting_payment: { label: "Waiting for payment", tone: "warn" },
    paid: { label: "Paid", tone: "info" },
    accepted: { label: "Accepted", tone: "info" },
    declined: { label: "Declined", tone: "muted" },
    expired: { label: "Expired", tone: "muted" },
    completed: { label: "Completed", tone: "ok" },
    cancelled: { label: "Cancelled", tone: "muted" },
    refund_due: { label: "Refund due", tone: "danger" },
    no_show: { label: "No-show", tone: "muted" },
    price_changed: {
      label: "Waiting for the customer to confirm new prices",
      tone: "warn",
    },
    refunded: { label: "Refunded", tone: "muted" },
  };

/** A status as the list shows it, with the badge's colour. */
export function orderStatus(s: string): { label: string; tone: Tone } {
  return (
    ORDER_STATUS[s as OrderStatus] ?? {
      label: s.replaceAll("_", " "),
      tone: "muted",
    }
  );
}

/** The list's filter, as the page's ?status= holds it. */
export const ORDER_FILTERS: { value: string; label: string }[] = [
  { value: "open", label: "Open" },
  { value: "all", label: "All" },
  { value: "completed", label: "Completed" },
  { value: "declined", label: "Declined" },
  { value: "expired", label: "Expired" },
  { value: "refund_due", label: "Refund due" },
  { value: "price_changed", label: "New prices to confirm" },
  { value: "cancelled", label: "Cancelled" },
  { value: "no_show", label: "No-show" },
  { value: "refunded", label: "Refunded" },
];

/** The GET /v1/storefront/orders call for a filter: "all" asks with no status. */
export function ordersPath(filter: string): string {
  const status = filter === "all" ? "" : filter || "open";
  return status
    ? `/v1/storefront/orders?${new URLSearchParams({ status })}`
    : "/v1/storefront/orders";
}

export const KIND_LABEL: Record<FormKind, string> = {
  order: "Order",
  enquiry: "Question",
  callback: "Call-back",
  booking: "Booking",
};

/** How long ago, in words: "just now", "5 min ago", "2 hours ago", "yesterday", "3 days ago", then the date. */
export function relativeAge(iso: string, now: number = Date.now()): string {
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return "";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return h === 1 ? "1 hour ago" : `${h} hours ago`;
  const d = Math.floor(h / 24);
  if (d === 1) return "yesterday";
  if (d < 7) return `${d} days ago`;
  return new Date(t).toLocaleDateString("en-GB", {
    day: "numeric",
    month: "short",
    year: "numeric",
  });
}

/** How long until, in words: "in 5 min", "in 3 hours", or "now" once past. */
export function timeLeft(iso: string, now: number = Date.now()): string {
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return "";
  const m = Math.floor((t - now) / 60000);
  if (m <= 0) return "now";
  if (m < 60) return `in ${m} min`;
  const h = Math.floor(m / 60);
  return h === 1 ? "in 1 hour" : `in ${h} hours`;
}

/** What was ordered: "2 × Jollof rice, 1 × Chapman". */
export function itemsSummary(basket: OrderLine[] | null | undefined): string {
  return (basket ?? [])
    .filter((l) => l.name)
    .map((l) => `${l.quantity ?? 1} × ${l.name}`)
    .join(", ");
}

/** What an order is for, as the list shows it: the basket, or a booking's service and time ("Haircut · Sat 24 Oct, 10:30"). */
export function orderWhat(
  o: Pick<Order, "kind" | "basket" | "service_name" | "starts_at">,
): string {
  if (o.kind === "booking")
    return [o.service_name, o.starts_at ? whenLabel(o.starts_at) : ""]
      .filter(Boolean)
      .join(" · ");
  return itemsSummary(o.basket);
}

/** An order's total as the list shows it: a booking without a price is paid on the day. */
export function orderTotal(o: Pick<Order, "kind" | "total_kobo">): string {
  if (o.kind === "booking")
    return o.total_kobo > 0 ? formatPrice(o.total_kobo) : "Price on the day";
  return o.kind === "order" ? formatPrice(o.total_kobo) : "—";
}

/** The line a price_changed order carries: the new total, and what is no longer offered. */
export function priceChangeLine(pc: PriceChange | null | undefined): string {
  if (!pc) return "";
  const gone = pc.gone?.length
    ? ` No longer offered: ${pc.gone.join(", ")}.`
    : "";
  return `New total ${formatPrice(pc.total_kobo)}.${gone}`;
}

/** How the customer pays, and whether they have. */
export function paymentSummary(
  o: Pick<Order, "payment" | "paid_at" | "kind"> &
    Partial<Pick<Order, "deposit_kobo">>,
): string {
  if (o.kind === "booking") {
    if (o.payment === "none") return "Pays at the appointment";
    const when =
      o.payment === "at_order" ? "Pays when booking" : "Pays after you confirm";
    const deposit = o.deposit_kobo
      ? ` · deposit ${formatPrice(o.deposit_kobo)}`
      : "";
    return `${when}${deposit} · ${o.paid_at ? "paid" : "not paid"}`;
  }
  if (o.kind !== "order" || o.payment === "none") return "No online payment";
  const when =
    o.payment === "at_order" ? "Pays when ordering" : "Pays after you accept";
  return `${when} · ${o.paid_at ? "paid" : "not paid"}`;
}

/** The line a refund_due order carries. */
export const refundLine = (totalKobo: number, paidBy?: Order["paid_by"]) =>
  paidBy && paidBy !== "card"
    ? `${formatPrice(totalKobo)} is due back to the customer. They paid by bank transfer, so send it back from your bank, then mark it refunded.`
    : `${formatPrice(totalKobo)} is due back to the customer. Refund it here through Paystack, or refund it yourself in Paystack.`;

/** Whether a value is the server's masked form ("A•••", "•••4567"). */
export const isMasked = (v: string | undefined) => !!v && v.includes("•••");

/** Who the customer is, as the list shows them: masked name and phone. */
export function customerLine(c: Customer): string {
  return [c.name, c.phone].filter(Boolean).join(" · ") || "—";
}

/** A phone's https://wa.me/ link: international digits, a Nigerian 0803… as 234803…; none for a masked number. */
export function whatsappLink(phone: string | undefined): string | null {
  if (!phone || isMasked(phone)) return null;
  let d = phone.replace(/\D/g, "");
  if (d.length === 11 && d.startsWith("0")) d = "234" + d.slice(1);
  return d.length >= 8 ? `https://wa.me/${d}` : null;
}

/** A phone's tel: link; none for a masked number. */
export function telLink(phone: string | undefined): string | null {
  if (!phone || isMasked(phone)) return null;
  const t = phone.replace(/[^\d+]/g, "");
  return t ? `tel:${t}` : null;
}

/** A connection a storefront form can take payments with (N3): a Paystack
 * one takes card payments and bank transfers, and is named by its name; any
 * other that opens account numbers for one payment takes bank transfers
 * only, and is named connector/name. */
export interface FormRail {
  value: string;
  label: string;
  card: boolean;
}

export function formRails(
  names: {
    connector: string;
    name: string;
    environment: string;
    status: string;
  }[],
  connectors: { id: string; name: string; capabilities?: string[] }[],
): FormRail[] {
  const out: FormRail[] = [];
  for (const n of names) {
    if (n.environment !== "prod") continue;
    const c = connectors.find((k) => k.id === n.connector);
    const card = n.connector === "paystack";
    if (!card && !(c?.capabilities ?? []).includes("collect_transfer"))
      continue;
    const service = c?.name ?? n.connector;
    const how = card ? "card or bank transfer" : "bank transfer";
    const state = n.status === "active" ? "" : `, ${n.status}`;
    out.push({
      value: card ? n.name : `${n.connector}/${n.name}`,
      label: `${n.name} (${service}: ${how}${state})`,
      card,
    });
  }
  return out.sort(
    (a, b) => Number(b.card) - Number(a.card) || a.label.localeCompare(b.label),
  );
}

/** How customers pay on a form whose connection is not Paystack's. */
export const TRANSFER_ONLY_LINE =
  "Customers pay by bank transfer, into an account number shown on their order's page. Card payments need a Paystack connection.";

/** What the storefront's Paystack connection means for customers' payments. */
export function paystackLine(
  c: { status: string; mode?: string | null } | undefined,
): string {
  if (!c) return "Connect Paystack so customers can pay.";
  if (c.status !== "active")
    return `Your Paystack connection is ${c.status}: connect it again, or customers cannot pay.`;
  if (c.mode === "test")
    return "This is a test key: payments are practice, not real money. Connect your live key before customers pay.";
  if (c.mode === "live")
    return "Live: customers pay real money into this Paystack account.";
  return "Customers pay into this Paystack account.";
}
