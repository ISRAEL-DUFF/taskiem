// A business's shop (pages/BusinessShop.tsx; docs/spec/mobile-app-look.md,
// "The business shop"): a linked supplier's prices as the shop draws them.
// Pure, so each is tested; the app's mobile/lib/chat/business_shop.dart and
// shop.dart ShopQuantityPrices do the same.

import { soldInSizes } from "./buyer";
import type { ShopItem } from "./personchat";
import type { PriceItem } from "./pricelists";

/** A supplier's quantity terms for an item: its unit, its minimum and its
 * lower prices from a quantity up, a product's sizes counted together. */
export interface ShopTerms {
  unit: string;
  min: number;
  breaks: { min_quantity: number; price_kobo: number }[];
  sized: boolean;
}

/** A supplier's item as the shop draws it: its photo read through the link,
 * a product's sizes by their choices, each at its own price where it has one. */
export function shopItemOf(it: PriceItem, link: string): ShopItem {
  const own = (z: { price_kobo?: number }) => (z.price_kobo ?? 0) > 0 && !it.overridden;
  return {
    id: it.item_id,
    name: it.name,
    description: it.description || undefined,
    category: it.category || undefined,
    price_kobo: it.price_kobo,
    photo: it.photo ? `/v1/link-chats/${encodeURIComponent(link)}/photo?item=${encodeURIComponent(it.item_id)}&w=640` : undefined,
    // A product sold in sizes with none on sale now: nothing to order.
    available: it.available && !(soldInSizes(it) && !it.sizes?.length),
    sizes: it.sizes?.length
      ? it.sizes.map((z) => ({ id: z.item_id, variant: z.variant, price_kobo: own(z) ? z.price_kobo : undefined, available: z.available }))
      : undefined,
  };
}

/** An item's terms, if it is one of theirs. */
export const termsOf = (it: PriceItem): ShopTerms => ({ unit: it.unit ?? "", min: it.min_quantity, breaks: it.breaks ?? [], sized: soldInSizes(it) });

const unitsOf = (unit: string, n: number) => (!unit ? "" : n === 1 || /s$/.test(unit) ? unit : `${unit}s`);

/** The quantity prices as rows, from the minimum up: "2 to 9 bags" at
 * ₦45,000, "10 bags and more" at ₦40,000; and which of them [have] reaches. */
export function quantitySteps(terms: ShopTerms, price: number, have: number): { steps: { words: string; price: number }[]; reached: number; last?: number } {
  const from = Math.max(1, terms.min);
  const breaks = terms.breaks.filter((b) => b.min_quantity > from).sort((a, b) => a.min_quantity - b.min_quantity);
  let p = price;
  for (const b of terms.breaks) if (b.min_quantity <= from) p = b.price_kobo;
  const raw: { from: number; to?: number; price: number }[] = [];
  let at = from;
  for (const b of breaks) {
    raw.push({ from: at, to: b.min_quantity - 1, price: p });
    at = b.min_quantity;
    p = b.price_kobo;
  }
  raw.push({ from: at, price: p });
  const u = (n: number) => (terms.unit ? ` ${unitsOf(terms.unit, n)}` : "");
  let reached = -1;
  raw.forEach((s, i) => {
    if (have >= s.from) reached = i;
  });
  return {
    steps: raw.map((s) => ({
      words: s.to === undefined ? `${s.from}${u(s.from)} and more` : s.to === s.from ? `${s.from}${u(s.from)}` : `${s.from} to ${s.to}${u(s.to)}`,
      price: s.price,
    })),
    reached,
    last: breaks[breaks.length - 1]?.min_quantity,
  };
}

/** "6 pairs", "50-pair": a quantity in the item's unit. */
export const inUnits = (terms: ShopTerms, n: number) => `${n}${terms.unit ? ` ${unitsOf(terms.unit, n)}` : ""}`;

/** A product sold in sizes whose sizes in the bag are under its minimum,
 * said; else undefined. */
export function shortWords(items: PriceItem[], qty: Record<string, number>): string | undefined {
  const short = items.find((it) => {
    const n = (it.sizes ?? []).reduce((a, z) => a + (qty[z.item_id] ?? 0), 0);
    return n > 0 && n < it.min_quantity;
  });
  return short && `${short.name}: at least ${short.min_quantity} in all, its sizes together.`;
}
