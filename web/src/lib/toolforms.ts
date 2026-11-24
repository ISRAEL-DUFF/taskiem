// What each chat tool's form saves, said as its diary line
// (docs/spec/chat-tools.md §3). Pure, so each is tested.

import { formatPrice } from "./storefront";
import { quantity } from "./stock";

export interface PriceChange {
  name: string;
  from: number;
  to: number;
}

/** "Price of Rice, 50kg bag: ₦76,000 → ₦78,000". */
export const priceLine = (c: PriceChange) => `Price of ${c.name}: ${formatPrice(c.from)} → ${formatPrice(c.to)}`;

/** "Stock of Rice counted: 4 bags → 6 bags". */
export const countLine = (name: string, from: number | null, to: number, unit?: string) =>
  `Stock of ${name} counted: ${from === null ? "no count" : quantity(from, unit)} → ${quantity(to, unit)}`;

/** "Stock in: Rice, 25kg bag +20 bags (now 24 bags)". */
export const stockInLine = (name: string, n: number, now: number | null, unit?: string) =>
  `Stock in: ${name} +${quantity(n, unit)}${now === null ? "" : ` (now ${quantity(now + n, unit)})`}`;

/** "New product: Garri, Ijebu, ₦32,000 a bag". */
export const productLine = (name: string, priceKobo: number, unit?: string) =>
  `New product: ${name}, ${formatPrice(priceKobo)}${unit ? ` a ${unit}` : ""}`;

/** "New service: Rice milling, 30 min, ₦2,500". */
export const serviceLine = (name: string, minutes: number, priceKobo: number | null) =>
  `New service: ${name}, ${minutes} min, ${priceKobo === null ? "price on the day" : formatPrice(priceKobo)}`;

/** "Price list Wholesale: Rice ₦40,000 → ₦41,000" (and how many more). */
export function priceListLine(list: string, changes: PriceChange[]): string {
  const [first, ...rest] = changes;
  if (!first) return `Price list ${list} saved`;
  const one = `${first.name} ${formatPrice(first.from)} → ${formatPrice(first.to)}`;
  return `Price list ${list}: ${one}${rest.length ? `, and ${rest.length} more` : ""}`;
}

/** "Accepted order AD-7Q4K2P from Rivers Foods". */
export const orderLine = (step: "accept" | "decline", reference: string, buyer: string) =>
  `${step === "accept" ? "Accepted" : "Declined"} order ${reference} from ${buyer}`;

/** "Added a row to Customers". */
export const rowLine = (table: string) => `Added a row to ${table}`;

/** Naira typed as kobo: "78,000" is 7,800,000; NaN when it is not money. */
export function nairaKobo(text: string): number {
  const t = text.replace(/[₦,\s]/g, "");
  if (!/^\d+(\.\d{1,2})?$/.test(t)) return NaN;
  return Math.round(Number(t) * 100);
}

/** Kobo as typed naira: 7,800,000 is "78,000". */
export function nairaText(kobo: number): string {
  const whole = Math.floor(kobo / 100);
  const frac = kobo % 100;
  return whole.toLocaleString("en-NG") + (frac ? `.${String(frac).padStart(2, "0")}` : "");
}
