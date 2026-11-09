// Orders from customers' messages (docs/spec/chat-five-more-flows.md,
// phase 2): what Taskiem read from a customer's message, as a card in their
// thread, for staff to answer, change, ask about and confirm. Pure helpers,
// tested on their own.

export interface OrderChoice {
  item_id: string;
  name: string;
  price_kobo?: number;
}

export interface OrderQuestion {
  kind: "which" | "size" | "how_many";
  text: string;
  choices?: OrderChoice[];
}

export interface OrderLine {
  /** The line, for as long as the order lives. */
  id?: string;
  /** The customer's own words for it. */
  said?: string;
  /** None: nothing in the catalogue was matched (kept as written). */
  item_id?: string;
  name?: string;
  quantity?: number;
  price_kobo?: number;
  no_price?: boolean;
  /** Not on sale now, or fewer left than asked. */
  out?: boolean;
  left?: number;
  ask?: OrderQuestion;
}

export type OrderPay = "transfer" | "on_delivery" | "later";

export interface OrderDraft {
  id: string;
  chat_id: string;
  message_id?: string;
  status: "open" | "asked" | "confirmed" | "dropped";
  lines: OrderLine[];
  fulfilment?: "delivery" | "pickup";
  address?: string;
  when?: string;
  note?: string;
  zone?: string;
  delivery_kobo: number;
  pay?: OrderPay;
  items_kobo: number;
  total_kobo: number;
  questions: number;
  ready: boolean;
  /** Why it cannot be confirmed yet, in a sentence. */
  waits?: string;
  version: number;
  reference?: string;
  order_status?: string;
  by_reader: boolean;
  asked_at?: string;
  created_at: string;
  updated_at: string;
}

export interface PickItem {
  id: string;
  name: string;
  price_kobo?: number;
  left?: number;
}

export interface OrderZone {
  name: string;
  fee_kobo: number;
}

export interface OrdersOfChat {
  orders: OrderDraft[];
  zones: OrderZone[];
  pay_page: boolean;
  items: PickItem[];
}

/** The order being worked on (one at a time), if any. */
export const workingOn = (list: OrderDraft[] | undefined) =>
  list?.find((d) => d.status === "open" || d.status === "asked");

/** The latest order confirmed, shown under the one being worked on. */
export const lastConfirmed = (list: OrderDraft[] | undefined) =>
  [...(list ?? [])].reverse().find((d) => d.status === "confirmed");

/** The card's pill: where the order stands, in plain words. */
export function orderPill(d: OrderDraft): {
  word: string;
  tone: "lime" | "warn" | "danger" | "";
} {
  if (d.status === "asked") return { word: "Waiting for them", tone: "" };
  if (d.status === "dropped") return { word: "Dropped", tone: "" };
  if (d.status === "open") {
    if (d.questions > 0)
      return {
        word: d.questions === 1 ? "1 question" : `${d.questions} questions`,
        tone: "warn",
      };
    return d.ready
      ? { word: "Ready to confirm", tone: "lime" }
      : { word: "Needs you", tone: "warn" };
  }
  switch (d.order_status) {
    case "awaiting_payment":
      return { word: "Waiting for payment", tone: "warn" };
    case "paid":
      return { word: "Paid", tone: "lime" };
    case "accepted":
      return { word: "Confirmed", tone: "lime" };
    case "dispatched":
    case "on_the_way":
      return { word: "On the way", tone: "lime" };
    case "arrived":
      return { word: "Arrived", tone: "lime" };
    case "completed":
      return { word: "Delivered", tone: "lime" };
    case "expired":
      return { word: "Not paid in time", tone: "danger" };
    case "cancelled":
    case "declined":
      return { word: "Cancelled", tone: "danger" };
    default:
      return { word: "Confirmed", tone: "lime" };
  }
}

/** How the customer pays, as the card's choice says it. */
export const PAY_WORDS: Record<OrderPay, string> = {
  transfer: "They pay now, by bank transfer (we send them the page)",
  on_delivery: "They pay when it arrives",
  later: "I'll arrange payment with them",
};

/** The ways to pay this order can offer: the page only when the business
 * has one; at the door only for a delivery. */
export function payChoices(
  d: Pick<OrderDraft, "fulfilment">,
  payPage: boolean,
): OrderPay[] {
  const out: OrderPay[] = [];
  if (payPage) out.push("transfer");
  if (d.fulfilment === "delivery") out.push("on_delivery");
  out.push("later");
  return out;
}

/** The way it is paid now: as set, else the first choice. */
export function payNow(
  d: Pick<OrderDraft, "fulfilment" | "pay">,
  payPage: boolean,
): OrderPay {
  const choices = payChoices(d, payPage);
  return d.pay && choices.includes(d.pay) ? d.pay : (choices[0] ?? "later");
}

/** What a line is, for its row: "2 × Jollof rice plate", or the
 * customer's words when nothing was matched. */
export function lineWords(l: OrderLine): string {
  if (!l.item_id) return `“${l.said ?? ""}”`;
  return `${l.quantity && l.quantity > 0 ? `${l.quantity} × ` : ""}${l.name ?? ""}`;
}

/** What a line comes to, or "" when it has no item, quantity or price. */
export function lineKobo(l: OrderLine): number | undefined {
  if (!l.item_id || !l.quantity || l.no_price) return undefined;
  return (l.price_kobo ?? 0) * l.quantity;
}

/** A note under a line: why it waits, or what is short. */
export function lineNote(l: OrderLine): string {
  if (!l.item_id && !l.ask)
    return "Not in your catalogue. Pick the item, or remove it.";
  if (l.no_price) return "No price in your catalogue: give one.";
  if (l.out)
    return l.left !== undefined && l.left > 0
      ? `Only ${l.left} left.`
      : "Out of stock.";
  return "";
}

/** "From their message" or "Started here", for the card's kicker. */
export const orderSource = (d: OrderDraft) =>
  d.by_reader ? "Order from their message" : "Order";
