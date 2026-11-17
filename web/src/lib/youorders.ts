// Orders on You and saved addresses (docs/spec/taskiem-for-everyone.md
// §13): what GET /v1/me/orders, GET /v1/me/orders/{business}/{reference}
// and /v1/me/addresses answer, and the words the screens build from it.
// Pure helpers, tested on their own.

import { ApiError } from "../api";
import type { DeliveryProgress } from "./personchat";
import { formatPrice } from "./storefront";

/** A business's photo, its sizes' addresses. */
export interface OrderPhoto {
  urls: Record<string, string>;
}

/** One row of Orders on You. */
export interface PersonOrder {
  business_id: string;
  business: string;
  photo?: OrderPhoto;
  reference: string;
  kind: "order" | "booking";
  state: string;
  summary: string;
  total_kobo: number;
  due_kobo: number;
  pay: boolean;
  made_at: string;
  chat_id: string | null;
}

export interface PersonOrders {
  orders: PersonOrder[];
  next: string | null;
  waiting_count: number;
}

export interface OrderLine {
  name: string;
  quantity: number;
  price_kobo: number;
  line_kobo: number;
}

/** One of the person's orders, with what it holds. */
export interface PersonOrderDetail extends PersonOrder {
  lines: OrderLine[];
  fulfilment?: "pickup" | "delivery";
  zone?: string;
  delivery_kobo?: number;
  paid_kobo: number;
  address?: { address: string; landmark?: string };
  service_name?: string;
  starts_at?: string;
  ends_at?: string;
  delivery?: DeliveryProgress;
}

/** One of the person's saved addresses. */
export interface SavedAddress {
  id: string;
  label: string;
  line: string;
  area: string;
  landmark: string;
  phone: string | null;
  default: boolean;
  created_at: string;
  updated_at: string;
}

/** The most addresses a person may keep. */
export const MAX_ADDRESSES = 20;

/** Where one order's page is. */
export const orderPath = (o: Pick<PersonOrder, "business_id" | "reference">) => `/you/orders/${encodeURIComponent(o.business_id)}/${encodeURIComponent(o.reference)}`;

/** The API's path of one order (and, with /pay, its pay link). */
export const orderAPI = (o: Pick<PersonOrder, "business_id" | "reference">) => `/v1/me/orders/${encodeURIComponent(o.business_id)}/${encodeURIComponent(o.reference)}`;

/** The next page's address: `next` goes back as `before`. */
export const ordersPage = (before: string | null | undefined, limit = 30) => `/v1/me/orders?limit=${limit}${before ? `&before=${encodeURIComponent(before)}` : ""}`;

/** Pages put together: a row once, in the order they came. */
export function addPage(cur: PersonOrder[], page: PersonOrder[]): PersonOrder[] {
  const seen = new Set(cur.map((o) => `${o.business_id}/${o.reference}`));
  return [...cur, ...page.filter((o) => !seen.has(`${o.business_id}/${o.reference}`))];
}

/** A row's line: "2 × Jollof rice · MK-1A2B · Today, 18:03". */
export function orderLine(o: Pick<PersonOrder, "summary" | "reference" | "kind">, when: string): string {
  const what = o.summary || (o.kind === "booking" ? "A booking" : "An order");
  return [what, o.reference, when].filter(Boolean).join(" · ");
}

/** What is due, under the total: "₦2,500 to pay", or nothing. */
export function dueWords(o: Pick<PersonOrder, "due_kobo" | "total_kobo" | "pay">): string {
  if (o.due_kobo <= 0) return "";
  if (o.due_kobo === o.total_kobo) return o.pay ? "To pay" : "Not paid yet";
  return `${formatPrice(o.due_kobo)} still to pay`;
}

/** How the order comes: pickup or delivery, in words. */
export function fulfilmentWords(f: PersonOrderDetail["fulfilment"], zone?: string): string {
  if (f === "pickup") return "You pick it up from them";
  if (f === "delivery") return zone ? `Delivered to you in ${zone}` : "Delivered to you";
  return "";
}

/** A saved address in one line: "12 Herbert Macaulay Way, Yaba". */
export function addressLine(a: Pick<SavedAddress, "line" | "area">): string {
  return [a.line.trim(), a.area.trim()].filter(Boolean).join(", ");
}

/** A saved address's second line: the landmark and the rider's phone. */
export function addressSub(a: Pick<SavedAddress, "landmark" | "phone" | "default">): string {
  return [a.default ? "Default" : "", a.landmark.trim() && `Near ${a.landmark.trim()}`, a.phone ? `Rider calls ${a.phone}` : ""].filter(Boolean).join(" · ");
}

/** The address form as typed. */
export interface AddressForm {
  label: string;
  line: string;
  area: string;
  landmark: string;
  phone: string;
  default: boolean;
}

export const emptyAddress = (first: boolean): AddressForm => ({ label: "", line: "", area: "", landmark: "", phone: "", default: first });

export const addressForm = (a: SavedAddress): AddressForm => ({ label: a.label, line: a.line, area: a.area, landmark: a.landmark, phone: a.phone ?? "", default: a.default });

/** What the form lacks before it is sent, or null. */
export function addressFormProblem(f: AddressForm): string | null {
  if (!f.label.trim()) return "Give the address a name, such as Home or Work.";
  if (!f.line.trim()) return "Give the street and number.";
  if (f.label.trim().length > 40) return "The name is at most 40 letters.";
  if (f.line.trim().length > 200) return "The street is at most 200 letters.";
  return null;
}

/** The body of POST or PUT /v1/me/addresses: a changed address sends
 * `phone: ""` to take a phone away; `default` only when it is chosen. */
export function addressBody(f: AddressForm, editing: boolean): Record<string, unknown> {
  const body: Record<string, unknown> = { label: f.label.trim(), line: f.line.trim(), area: f.area.trim(), landmark: f.landmark.trim() };
  if (f.phone.trim() || editing) body.phone = f.phone.trim();
  if (f.default) body.default = true;
  return body;
}

/** A refused address, in a person's words. */
export function addressProblem(e: unknown): string | null {
  if (!(e instanceof ApiError)) return null;
  if (e.status === 409) return `You can keep up to ${MAX_ADDRESSES} addresses. Delete one you no longer use first.`;
  if (e.status === 400 && (e.body.phone_invalid || /phone/i.test(e.message))) return "That phone number does not look right. Check it, starting with 0 or +234.";
  return null;
}

/** The pay link refused, in a person's words (the server's sentence
 * when it gave one). */
export function payProblem(e: unknown): string | null {
  if (!(e instanceof ApiError)) return null;
  if (e.status === 403) return "This business is not taking messages or payments from you.";
  if (e.status === 409 && e.body.area_off) return "This business is not taking orders right now.";
  return null;
}

/** "These are mine" done: the words of the notice. */
export const confirmedWords = "Thank you. They are yours now.";
