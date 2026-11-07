// Price lists and item links between linked businesses (business links
// L4, docs/spec/business-links.md §9; /v1/price-lists, /v1/link-prices,
// /v1/supplier-prices, /v1/item-links): the API's shapes and how a price
// reads.

import { breakWords, type Break } from "./deals";
import { formatPrice } from "./storefront";

export interface PriceItem {
  /** The seller's own item. */
  item_id: string;
  name: string;
  unit?: string;
  price_kobo: number;
  min_quantity: number;
  available: boolean;
  /** This buyer's own price, not the list's (the seller's view). */
  overridden?: boolean;
  /** Lower prices from a quantity up (none on a buyer's own price). */
  breaks?: Break[];
}

export interface PriceList {
  id: string;
  name: string;
  items: PriceItem[];
  /** The links it is shared with. */
  links: string[];
  updated_at: string;
}

export interface SupplierPrices {
  link: string;
  business: string;
  name: string;
  items: PriceItem[];
  updated_at: string;
}

export interface ItemLink {
  link: string;
  item_id: string;
  seller_item_id: string;
  seller_item: string;
}

/** A price as a buyer reads it: "₦45,000 a bag · at least 5". */
export function priceWords(
  it: Pick<PriceItem, "price_kobo" | "unit" | "min_quantity" | "available"> & {
    breaks?: Break[];
  },
): string {
  const parts = [formatPrice(it.price_kobo) + (it.unit ? ` a ${it.unit}` : "")];
  if (it.min_quantity > 1) parts.push(`at least ${it.min_quantity}`);
  if (it.breaks?.length) parts.push(breakWords(it));
  if (!it.available) parts.push("not available now");
  return parts.join(" · ");
}

/** What a line of an order from a price list is short of: a whole number,
 * at least the minimum, while available ("" when it may be ordered). */
export function lineProblem(it: PriceItem, quantity: string): string {
  const q = quantity.trim();
  if (q === "") return "";
  if (!/^\d+$/.test(q) || Number(q) <= 0) return "a whole number";
  if (!it.available) return "not available now";
  if (Number(q) < it.min_quantity) return `at least ${it.min_quantity}`;
  return "";
}

/** The price list a product's form shows (the DeskSeller board): the one
 * shared with the business whose conversation it was opened from, the one
 * that holds the product, else the first. */
export function listFor(
  lists: PriceList[],
  itemId: string,
  link?: string,
): PriceList | undefined {
  const holds = (l: PriceList) => l.items.some((x) => x.item_id === itemId);
  const shared = (l: PriceList) => !!link && l.links.includes(link);
  return (
    lists.find((l) => shared(l) && holds(l)) ??
    lists.find(shared) ??
    lists.find(holds) ??
    lists[0]
  );
}

/** A list's items as PUT /v1/price-lists/{id}/items takes them, with one
 * product at a new price (added, at least 1 and on sale, if it was not
 * there). */
export function itemsWithPrice(
  list: PriceList,
  itemId: string,
  kobo: number,
): {
  item_id: string;
  price_kobo: number;
  min_quantity: number;
  available: boolean;
  breaks?: Break[];
}[] {
  const out: ReturnType<typeof itemsWithPrice> = list.items.map((x) => ({
    item_id: x.item_id,
    price_kobo: x.item_id === itemId ? kobo : x.price_kobo,
    min_quantity: x.min_quantity,
    available: x.available,
    // Breaks stay while still under the new price.
    breaks:
      x.item_id === itemId
        ? x.breaks?.filter((b) => b.price_kobo < kobo)
        : x.breaks,
  }));
  if (!list.items.some((x) => x.item_id === itemId))
    out.push({ item_id: itemId, price_kobo: kobo, min_quantity: 1, available: true });
  return out;
}

/** "Rivers Foods Ltd, Mama Ngozi Stores", or that it is shared with no one. */
export function sharedWords(ids: string[], names: Map<string, string>): string {
  const shown = ids.map((id) => names.get(id)).filter((n): n is string => !!n);
  return shown.length ? shown.join(", ") : "No one yet";
}

/** A list as a label: "Trade price list", but "Trade prices" and "VIP
 * price list" as they are (the word is not said twice). */
export function listLabel(name: string): string {
  return /\bprices?\b|\bprice list\b/i.test(name) ? name : `${name} price list`;
}
