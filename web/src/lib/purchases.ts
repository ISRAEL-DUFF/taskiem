// Suppliers and purchase orders (docs/stock.md "Suppliers and purchase
// orders"; /v1/stock/suppliers, /v1/stock/purchases): the API's shapes, how
// a supplier and an order read in a row, and the forms.

import { whenLabel } from "./bookings";
import { feeInput, feeKobo } from "./orders";
import { quantity } from "./stock";
import { formatPrice } from "./storefront";

export interface Supplier {
  id: string;
  name: string;
  phone: string;
  /** Where an order goes: the chat app they tapped in on, or sms; link
   * when the supplier is a linked business on Taskiem. */
  reached: "whatsapp" | "telegram" | "sms" | "link";
  /** The link with the supplier's business, when it is one. */
  link_id?: string | null;
  owed_kobo: number;
  /** Their orders not yet received or cancelled. */
  open: number;
  invited_at: string | null;
  created_at: string;
  retired_at: string | null;
}

export interface StockSettings {
  /** Orders above it need a second person to send them. */
  purchase_limit_kobo: number | null;
  /** A count's loss above it, at cost, is a leak; null: ₦5,000. */
  count_limit_kobo?: number | null;
}

export interface SuppliersView {
  suppliers: Supplier[];
  settings: StockSettings;
}

export interface PurchaseLine {
  item_id: string;
  item: string;
  unit?: string;
  quantity: number;
  cost_kobo: number;
  received: number;
  /** What a linked supplier says it delivered. */
  delivered?: number;
}

export type PurchaseStatus =
  | "drafted"
  | "sent"
  | "confirmed"
  | "part_received"
  | "received"
  | "cancelled";

export interface Purchase {
  id: string;
  reference: string;
  supplier_id: string;
  supplier: string;
  lines: PurchaseLine[];
  total_kobo: number;
  status: PurchaseStatus;
  note?: string;
  drafted_by: string;
  approved_by: string | null;
  sent_at: string | null;
  confirmed_at: string | null;
  confirmed_how: "supplier" | "owner" | null;
  /** The supplier's Change, in their words. */
  counter: string | null;
  counter_at: string | null;
  received_at: string | null;
  cancelled_at: string | null;
  received_kobo: number;
  paid_kobo: number;
  owed_kobo: number;
  created_at: string;
  /** Sent to a linked supplier through this link; the supplier's side,
   * mirrored, and our own cancel request and Not all came. */
  link_id?: string;
  seller_status?: "received" | "changed" | "accepted" | "ready" | "dispatched" | "delivered" | "declined" | "cancelled" | null;
  seller_note?: string;
  change?: PurchaseLine[];
  delivered_at?: string;
  cancel_asked_at?: string;
  short_at?: string;
}

/** An order's status in a word or two, and its badge. */
export const STATUS: Record<PurchaseStatus, { label: string; tone: string }> = {
  drafted: { label: "Draft", tone: "muted" },
  sent: { label: "Sent", tone: "info" },
  confirmed: { label: "Confirmed", tone: "info" },
  part_received: { label: "Part received", tone: "warn" },
  received: { label: "Received", tone: "ok" },
  cancelled: { label: "Cancelled", tone: "muted" },
};

/** Where an order reaches a supplier. */
export function reachedLine(s: Supplier): string {
  if (s.reached === "whatsapp") return "Gets orders on WhatsApp";
  if (s.reached === "telegram") return "Gets orders on Telegram";
  if (s.reached === "link") return "Linked on Taskiem: orders go straight to their business";
  return "Gets orders by text message until they tap the link";
}

/** A supplier underneath their name. */
export function supplierSub(s: Supplier): string {
  const parts = [s.phone, reachedLine(s)];
  if (s.open > 0)
    parts.push(s.open === 1 ? "1 open order" : `${s.open} open orders`);
  if (s.owed_kobo > 0) parts.push(`you owe ${formatPrice(s.owed_kobo)}`);
  return parts.join(" · ");
}

/** An order's items: "46 cartons Indomie, 17 bags Rice". */
export const purchaseItems = (p: Purchase) =>
  p.lines.map((l) => `${quantity(l.quantity, l.unit)} ${l.item}`).join(", ");

/** An order underneath its title: its items, when, what the supplier said, what is owed. */
export function purchaseSub(p: Purchase): string {
  const parts = [purchaseItems(p)];
  const when = p.received_at ?? p.confirmed_at ?? p.sent_at ?? p.created_at;
  parts.push(whenLabel(when));
  if (p.status === "confirmed" || p.confirmed_how)
    parts.push(
      p.confirmed_how === "supplier"
        ? "the supplier confirmed"
        : "marked confirmed",
    );
  if (p.status === "part_received") {
    const left = p.lines.filter((l) => l.received < l.quantity).length;
    parts.push(left === 1 ? "1 item still to come" : `${left} items to come`);
  }
  if (p.owed_kobo > 0) parts.push(`owed ${formatPrice(p.owed_kobo)}`);
  else if (p.paid_kobo > 0) parts.push("paid");
  return parts.join(" · ");
}

/** What the order's buttons may do in its status. */
export function purchaseActions(p: Purchase) {
  return {
    send: p.status === "drafted",
    confirm: p.status === "sent" && !p.link_id,
    receive:
      p.status === "sent" ||
      p.status === "confirmed" ||
      p.status === "part_received",
    pay: p.status !== "drafted" && p.status !== "cancelled",
    cancel:
      (p.status === "drafted" || p.status === "sent" || p.status === "confirmed") &&
      !(p.link_id && (p.cancel_asked_at || p.seller_status === "delivered")),
  };
}

// --- the forms ---

export interface SupplierForm {
  name: string;
  phone: string;
  items: string[];
}

/** Why a supplier will be refused, or null. */
export function supplierProblem(
  f: SupplierForm,
  adding: boolean,
): string | null {
  if (f.name.trim() === "") return "Give the supplier a name.";
  if (f.name.trim().length > 80) return "A name is up to 80 characters.";
  if (adding && !/^\+?[\d\s-]{10,16}$/.test(f.phone.trim()))
    return "Enter a mobile number, like 0803 123 4567.";
  return null;
}

/** How many came of each line, as the form holds them. */
export type ReceiveForm = Record<string, string>;

/** The lines still to come, filled with what is left of each. */
export const receiveForm = (p: Purchase): ReceiveForm =>
  Object.fromEntries(
    p.lines.map((l) => [
      l.item_id,
      String(Math.max(0, l.quantity - l.received)),
    ]),
  );

const whole = (v: string) => (/^\d+$/.test(v.trim()) ? Number(v.trim()) : NaN);

/** Why a receipt will be refused, or null. */
export function receiveProblem(f: ReceiveForm): string | null {
  const values = Object.values(f);
  if (values.some((v) => v.trim() !== "" && Number.isNaN(whole(v))))
    return "How many came is a whole number, like 20.";
  if (!values.some((v) => whole(v) > 0))
    return "Say how many came of at least one item.";
  return null;
}

/** The POST /v1/stock/purchases/{id}/receive body. */
export const receiveRequest = (f: ReceiveForm) => ({
  lines: Object.entries(f)
    .filter(([, v]) => whole(v) > 0)
    .map(([item_id, v]) => ({ item_id, quantity: whole(v) })),
});

/** Why a payment will be refused, or null. */
export function payProblem(amount: string): string | null {
  const k = feeKobo(amount);
  if (amount.trim() === "" || Number.isNaN(k) || k <= 0)
    return "Enter the amount paid, like 425000.";
  return null;
}

/** The limit as the form shows it: "" for none. */
export const limitInput = (s: StockSettings) =>
  s.purchase_limit_kobo === null ? "" : feeInput(s.purchase_limit_kobo);

/** The PUT /v1/stock/settings body; NaN in it when the limit is not an amount. */
export const limitRequest = (v: string): StockSettings => ({
  purchase_limit_kobo: v.trim() === "" ? null : feeKobo(v),
});
