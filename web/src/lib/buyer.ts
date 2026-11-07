// The buyer's tools in a conversation with a supplier (docs/spec/chat-tools.md
// §5, phase 3): ordering from their price list, the orders sent them, their
// bills. Pure, so each is tested.

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

/** The bag: its lines, how many and the total. */
export function bagOf(items: PriceItem[], qty: Record<string, number>) {
  const lines = items
    .filter((it) => (qty[it.item_id] ?? 0) > 0)
    .map((it) => ({ item: it, quantity: qty[it.item_id] ?? 0 }));
  const total = lines.reduce((n, l) => n + l.quantity * l.item.price_kobo, 0);
  const count =
    lines.length === 0
      ? "Nothing yet"
      : `${lines.length} ${lines.length === 1 ? "item" : "items"} in your order`;
  return { lines, total, count };
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
