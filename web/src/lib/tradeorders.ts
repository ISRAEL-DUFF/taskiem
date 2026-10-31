// Orders between linked businesses (business links L2;
// docs/spec/business-links.md §7): a linked buyer's purchase order as an
// order in the seller's business (GET /v1/trade-orders), and the seller's
// side of it as the buyer's purchase order shows it.

import type { Purchase } from "./purchases";

export type TradeStatus = "received" | "changed" | "accepted" | "ready" | "dispatched" | "delivered" | "declined" | "cancelled";

export interface TradeLine {
  item: string;
  unit?: string;
  quantity: number;
  price_kobo: number;
  /** What the seller delivered of it. */
  delivered?: number;
}

export interface TradeOrder {
  id: string;
  link: string;
  buyer: string;
  buyer_name: string;
  /** The buyer's purchase order reference. */
  reference: string;
  lines: TradeLine[];
  total_kobo: number;
  note?: string;
  status: TradeStatus;
  /** Our proposed lines, waiting for the buyer. */
  change?: TradeLine[];
  seller_note?: string;
  /** The buyer asks to cancel it. */
  cancel_asked_at?: string;
  /** Not all came: the buyer's count of each line. */
  short?: number[];
  short_at?: string;
  receipt_at?: string;
  short_agreed_at?: string;
  invoice_id?: string;
  invoice_at: "accepted" | "delivered";
  accepted_at?: string;
  delivered_at?: string;
  created_at: string;
  updated_at: string;
}

type Tone = "ok" | "warn" | "info" | "muted" | "danger";

/** An order's status in a word or two, with its tone, as either side says it. */
export function tradeStatus(s: TradeStatus): { label: string; tone: Tone } {
  switch (s) {
    case "received":
      return { label: "New", tone: "warn" };
    case "changed":
      return { label: "Change asked", tone: "info" };
    case "accepted":
      return { label: "Accepted", tone: "info" };
    case "ready":
      return { label: "Ready", tone: "info" };
    case "dispatched":
      return { label: "On its way", tone: "info" };
    case "delivered":
      return { label: "Delivered", tone: "ok" };
    case "declined":
      return { label: "Declined", tone: "muted" };
    default:
      return { label: "Cancelled", tone: "muted" };
  }
}

/** The steps the seller may take on an order in its status. */
export function sellerSteps(o: Pick<TradeOrder, "status" | "cancel_asked_at" | "short_at" | "short_agreed_at">) {
  const live = o.status === "accepted" || o.status === "ready" || o.status === "dispatched";
  return {
    accept: o.status === "received",
    change: o.status === "received" || o.status === "changed",
    decline: o.status === "received" || o.status === "changed",
    ready: o.status === "accepted",
    dispatched: o.status === "accepted" || o.status === "ready",
    delivered: live,
    cancel: live,
    keep: live && !!o.cancel_asked_at,
    agree: o.status === "delivered" && !!o.short_at && !o.short_agreed_at,
  };
}

/** What still needs the seller, in words; empty when nothing does. */
export function sellerAsk(o: TradeOrder): string {
  if (o.status === "received") return "Accept it, change it, or decline it.";
  if (o.cancel_asked_at) return `${o.buyer_name} asks to cancel it. Cancel it, or keep it.`;
  if (o.status === "delivered" && o.short_at && !o.short_agreed_at) return `${o.buyer_name} says not all of it came. Agree their count, or talk to them.`;
  if (o.status === "changed") return `Waiting for ${o.buyer_name} to accept your change.`;
  return "";
}

/** The seller's side of a purchase order to a linked supplier, in words. */
export function sellerLine(p: Pick<Purchase, "supplier" | "seller_status" | "link_id" | "cancel_asked_at">): string {
  if (!p.link_id) return "";
  switch (p.seller_status) {
    case undefined:
    case null:
      return `On its way to ${p.supplier} on Taskiem.`;
    case "received":
      return p.cancel_asked_at ? "" : `${p.supplier} has it, and has not answered yet.`;
    case "changed":
      return `${p.supplier} asks to change it.`;
    case "accepted":
      return p.cancel_asked_at ? `You asked ${p.supplier} to cancel it.` : `${p.supplier} accepted it.`;
    case "ready":
      return `${p.supplier} has it ready.`;
    case "dispatched":
      return `${p.supplier} sent it on its way.`;
    case "delivered":
      return `${p.supplier} delivered it.`;
    case "declined":
      return `${p.supplier} declined it.`;
    default:
      return `${p.supplier} cancelled it.`;
  }
}

/** What the buyer may do on a purchase order to a linked supplier. */
export function buyerSteps(p: Pick<Purchase, "status" | "seller_status" | "link_id" | "cancel_asked_at" | "short_at" | "change">) {
  const open = p.status === "sent" || p.status === "confirmed";
  const delivered = p.seller_status === "delivered";
  return {
    acceptChange: !!p.link_id && open && p.seller_status === "changed" && !!p.change,
    cancel: !!p.link_id && open && !p.cancel_asked_at && p.seller_status !== "delivered",
    answerDelivery: !!p.link_id && delivered && !p.short_at && p.status !== "cancelled",
  };
}

/** The order's total at a set of lines. */
export const linesTotal = (lines: Pick<TradeLine, "quantity" | "price_kobo">[]) => lines.reduce((n, l) => n + l.quantity * l.price_kobo, 0);
