// Price lists and item links between linked businesses (business links
// L4, docs/spec/business-links.md §9; /v1/price-lists, /v1/link-prices,
// /v1/supplier-prices, /v1/item-links): the API's shapes and how a price
// reads.

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
  it: Pick<PriceItem, "price_kobo" | "unit" | "min_quantity" | "available">,
): string {
  const parts = [formatPrice(it.price_kobo) + (it.unit ? ` a ${it.unit}` : "")];
  if (it.min_quantity > 1) parts.push(`at least ${it.min_quantity}`);
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
