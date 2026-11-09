// The card frame (docs/spec/chat-tools.md §3): what a document card shows
// on its top line and its progress, from the event it carries.

import { isDelivery, type DocCard } from "./linkchat";

export type Tone = "lime" | "warn" | "danger" | "plain";

export interface CardStatus {
  /** The pill: "Accepted", "Queried". */
  word: string;
  tone: Tone;
  /** An order's place on Placed, Accepted, On the way, Delivered (0 to 3). */
  step?: number;
}

export const ORDER_STEPS = ["Placed", "Accepted", "On the way", "Delivered"];
/** A delivery's steps (docs/spec/chat-riders.md): the rider's and the business's card alike. */
export const DELIVERY_STEPS = [
  "Offered",
  "Accepted",
  "Picked up",
  "Arrived",
  "Delivered",
];

/** The steps a card's progress runs on. */
export function stepsFor(card: Pick<DocCard, "doc">): string[] {
  return isDelivery(card) ? DELIVERY_STEPS : ORDER_STEPS;
}

const DELIVERY: Record<string, CardStatus> = {
  offered: { word: "Offered", tone: "warn", step: 0 },
  accepted: { word: "Accepted", tone: "lime", step: 1 },
  picked_up: { word: "Picked up", tone: "lime", step: 2 },
  arrived: { word: "At the door", tone: "lime", step: 3 },
  delivered: { word: "Delivered", tone: "lime", step: 4 },
  // Phase 4: the business confirmed it (the fee is on its way) and paid it.
  confirmed: { word: "Confirmed", tone: "lime", step: 4 },
  paid: { word: "Fee paid", tone: "lime", step: 4 },
  cant: { word: "Passed", tone: "danger" },
  passed: { word: "Ran out", tone: "plain" },
  withdrawn: { word: "Withdrawn", tone: "plain" },
  released: { word: "Taken back", tone: "warn" },
};

const ORDER: Record<string, CardStatus> = {
  ordered: { word: "New", tone: "lime", step: 0 },
  offered: { word: "Offer", tone: "warn", step: 0 },
  countered: { word: "Counter", tone: "warn", step: 0 },
  proposed: { word: "Draft", tone: "plain", step: 0 },
  confirmed: { word: "Placed", tone: "plain", step: 0 },
  auto_changed: { word: "Changed", tone: "warn", step: 0 },
  change: { word: "Change asked", tone: "warn", step: 0 },
  accept: { word: "Accepted", tone: "lime", step: 1 },
  auto_accepted: { word: "Accepted", tone: "lime", step: 1 },
  change_accepted: { word: "Accepted", tone: "lime", step: 1 },
  keep: { word: "Kept", tone: "lime", step: 1 },
  ready: { word: "Ready", tone: "lime", step: 1 },
  dispatched: { word: "On the way", tone: "lime", step: 2 },
  delivered: { word: "Delivered", tone: "lime", step: 3 },
  received: { word: "Received", tone: "lime", step: 3 },
  agree: { word: "Count agreed", tone: "lime", step: 3 },
  short: { word: "Not all came", tone: "warn", step: 3 },
  decline: { word: "Declined", tone: "danger" },
  cancel: { word: "Cancelled", tone: "danger" },
  cancel_agreed: { word: "Cancelled", tone: "danger" },
  cancelled_by_buyer: { word: "Cancelled", tone: "danger" },
  cancel_asked: { word: "Cancel asked", tone: "warn" },
};

const INVOICE: Record<string, CardStatus> = {
  bill: { word: "To pay", tone: "lime" },
  declared: { word: "Paid, they say", tone: "plain" },
  queried: { word: "Queried", tone: "warn" },
  resolved: { word: "Settled", tone: "lime" },
  reminder: { word: "Reminder", tone: "warn" },
};

/** The card's pill and progress. */
export function cardStatus(card: DocCard, now = Date.now()): CardStatus {
  if (isDelivery(card))
    return (
      DELIVERY[card.event] ?? {
        word: card.event.replaceAll("_", " "),
        tone: "plain",
      }
    );
  const order = card.doc === "order" || card.doc === "purchase";
  // A deal no one answered in time (phase 7): nothing more happens to it.
  if (
    order &&
    card.deal?.until &&
    ["offered", "countered", "change"].includes(card.event) &&
    new Date(card.deal.until).getTime() <= now
  )
    return { word: "Lapsed", tone: "plain" };
  // A change that answers an offer is the seller's counter.
  if (order && card.event === "change" && card.was_kobo !== undefined)
    return { word: "Counter", tone: "warn", step: 0 };
  const s = (order ? ORDER : INVOICE)[card.event];
  return s ?? { word: card.event.replaceAll("_", " "), tone: "plain" };
}

/** The card's kicker: "ORDER · P-7QX4KD". */
export function cardKicker(card: DocCard): string {
  const word = isDelivery(card)
    ? "Delivery"
    : card.doc === "order" || card.doc === "purchase"
      ? "Order"
      : "Invoice";
  return `${word} · ${card.reference}`;
}

/** Whether a card is a change we made ourselves that needs nothing more:
 * shown as a diary line, not a card (a new document is always a card). */
export function isDiary(card: DocCard, mine: boolean): boolean {
  return (
    mine &&
    card.event !== "ordered" &&
    card.event !== "offered" &&
    card.event !== "bill" &&
    card.event !== "proposed"
  );
}
