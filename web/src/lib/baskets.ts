// Ordering together (docs/spec/people-chatting.md §10.3, §11 A and C;
// people-chatting-5b.md C with the founder's C1, C2, C3): the shapes of
// GET /v1/me/baskets/{id} and the words the basket's screens say.

import type { DeliveryProgress } from "./personchat";

export interface BasketLineOut {
  id: string;
  item_id: string;
  name: string;
  quantity: number;
  price_kobo: number;
  added_price_kobo: number;
  price_changed: boolean;
  line_kobo: number;
  available: boolean;
  few_left?: number;
}

export interface BasketShare {
  items_kobo: number;
  delivery_kobo: number;
  amount_kobo: number;
  paid: boolean;
  paid_at?: string;
  /** Paid after the order could take it: the business sends it back. */
  refund_due: boolean;
  card_sent: boolean;
}

/** Someone in the basket's group or chat now. */
export interface BasketMember {
  person_id: string;
  name: string;
  you: boolean;
}

export interface BasketPerson {
  person_id: string;
  name: string;
  you: boolean;
  lines: BasketLineOut[];
  items_kobo: number;
  share?: BasketShare;
}

export interface BasketShop {
  open: boolean;
  closed?: string;
  pickup: boolean;
  delivery: boolean;
  zones: { name: string; fee_kobo: number }[];
  payment: string;
  each_offered: boolean;
}

export interface BasketOrder {
  reference: string;
  status: string;
  line: string;
  total_kobo: number;
  paid_kobo: number;
  left_kobo: number;
  payment_state: "unpaid" | "part_paid" | "paid";
  payable_now: boolean;
  pay_by?: string;
  delivery?: DeliveryProgress;
}

export type BasketState = "open" | "closed" | "placing" | "placed" | "cancelled";

export interface Basket {
  id: string;
  business_id: string;
  business: string;
  /** The person reading it: compare with placed_by and holder_id. */
  you_id: string;
  /** Who is in the group or the chat now (with items or not). */
  members: BasketMember[];
  where: "group" | "person";
  group_id?: string;
  chat_id?: string;
  state: BasketState;
  starter_id: string;
  holder_id: string;
  holder: string;
  you_hold: boolean;
  holder_gone: boolean;
  fulfilment?: "pickup" | "delivery";
  zone?: string;
  payment?: "all" | "each";
  shop?: BasketShop;
  people: BasketPerson[];
  items_kobo: number;
  delivery_kobo: number;
  total_kobo: number;
  problems: string[];
  order?: BasketOrder;
  placed_by?: string;
  created_at: string;
  closed_at?: string;
  placed_at?: string;
}

/** The live connection's `basket` event. */
export interface LiveBasket {
  basket: Basket;
}

/** The basket's state in a few words, and how it is marked. */
export function basketStateWords(b: Pick<Basket, "state" | "order">): { label: string; tone: "lime" | "warn" | "plain" | "danger" } {
  switch (b.state) {
    case "open":
      return { label: "Open: add what you want", tone: "lime" };
    case "closed":
      return { label: "Closed for placing", tone: "warn" };
    case "placing":
      return { label: "Being placed", tone: "warn" };
    case "cancelled":
      return { label: "Cancelled", tone: "danger" };
  }
  return { label: b.order?.line || "Placed", tone: "plain" };
}

/** Who holds it, said to the person. */
export function holderWords(b: Pick<Basket, "you_hold" | "holder" | "holder_gone" | "state">): string {
  if (b.state === "placed" || b.state === "cancelled") return "";
  if (b.holder_gone) return "The one who held this basket is no longer here: anyone can take it over.";
  return b.you_hold ? "You hold this basket: you close it and place the order." : `${b.holder} holds this basket: they close it and place the order.`;
}

/** What the person may do now. */
export function basketCan(b: Basket) {
  const changeable = b.state === "open" || b.state === "closed";
  const placed = b.state === "placed";
  const me = b.people.find((p) => p.you);
  // The placer by their own id, whether or not they have items.
  const placer = placed && !!b.placed_by && b.placed_by === b.you_id;
  const due = (s?: BasketShare) => !!s && !s.paid && !s.refund_due && s.amount_kobo >= 100;
  return {
    addMine: b.state === "open" && !!b.shop?.open,
    holder: changeable && b.you_hold,
    close: b.state === "open" && b.you_hold && b.people.length > 0,
    reopen: b.state === "closed" && b.you_hold,
    place: b.state === "closed" && b.you_hold,
    takeOver: (changeable || b.state === "placing") && b.holder_gone,
    /** Whom the holder may hand it to: anyone in the group or chat now. */
    handTo: (b.members ?? []).filter((m) => !m.you),
    payMine: placed && b.payment === "each" && due(me?.share) && !!b.order?.payable_now,
    payOrder: placer && b.payment === "all" && !!b.order?.payable_now,
    payFor: placed && b.payment === "each" && placer && !!b.order?.payable_now ? b.people.filter((p) => !p.you && due(p.share)) : [],
    shareCards: placer && b.payment === "all" && b.people.some((p) => !p.you && p.share && !p.share.card_sent && p.share.amount_kobo >= 100),
  };
}

/** "Each pays their share" said, or why only "I'll pay all". */
export function payChoiceWords(each: boolean, business: string): { all: string; each: string; note: string } {
  return {
    all: "I'll pay all",
    each: "Each pays their share",
    note: each
      ? "Each pays the business for their own items and an equal part of the delivery. You can pay what's left for anyone who hasn't."
      : `${business} takes no payment in the app, so you pay for everyone. You can then send each a "Your share" card.`,
  };
}

/** A member's share in words: "₦4,833.33: items ₦4,500 and delivery ₦333.33". */
export function shareWords(s: BasketShare, naira: (k: number) => string): string {
  const parts = [`${naira(s.amount_kobo)}: items ${naira(s.items_kobo)}`];
  if (s.delivery_kobo > 0) parts.push(`delivery ${naira(s.delivery_kobo)}`);
  return parts.join(" and ");
}

/** The basket card's line under its title in a chat. */
export function basketCardLine(b: Pick<Basket, "people" | "total_kobo" | "state" | "order">, naira: (k: number) => string): string {
  const n = b.people.length;
  const who = n === 0 ? "Nothing in it yet" : n === 1 ? "1 person" : `${n} people`;
  return `${who} · ${naira(b.order?.total_kobo ?? b.total_kobo)}`;
}

/** The member's own lines, as PUT …/mine takes them. */
export function myLines(b: Pick<Basket, "people">): { item_id: string; name: string; price_kobo: number; quantity: number }[] {
  const me = b.people.find((p) => p.you);
  return (me?.lines ?? []).map((l) => ({ item_id: l.item_id, name: l.name, price_kobo: l.price_kobo, quantity: l.quantity }));
}

/** The basket after a live event, if it is this one. */
export function basketWithLive(cur: Basket | undefined, e: LiveBasket): Basket | undefined {
  return cur && cur.id === e.basket.id ? e.basket : cur;
}
