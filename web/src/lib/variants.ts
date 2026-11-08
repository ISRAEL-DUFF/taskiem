// Sizes and colours (docs/spec/chat-categories-and-variants.md, Part B):
// a product's choices, and each combination sold as an item of its own
// pointing at the product (variant_of). The words on owners' screens are
// "sizes and colours" and "choices", never "variant".

import type { StockItem } from "./stock";

export interface Choice {
  name: string;
  values: string[];
}
export type Variant = Record<string, string>;

/** The plan's limits. */
export const MAX_CHOICES = 3;
export const MAX_VALUES = 20;
export const MAX_SIZES = 100;

/** A variant's values in its choices' order: "42, Black". */
export function label(v: Variant | undefined, cs: Choice[] | undefined): string {
  if (!v) return "";
  if (!cs?.length) return Object.values(v).join(", ");
  return cs
    .map((c) => v[c.name])
    .filter(Boolean)
    .join(", ");
}

/** A variant as one string, whatever its case. */
export function keyOf(v: Variant): string {
  return Object.entries(v)
    .map(([k, x]) => `${k.trim().toLowerCase()}=${x.trim().toLowerCase()}`)
    .sort()
    .join("|");
}

/** Every combination of the choices' values, the first choice slowest. */
export function allCells(cs: Choice[]): Variant[] {
  let out: Variant[] = [{}];
  for (const c of cs) {
    const next: Variant[] = [];
    for (const v of out) for (const x of c.values) next.push({ ...v, [c.name]: x });
    out = next;
  }
  return cs.length ? out : [];
}

/** A product's sizes and colours (those retired too, when all). */
export function sizesOf(items: StockItem[], product: string, all = false): StockItem[] {
  return items.filter((x) => x.variant_of === product && (all || x.available));
}

/** Whether a product has sizes and colours on sale. */
export const hasSizes = (it: StockItem) => (it.choices?.length ?? 0) > 0;

/** A product with sizes and colours, as a list says it: how many, and
 * how many are low. */
export function sizesLine(items: StockItem[], product: string): {
  count: number;
  low: number;
  stock: number | null;
} {
  const vs = sizesOf(items, product);
  let stock: number | null = null;
  for (const v of vs) if (v.stock !== null) stock = (stock ?? 0) + v.stock;
  return { count: vs.length, low: vs.filter((v) => v.low).length, stock };
}

/** "12 sizes and colours", "1 size or colour". */
export const sizesWords = (n: number) =>
  n === 1 ? "1 size or colour" : `${n} sizes and colours`;

/** The size or colour a pick names, when it names one on sale. */
export function findSize(
  items: StockItem[],
  product: string,
  picked: Variant,
): StockItem | undefined {
  const k = keyOf(picked);
  return sizesOf(items, product).find((v) => v.variant && keyOf(v.variant) === k);
}

/** Which of a choice's values can still be picked, given the others
 * picked so far: one of the product's on sale, with some left (or no
 * count kept). */
export function pickable(
  items: StockItem[],
  product: string,
  choice: string,
  picked: Variant,
): Set<string> {
  const out = new Set<string>();
  for (const v of sizesOf(items, product)) {
    if (!v.variant || (v.stock !== null && v.stock <= 0)) continue;
    const fits = Object.entries(picked).every(
      ([k, x]) => k === choice || v.variant?.[k] === x,
    );
    const x = v.variant[choice];
    if (fits && x) out.add(x);
  }
  return out;
}

/** Ready-made values, to save typing: any can be changed. */
export const PRESETS: { label: string; name: string; values: string[] }[] = [
  { label: "Shoe sizes (EU)", name: "Size", values: ["36", "37", "38", "39", "40", "41", "42", "43", "44", "45", "46"] },
  { label: "Shoe sizes (UK)", name: "Size", values: ["3", "4", "5", "6", "7", "8", "9", "10", "11", "12"] },
  { label: "Clothing", name: "Size", values: ["XS", "S", "M", "L", "XL", "XXL"] },
  { label: "Small, medium, large", name: "Size", values: ["Small", "Medium", "Large"] },
  { label: "Children's ages", name: "Age", values: ["0–1", "1–2", "2–3", "3–4", "4–5", "5–6", "7–8", "9–10", "11–12"] },
  { label: "Bottles", name: "Size", values: ["35cl", "50cl", "75cl", "1L", "1.5L"] },
  { label: "Bags", name: "Size", values: ["5kg", "10kg", "25kg", "50kg"] },
  { label: "Colours", name: "Colour", values: ["Black", "White", "Red", "Blue", "Brown", "Grey"] },
];

/** Values typed as one line: "40, 41, 42" or one a line. */
export function parseValues(text: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of text.split(/[,\n]/)) {
    const v = raw.trim().replace(/\s+/g, " ");
    if (v && !seen.has(v.toLowerCase())) {
      seen.add(v.toLowerCase());
      out.push(v);
    }
  }
  return out;
}
