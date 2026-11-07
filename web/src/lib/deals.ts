// Wholesale prices and price deliberation (docs/spec/chat-tools.md §9,
// phase 7): quantity breaks on a price list, offers and counters as chat
// cards, a price kept for a buyer. Pure, so each is tested.

import type { PriceItem } from "./pricelists";
import { formatPrice } from "./storefront";

/** A lower price from a quantity up: from 10 bags, ₦39,500 a bag. */
export interface Break {
  min_quantity: number;
  price_kobo: number;
}

/** Up to this many quantity prices on an item (the API's MaxBreaks). */
export const MAX_BREAKS = 5;

/** One step of a deal, as the order's history keeps it. */
export interface DealStep {
  by: "buyer" | "seller";
  who?: string;
  total_kobo: number;
  was_kobo?: number;
  as_total?: boolean;
  note?: string;
  at: string;
}

/** A deal on a card: its steps (a person named on our own side only)
 * and when it lapses. */
export interface DealCard {
  steps: { by: "buyer" | "seller"; who?: string; total_kobo: number; at: string }[];
  until?: string;
}

/** Whose card it is: the seller's order, the buyer's purchase. */
export const cardSide = (doc: string): "buyer" | "seller" =>
  doc === "purchase" ? "buyer" : "seller";

/** Whether a deal card can still be answered: not lapsed. */
export const dealOpen = (d: DealCard | undefined, now = Date.now()) =>
  !d?.until || new Date(d.until).getTime() > now;

/** How long a price is kept for a buyer once agreed: this order only, or
 * that many days. */
export const KEEP_CHOICES: { days: number; label: string }[] = [
  { days: 0, label: "This order only" },
  { days: 7, label: "7 days" },
  { days: 14, label: "14 days" },
  { days: 30, label: "30 days" },
];

/** What one of an item costs at a quantity: the last break reached, else
 * its price (the server's PriceAt; a buyer's own price has no breaks). */
export function priceAt(
  it: Pick<PriceItem, "price_kobo"> & { breaks?: Break[] },
  qty: number,
): number {
  let p = it.price_kobo;
  for (const b of it.breaks ?? []) if (qty >= b.min_quantity) p = b.price_kobo;
  return p;
}

/** The breaks as a buyer reads them: "from 10 bags ₦39,500 · from 50 ₦38,800". */
export function breakWords(
  it: Pick<PriceItem, "unit"> & { breaks?: Break[] },
): string {
  return (it.breaks ?? [])
    .map(
      (b, i) =>
        `from ${b.min_quantity}${i === 0 && it.unit ? ` ${plural(it.unit, b.min_quantity)}` : ""} ${formatPrice(b.price_kobo)}`,
    )
    .join(" · ");
}

const plural = (unit: string, n: number) =>
  n === 1 || /s$/.test(unit) ? unit : `${unit}s`;

/** What is wrong with an item's breaks, else "": each a larger quantity
 * than the last (and than its minimum), each a lower price. */
export function breaksProblem(
  min: number,
  price: number,
  breaks: Break[],
): string {
  if (breaks.length > MAX_BREAKS) return `Up to ${MAX_BREAKS} quantity prices.`;
  let q = Math.max(1, min);
  let p = price;
  for (const b of breaks) {
    if (!Number.isInteger(b.min_quantity) || b.min_quantity <= q)
      return "Each quantity is more than the one before it.";
    if (!(b.price_kobo > 0) || b.price_kobo >= p)
      return "Each price is lower than the one before it.";
    q = b.min_quantity;
    p = b.price_kobo;
  }
  return "";
}

/** Naira as people type them in a deal: "760k", "1.2m", "₦760,000". */
export function dealKobo(text: string): number {
  const t = text.trim().toLowerCase().replace(/[₦,\s]/g, "").replace(/^n(?=\d)/, "");
  const m = /^(\d+(?:\.\d+)?)([km]?)$/.exec(t);
  if (!m) return NaN;
  const [, num = "", suffix = ""] = m;
  const mult = suffix === "k" ? 1_000 : suffix === "m" ? 1_000_000 : 1;
  const naira = Number(num) * mult;
  if (!suffix && !/^\d+(\.\d{1,2})?$/.test(num)) return NaN;
  return Math.round(naira * 100);
}

/** How far under a price an amount is: "6% off" (nothing when not under). */
export function offWords(amount: number, was?: number): string {
  if (!was || amount >= was) return "";
  const pct = Math.round(((was - amount) * 100) / was);
  return pct > 0 ? `${pct}% off` : "under";
}

/** A deal's amount, against what it was: "₦740,000 · was ₦790,000 · 6% off". */
export function dealLine(amount: number, was?: number): string {
  const off = offWords(amount, was);
  return [formatPrice(amount), off && was ? `was ${formatPrice(was)}` : "", off]
    .filter(Boolean)
    .join(" · ");
}

/** A step of the history in words: "They offered ₦740,000", "Tunde
 * countered ₦770,000" (the first step is the offer, the rest counters). */
export function stepWords(
  s: Pick<DealStep, "by" | "who" | "total_kobo">,
  index: number,
  mine: "buyer" | "seller",
): string {
  const who = s.by === mine ? (s.who ?? "You") : "They";
  return `${who} ${index === 0 ? "offered" : "countered"} ${formatPrice(s.total_kobo)}`;
}

/** When an offer lapses, in words: "Lapses in 47 hours", "Lapses in 20
 * minutes", "Lapsed". */
export function lapseWords(until: string | undefined, now = Date.now()): string {
  if (!until) return "";
  const ms = new Date(until).getTime() - now;
  if (ms <= 0) return "Lapsed";
  const h = Math.floor(ms / 3_600_000);
  if (h >= 1) return `Lapses in ${h} ${h === 1 ? "hour" : "hours"}`;
  const m = Math.max(1, Math.floor(ms / 60_000));
  return `Lapses in ${m} ${m === 1 ? "minute" : "minutes"}`;
}
