// The buyer's tools in a conversation with a supplier (docs/spec/chat-tools.md
// §5, phase 3): ordering from their price list, the orders sent them, their
// bills. Pure, so each is tested.

import { priceAt } from "./deals";
import type { ItemLink, PriceItem } from "./pricelists";
import type { Purchase } from "./purchases";
import { formatPrice } from "./storefront";

/** A step of − or + on a line: + from nothing goes to its minimum, − under
 * the minimum takes it off (the Buyer design). */
export function stepQty(qty: number, min: number, dir: 1 | -1): number {
  const m = Math.max(1, min);
  if (dir === 1) return qty === 0 ? m : qty + 1;
  return qty - 1 < m ? 0 : qty - 1;
}

/** An item an order line names: one of a list's, or one size of a
 * product listed with its sizes and colours (variant_of its product). */
export type OrderItem = PriceItem & {
  variant_of?: string;
  variant?: Record<string, string>;
};

/** A list's items as order lines name them: a product sold in sizes and
 * colours as each size (at its own price, else the product's with its
 * quantity prices; on sale when both are), every other item as it is. */
export function flatItems(items: PriceItem[]): OrderItem[] {
  const out: OrderItem[] = [];
  for (const it of items) {
    if (!it.sizes?.length) {
      out.push(it);
      continue;
    }
    for (const z of it.sizes) {
      const own = (z.price_kobo ?? 0) > 0 && !it.overridden;
      out.push({
        item_id: z.item_id,
        name: z.name,
        unit: it.unit,
        category: it.category,
        price_kobo: own ? (z.price_kobo ?? 0) : it.price_kobo,
        min_quantity: 1,
        available: it.available && z.available,
        breaks: own ? undefined : it.breaks,
        variant_of: it.item_id,
        variant: z.variant,
      });
    }
  }
  return out;
}

/** The item a line's quantity prices and minimum count toward: a size's
 * product, else the item itself. */
export const productOf = (it: OrderItem) => it.variant_of ?? it.item_id;

/** How many of each product the quantities take, its sizes together. */
export function together(
  items: OrderItem[],
  qty: Record<string, number>,
): Record<string, number> {
  const out: Record<string, number> = {};
  for (const it of items) {
    const n = qty[it.item_id] ?? 0;
    if (n > 0) out[productOf(it)] = (out[productOf(it)] ?? 0) + n;
  }
  return out;
}

/** "36 pairs in 7 sizes and colours": a product's sizes in the bag. */
export function sizesTaken(
  product: PriceItem,
  qty: Record<string, number>,
): string {
  const taken = (product.sizes ?? []).filter((z) => (qty[z.item_id] ?? 0) > 0);
  if (taken.length === 0) return "";
  const n = taken.reduce((a, z) => a + (qty[z.item_id] ?? 0), 0);
  const unit = product.unit ? (n === 1 ? product.unit : `${product.unit}s`) : "";
  const sizes = taken.length === 1 ? "1 size or colour" : `${taken.length} sizes and colours`;
  return `${n}${unit ? ` ${unit}` : ""} in ${sizes}`;
}

/** The bag: its lines, how many and the total. A product's sizes are
 * lines of their own, priced at the product's quantity together. */
export function bagOf(items: PriceItem[], qty: Record<string, number>) {
  const flat = flatItems(items);
  const lines = flat
    .filter((it) => (qty[it.item_id] ?? 0) > 0)
    .map((it) => ({ item: it, quantity: qty[it.item_id] ?? 0 }));
  const sum = together(flat, qty);
  const unitPrice = (it: OrderItem) => priceAt(it, sum[productOf(it)] ?? 0);
  // Each at its price for that quantity (a quantity break).
  const total = lines.reduce((n, l) => n + l.quantity * unitPrice(l.item), 0);
  const products = new Set(lines.map((l) => productOf(l.item))).size;
  const count =
    products === 0
      ? "Nothing yet"
      : `${products} ${products === 1 ? "item" : "items"} in your order`;
  return { lines, total, count, unitPrice };
}

/** A message asking the supplier to quote for the bag. */
export function quoteText(
  lines: { item: Pick<PriceItem, "name" | "unit">; quantity: number }[],
): string {
  return [
    "Could you quote me for:",
    ...lines.map((l) => `• ${l.quantity} × ${l.item.name}`),
  ].join("\n");
}

/** Where an order sent to a supplier is, on Placed, Accepted, On the way,
 * Delivered; a word for its state; and whether it waits on us. */
export function purchaseState(
  p: Pick<Purchase, "status" | "seller_status" | "cancel_asked_at">,
): {
  word: string;
  tone: "lime" | "warn" | "danger" | "plain";
  step?: number;
  quote: boolean;
} {
  if (p.status === "cancelled" || p.seller_status === "cancelled")
    return { word: "Cancelled", tone: "danger", quote: false };
  if (p.seller_status === "declined")
    return { word: "Declined", tone: "danger", quote: false };
  if (p.status === "received")
    return { word: "Received", tone: "lime", step: 3, quote: false };
  switch (p.seller_status) {
    case "proposed":
      return {
        word: "Their quote: confirm it",
        tone: "warn",
        step: 0,
        quote: true,
      };
    case "changed":
      return { word: "They changed it", tone: "warn", step: 0, quote: false };
    case "accepted":
    case "ready":
      return {
        word: p.seller_status === "ready" ? "Ready" : "Accepted",
        tone: "lime",
        step: 1,
        quote: false,
      };
    case "dispatched":
      return { word: "On the way", tone: "lime", step: 2, quote: false };
    case "delivered":
      return { word: "Delivered", tone: "lime", step: 3, quote: false };
  }
  if (p.status === "drafted")
    return { word: "Draft", tone: "plain", quote: false };
  return {
    word: p.cancel_asked_at ? "Cancel asked" : "Sent",
    tone: "plain",
    step: 0,
    quote: false,
  };
}

/** The last order's lines again, as the price list has them now: those
 * still on it, by the item links between their items and ours. */
export function reorderQty(
  last: Pick<Purchase, "lines">,
  links: ItemLink[],
  items: PriceItem[],
): Record<string, number> {
  const out: Record<string, number> = {};
  for (const l of last.lines) {
    const seller =
      links.find((x) => x.item_id === l.item_id)?.seller_item_id ??
      items.find((it) => it.name === l.item)?.item_id;
    const it = items.find((x) => x.item_id === seller);
    if (it && it.available)
      out[it.item_id] = Math.max(l.quantity, it.min_quantity);
  }
  return out;
}

/** "₦78,000 · min 2". */
export const priceLine = (it: PriceItem) =>
  `${formatPrice(it.price_kobo)}${it.unit ? ` a ${it.unit}` : ""}${it.min_quantity > 1 ? ` · min ${it.min_quantity}` : ""}`;

/** An order row's line, as the Buyer board draws it: "₦78,000 · min 2". */
export const orderLine = (it: PriceItem) =>
  `${formatPrice(it.price_kobo)}${it.min_quantity > 1 ? ` · min ${it.min_quantity}` : ""}`;
