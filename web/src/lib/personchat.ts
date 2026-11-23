// A person's chats with businesses (docs/spec/taskiem-for-everyone.md §8,
// §9, §15): what the person's side of the API answers (GET /v1/me/chats…,
// GET /v1/chat-links/{code}, GET /v1/me/storefront-orders), and the words
// the screens build from it. Pure helpers, tested on their own.

import { ApiError } from "../api";
import type { SavedCard } from "./saved";
import { formatPrice } from "./storefront";
import { startErrorWords } from "./toolanswer";

/** A delivery as its customer follows it, once a rider holds it (nothing
 * of the rider). */
export interface DeliveryProgress {
  status: "accepted" | "picked_up" | "arrived" | "delivered";
  /** "A rider is going to collect it", "On its way to you"… */
  line: string;
  accepted_at?: string;
  picked_up_at?: string;
  arrived_at?: string;
  delivered_at?: string;
}

/** A card in a conversation: an order, its pay link, a booking (or a
 * booking offer), a product, an invoice, a delivery. The same on both
 * sides. */
export interface AppCard {
  type: "order" | "pay" | "booking" | "product" | "invoice" | "delivery" | "saved";
  /** A saved thing shared (saved objects S1), as it was when sent. */
  saved?: SavedCard;
  /** An order's reference; an invoice's number. */
  reference?: string;
  invoice_id?: string;
  /** A delivery card's progress, as it was when sent. */
  delivery?: DeliveryProgress;
  title: string;
  line?: string;
  status?: string;
  total_kobo?: number;
  item_id?: string;
  service_id?: string;
  minutes?: number;
  /** A pay link's page. */
  url?: string;
}

/** A photo, voice note or file of an app message. */
export interface AppAttachment {
  kind: "photo" | "voice" | "file";
  type: string;
  size: number;
  name?: string;
}

/** The business's opening line under its name. */
export interface ChatHours {
  open: boolean;
  line: string;
}

/** One of the person's conversations (GET /v1/me/chats). */
export interface PersonChat {
  id: string;
  business: string;
  name: string;
  last: string;
  last_mine: boolean;
  last_at: string;
  unread: number;
  muted: boolean;
  archived: boolean;
  /** You blocked the business. */
  blocked: boolean;
  /** The business is not taking your messages. */
  blocked_you: boolean;
  offers: "unasked" | "yes" | "no";
  /** Ask "Get offers from …?" now (once). */
  ask_offers: boolean;
  typing?: boolean;
  hours?: ChatHours;
  /** Said once, on starting. */
  notice?: string;
  /** One chat only: the business's photo (sizes by width). */
  photo?: { urls?: Record<string, string> };
  /** One chat only: what the business has proved (§15). */
  verified?: BusinessTrust;
  /** On a PUT: the answer to offers was not taken (they blocked you). */
  offers_refused?: boolean;
  /** Where the chat runs: the app, and WhatsApp once the person's messages
   * with the business on its own number are shown here too (§11). */
  lines?: ("app" | "whatsapp")[];
}

/** What a business has proved, for the conversation's header. */
export interface BusinessTrust {
  phone: boolean;
  business: boolean;
  payments: boolean;
  months: number;
}

/** A message of the person's copy. */
export interface PersonMessage {
  id: string;
  /** From the person. */
  mine: boolean;
  kind: "text" | "media" | "card";
  body: string;
  sender_name?: string;
  card?: AppCard;
  attachment?: AppAttachment;
  /** Marked Offer, with Stop offers under it. */
  offer?: boolean;
  /** The business asks for a transfer to an account: warn (§15). */
  warn_transfer?: boolean;
  read_at: string | null;
  created_at: string;
  /** Shown before the server answers. */
  local?: boolean;
  /** Could not be sent: why. */
  failed?: string;
  /** A photo being sent: this device's own copy. */
  localPreview?: string;
  /** In the app, or on WhatsApp with the business's own number (§11):
   * shown marked "on WhatsApp", not answered there from here. */
  line?: "app" | "whatsapp";
}

/** A business's card, for anyone with its chat link (GET /v1/chat-links/{code}). */
export interface ChatLinkCard {
  name: string;
  tagline?: string;
  area?: string;
  photo?: { urls?: Record<string, string> };
  hours?: { days: { open: string; close: string }[][]; note?: string };
  open_now?: boolean;
  takes_chats: boolean;
  shop?: string;
  code: string;
  link: string;
  words: string;
  /** Where to get the Taskiem app, when it is listed. */
  app?: string;
  /** Chat on WhatsApp, on the business's own number (§11). */
  whatsapp?: { number: string; link: string };
  /** The ways to chat the link offers, in order. */
  choices?: ("whatsapp" | "taskiem")[];
}

/** An item of their shop. */
export interface ShopItem {
  id: string;
  name: string;
  price_kobo?: number;
  description?: string;
  category?: string;
  photo?: string;
  available: boolean;
  few_left?: number;
  sizes?: ShopSize[];
}

export interface ShopSize {
  id: string;
  variant: Record<string, string>;
  price_kobo?: number;
  available: boolean;
  few_left?: number;
}

export interface ShopZone {
  name: string;
  fee_kobo: number;
}

/** Their shop (GET /v1/me/chats/{id}/shop). */
export interface TheirShop {
  items: ShopItem[];
  open: boolean;
  closed?: string;
  pickup: boolean;
  delivery: boolean;
  zones: ShopZone[];
  payment: "at_order" | "after_accept" | "none" | string;
}

/** One of the person's orders or bookings with the business. */
export interface DoorOrder {
  reference: string;
  kind: "order" | "booking";
  status: string;
  summary: string;
  total_kobo: number;
  due_kobo: number;
  service_name?: string;
  starts_at?: string;
  pay: boolean;
  /** The rider's progress, once a rider holds it. */
  delivery?: DeliveryProgress;
  created_at: string;
}

/** A storefront order made with the person's phone (GET /v1/me/storefront-orders). */
export interface PhoneOrder {
  reference: string;
  state: string;
  total_kobo: number;
  made_at: string;
  business: string;
  business_id: string;
}

/** GET /v1/me/storefront-orders: confirmed orders only; those made before
 * the phone was on the account are only counted until "These are mine". */
export interface PhoneOrders {
  orders: PhoneOrder[];
  waiting: boolean;
  waiting_count: number;
}

/** The question about orders made before the number was on the account. */
export function waitingWords(n: number): string {
  if (n === 1) return "We found 1 order made with your number before it was on your account. Is it yours?";
  return `We found ${n} orders made with your number before it was on your account. Are they yours?`;
}

/** What a business has proved, in plain words for the header:
 * "Phone checked · Payments through Taskiem · Trading 8 months". */
export function trustWords(v: BusinessTrust | undefined): string {
  if (!v) return "";
  const out: string[] = [];
  if (v.phone) out.push("Phone checked");
  if (v.business) out.push("Business checked");
  if (v.payments) out.push("Payments through Taskiem");
  out.push(tradingWords(v.months));
  return out.join(" · ");
}

/** "New on Taskiem", "Trading 1 month", "Trading 8 months", "Trading 2 years". */
export function tradingWords(months: number): string {
  if (!months || months < 1) return "New on Taskiem";
  if (months < 12) return `Trading ${months} ${months === 1 ? "month" : "months"}`;
  const y = Math.floor(months / 12);
  return `Trading ${y} ${y === 1 ? "year" : "years"}`;
}

/** Under a business's message that asks for a transfer to an account. */
export const TRANSFER_WARNING = "Pay only through Taskiem's pay links. Taskiem never asks you to send money to a personal account.";

/** The business's photo at a size fit for an avatar. */
export function photoURL(p: { urls?: Record<string, string> } | undefined): string | undefined {
  const urls = p?.urls ?? {};
  return urls["320"] ?? urls["800"] ?? Object.values(urls)[0];
}

/** A 409 that closes a conversation for good: the customer's details were
 * erased. The server's own sentence, or ours when it gave a terse one. */
export function erasedWords(status: number, message: string, side: "person" | "business"): string | null {
  if (status !== 409 || !/erased/i.test(message)) return null;
  const m = message.trim();
  if (/^[A-Z].*\.$/.test(m)) return m;
  return side === "person"
    ? "This business erased your details from its records, so this conversation is closed."
    : "This customer's details were erased, so this conversation is closed.";
}

/** Whether a send that failed may have reached the server all the same (the
 * connection dropped, or the server failed after taking it): then a retry
 * of the same words keeps its client_id, so it is not sent twice. */
export const mayHaveArrived = (status: number) => status === 0 || status >= 500;

/** A send whose answer never came: its words and client_id. */
export interface KeptSend {
  text: string;
  client: string;
}

/** The client_id for words about to be sent: the kept one when the same
 * words are sent again, else a fresh one. */
export function clientFor(kept: KeptSend | undefined, text: string, fresh: () => string): string {
  return kept && kept.text === text ? kept.client : fresh();
}

/** The person's own copy of a message's file. */
export const personFileURL = (chat: string, message: string) =>
  `/v1/me/chats/${encodeURIComponent(chat)}/files/${encodeURIComponent(message)}`;

/** The question asked once, at the first chat. */
export const offersQuestion = (business: string) => `Get offers from ${business}?`;

/** The line under an offer. */
export const stopOffersWords = (business: string) => `Stop offers from ${business}`;

/** What the box says when nothing can be written, or null. */
export function closedNote(c: Pick<PersonChat, "blocked" | "blocked_you" | "name">): string | null {
  if (c.blocked) return `You blocked ${c.name}. Unblock them to write to them.`;
  if (c.blocked_you) return `${c.name} is not taking messages from you.`;
  return null;
}

/** The line under the name: typing, else their hours. */
export function headLine(c: Pick<PersonChat, "typing" | "hours">): string {
  if (c.typing) return "typing…";
  return c.hours?.line ?? "";
}

/** The quiet line under the business's name once their WhatsApp chat
 * with the person shows here too (§11), or "". */
export function whatsappHere(c: Pick<PersonChat, "lines">): string {
  return c.lines?.includes("whatsapp") ? "Your WhatsApp chat with them is here too" : "";
}

/** What the chat link offers (§11): Chat on Taskiem, Chat on WhatsApp, or
 * both; WhatsApp only with its link, and only a wa.me link (it becomes a
 * button's href). Without choices (an older answer), Taskiem while it takes
 * chats. */
export function linkChoices(c: Pick<ChatLinkCard, "choices" | "whatsapp" | "takes_chats">): { taskiem: boolean; whatsapp: string | null } {
  const ch = c.choices ?? (c.takes_chats ? ["taskiem"] : []);
  const link = c.whatsapp?.link ?? "";
  return { taskiem: ch.includes("taskiem"), whatsapp: ch.includes("whatsapp") && link.startsWith("https://wa.me/") ? link : null };
}

/** A row's preview: what was said last, "You: " before ours. */
export function chatPreview(c: Pick<PersonChat, "last" | "last_mine" | "blocked" | "blocked_you">): string {
  if (c.blocked) return "You blocked them";
  if (!c.last) return "No messages yet";
  return `${c.last_mine ? "You: " : ""}${c.last}`;
}

/** How many unread, in words. */
export const unreadChats = (n: number) => (n === 1 ? "1 unread message" : `${n} unread messages`);

/** A customer's word for where their order is. */
const STATE_WORDS: Record<string, { label: string; tone: "warn" | "lime" | "plain" | "danger" }> = {
  sent: { label: "Sent", tone: "plain" },
  received: { label: "Sent", tone: "plain" },
  proposed: { label: "Waiting for them", tone: "plain" },
  changed: { label: "Waiting for them", tone: "plain" },
  accepted: { label: "Accepted", tone: "lime" },
  awaiting_payment: { label: "To pay", tone: "warn" },
  paid: { label: "Paid", tone: "lime" },
  dispatched: { label: "With a rider", tone: "lime" },
  on_the_way: { label: "On the way", tone: "lime" },
  arrived: { label: "At the door", tone: "lime" },
  completed: { label: "Done", tone: "lime" },
  declined: { label: "Declined", tone: "danger" },
  cancelled: { label: "Cancelled", tone: "plain" },
  expired: { label: "Expired", tone: "plain" },
  refund_due: { label: "Refund due", tone: "warn" },
  refunded: { label: "Refunded", tone: "plain" },
  no_show: { label: "Missed", tone: "plain" },
  price_changed: { label: "New prices to confirm", tone: "warn" },
};

/** An order's state in a customer's words, with its pill's tone. */
export function stateWords(state: string | undefined): { label: string; tone: "warn" | "lime" | "plain" | "danger" } {
  if (!state) return { label: "", tone: "plain" };
  return STATE_WORDS[state] ?? { label: state.replace(/_/g, " ").replace(/^./, (c) => c.toUpperCase()), tone: "plain" };
}

/** A card's kicker: what kind of thing it is. */
export function cardKicker(c: AppCard): string {
  switch (c.type) {
    case "order":
      return c.title.startsWith("Booking") ? "Booking" : "Order";
    case "pay":
      return "Pay";
    case "booking":
      return c.reference ? "Booking" : "Book";
    case "invoice":
      return "Invoice";
    case "delivery":
      return "Delivery";
    default:
      return "Product";
  }
}

const DELIVERY_STEPS: DeliveryProgress["status"][] = ["accepted", "picked_up", "arrived", "delivered"];

/** How far a delivery has got: the step (1 to 4) of 4, for its bar. */
export function deliveryStep(d: Pick<DeliveryProgress, "status">): number {
  return DELIVERY_STEPS.indexOf(d.status) + 1;
}

type Tone = "warn" | "lime" | "plain" | "danger";

/** A delivery's state in a customer's words, with its pill's tone. */
export function deliveryWords(status: string | undefined): { label: string; tone: Tone } {
  switch (status) {
    case "accepted":
      return { label: "Rider coming", tone: "plain" };
    case "picked_up":
      return { label: "On the way", tone: "lime" };
    case "arrived":
      return { label: "At your address", tone: "lime" };
    case "delivered":
      return { label: "Delivered", tone: "lime" };
    default:
      return stateWords(status);
  }
}

/** An invoice's state in a customer's words. */
export function invoiceWords(status: string | undefined): { label: string; tone: Tone } {
  switch (status) {
    case "paid":
      return { label: "Paid", tone: "lime" };
    case "part_paid":
      return { label: "Part-paid", tone: "warn" };
    case "overdue":
      return { label: "Overdue", tone: "danger" };
    case "sent":
    case "viewed":
      return { label: "To pay", tone: "warn" };
    default:
      return stateWords(status);
  }
}

/** A card's pill: its state as the card's kind says it. */
export function cardState(c: Pick<AppCard, "type" | "status">): { label: string; tone: Tone } {
  if (c.type === "delivery") return deliveryWords(c.status);
  if (c.type === "invoice") return invoiceWords(c.status);
  return stateWords(c.status);
}

/** A card as it is now: an order's or a delivery's card follows the
 * person's order (its state, its rider) rather than what it said when sent. */
export function liveCard(c: AppCard, o: Pick<DoorOrder, "status" | "delivery"> | undefined): AppCard {
  if (!o) return c;
  if (c.type === "delivery") return o.delivery ? { ...c, status: o.delivery.status, line: o.delivery.line, delivery: o.delivery } : c;
  if (c.type === "order") return { ...c, status: o.status };
  return c;
}

/** What the order and delivery cards of a thread name, to know when to read
 * the orders again: the references, and the latest such card. Empty with none. */
export function cardsKey(ms: { id: string; local?: boolean; card?: AppCard }[] | undefined): string {
  const withCards = (ms ?? []).filter((m) => !m.local && m.card?.reference && (m.card.type === "order" || m.card.type === "delivery"));
  if (withCards.length === 0) return "";
  const refs = [...new Set(withCards.map((m) => m.card?.reference ?? ""))].sort();
  return `${refs.join(",")}|${withCards[withCards.length - 1]?.id ?? ""}`;
}

/** A card's figure: its total, or the time a booking offer takes. */
export function cardFigure(c: AppCard): string {
  const parts: string[] = [];
  if (c.total_kobo !== undefined) parts.push(formatPrice(c.total_kobo));
  if (c.minutes) parts.push(minutesWords(c.minutes));
  return parts.join(" · ");
}

/** "30 min", "1 h", "1 h 30 min". */
export function minutesWords(m: number): string {
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  const r = m % 60;
  return r ? `${h} h ${r} min` : `${h} h`;
}

/** A size and colour, as the shop names it: "Red, 42". */
export function sizeWords(v: Record<string, string>): string {
  return Object.values(v).filter(Boolean).join(", ");
}

/** "Only 3 left", when the shop says how few. */
export function fewLeftWords(n: number | undefined): string {
  if (!n || n <= 0) return "";
  return `Only ${n} left`;
}

/** One line of a basket: an item or one of its sizes, and how many. */
export interface BasketLine {
  item_id: string;
  name: string;
  price_kobo: number;
  quantity: number;
}

/** What a basket comes to, with the delivery fee. */
export function basketTotal(lines: BasketLine[], fee = 0): number {
  return lines.reduce((t, l) => t + l.price_kobo * l.quantity, 0) + fee;
}

/** "2 items", "1 item". */
export function itemsWords(lines: BasketLine[]): string {
  const n = lines.reduce((t, l) => t + l.quantity, 0);
  return `${n} ${n === 1 ? "item" : "items"}`;
}

/** The basket with one more (or one fewer) of a line; none left drops it. */
export function changeBasket(lines: BasketLine[], line: Omit<BasketLine, "quantity">, by: number): BasketLine[] {
  const at = lines.findIndex((l) => l.item_id === line.item_id);
  if (at < 0) return by > 0 ? [...lines, { ...line, quantity: by }] : lines;
  const q = (lines[at]?.quantity ?? 0) + by;
  return q <= 0 ? lines.filter((_, i) => i !== at) : lines.map((l, i) => (i === at ? { ...l, quantity: q } : l));
}

/** How an order is paid, in a line under the total. */
export function payWords(payment: string, business: string): string {
  switch (payment) {
    case "at_order":
      return "You pay when you order, by a pay link.";
    case "after_accept":
      return `You pay once ${business} accepts your order.`;
    case "on_delivery":
      return "You pay when it reaches you.";
    default:
      return `You pay ${business} as you agree with them.`;
  }
}

/** Whether a shop item can be put in the basket as it is (no sizes). */
export const plainItem = (it: ShopItem) => !it.sizes || it.sizes.length === 0;

/** Messages merged by id, oldest first (a page, a send, a poll). */
export function mergeMessages(cur: PersonMessage[], more: PersonMessage[]): PersonMessage[] {
  const byID = new Map(cur.map((m) => [m.id, m]));
  for (const m of more) byID.set(m.id, m);
  return [...byID.values()].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at) || (a.id < b.id ? -1 : 1));
}

/** The thread after its latest page is fetched again: the page in place of
 * what it covers, older messages kept when there are more, and sends
 * still under way. */
export function withLatest(cur: PersonMessage[], page: PersonMessage[], more: boolean): PersonMessage[] {
  const from = page[0] ? Date.parse(page[0].created_at) : Infinity;
  const kept = cur.filter((m) => m.local || (more && Date.parse(m.created_at) < from));
  return mergeMessages(kept, page);
}

/** The days of the week, as the shop's hours list them (Monday first). */
const DAYS = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"];

/** A shop's hours as lines: "Monday 08:00 to 18:00", "Sunday closed". */
export function hoursLines(h: ChatLinkCard["hours"] | undefined): string[] {
  if (!h?.days) return [];
  return h.days.map((spans, i) => {
    const day = DAYS[i] ?? "";
    if (!spans || spans.length === 0) return `${day} closed`;
    return `${day} ${spans.map((s) => `${s.open} to ${s.close}`).join(", ")}`;
  });
}

/** "Open now" or "Closed now", when its hours say. */
export function openWords(open: boolean | undefined): string {
  if (open === undefined) return "";
  return open ? "Open now" : "Closed now";
}

/** A refusal of POST /v1/me/chats, in a person's words. */
export function startProblem(status: number, body: Record<string, unknown>, fallback: string): string {
  if (status === 410 || body.link_replaced) return "This link no longer works. Ask the business for its new link.";
  if (status === 403 && body.blocked) return "This business is not taking messages from you.";
  if (status === 404) return "This link does not open a chat with any business. Check it, or ask the business for it.";
  return fallback;
}

/** The report's reasons, in words. */
export const REPORT_REASONS: { key: "spam" | "scam" | "abuse" | "other"; words: string }[] = [
  { key: "scam", words: "They are trying to cheat me" },
  { key: "spam", words: "Messages I don't want" },
  { key: "abuse", words: "Rude or threatening" },
  { key: "other", words: "Something else" },
];

/** The button on a storefront order: "Chat with Mama's Kitchen about this order". */
export const chatAboutWords = (business: string) => `Chat with ${business} about this order`;

/** What the person's first message about an order says, ready to send. */
export const aboutOrderDraft = (reference: string) => `Hello, about my order ${reference}: `;

/** A chat's history state once the tool it asked for is open: the rest
 * of it (null when nothing is left), or undefined when it asked for none. */
export function withoutTool(state: unknown): Record<string, unknown> | null | undefined {
  if (!state || typeof state !== "object" || !("tool" in state)) return undefined;
  const rest = Object.fromEntries(Object.entries(state).filter(([k]) => k !== "tool"));
  return Object.keys(rest).length ? rest : null;
}

/** What a customer's start of one of a business's tools, or their yes,
 * ran into, in words (made tools T5): a tool not offered to customers (it
 * moves money, or its form cannot be asked here), one gone, too many
 * starts; else as any tool's start. */
export function theirErrorWords(e: unknown): string {
  if (e instanceof ApiError && e.status === 409 && e.body.not_offered) return "This can't be done from here. Ask them here in the chat.";
  if (e instanceof ApiError && e.status === 404) return "This isn't available any more.";
  if (e instanceof ApiError && e.status === 429) return "Too many tries just now. Try again in a while.";
  return startErrorWords(e);
}
