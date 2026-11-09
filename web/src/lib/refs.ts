// What "/invoice", "/product", "/order" and "/customer" offer to tag
// (docs/spec/chat-five-more-flows.md, phase 2 additions), and the
// business's own products on "#" in the Taskiem chat. Pure, tested.

import type { Suggestion } from "./mentions";
import { refStatusWords } from "./mentions";
import { naira, type CustomerInvoice } from "./invoices";
import type { StockItem } from "./stock";

/** A thing between two businesses (GET /v1/link-chats/{link}/refs). */
export interface LinkRef {
  kind: "invoice" | "order";
  id: string;
  name: string;
  total_kobo: number;
  status: string;
  /** ours: we sent it, or they ordered from us; theirs: the other way. */
  side: "ours" | "theirs";
}

/** Our invoices, as the picker offers them. */
export const invoiceRefs = (list: CustomerInvoice[]): Suggestion[] =>
  list
    .filter((x) => x.status !== "draft")
    .map((x) => ({
      kind: "invoice",
      id: x.id,
      name: x.number,
      sub: `${x.customer.name} · ${naira(x.total_kobo)} · ${refStatusWords(x.status)}`,
      words: x.customer.name,
    }));

/** Our orders (shop, chat and trade), as the picker offers them. */
export const orderRefs = (
  list: { id: string; reference: string; total_kobo: number; status: string; customer?: { name?: string } }[],
): Suggestion[] =>
  list.map((o) => ({
    kind: "order",
    id: o.id,
    name: o.reference,
    sub: [o.customer?.name, naira(o.total_kobo), refStatusWords(o.status)].filter(Boolean).join(" · "),
    words: o.customer?.name,
  }));

/** Customers who write to the business, by name. */
export const customerRefs = (list: { id: string; name: string }[]): Suggestion[] =>
  list.filter((c) => c.name).map((c) => ({ kind: "customer", id: c.id, name: c.name }));

/** The business's own products (not their sizes), as "#" and "/product"
 * offer them in its Taskiem chat. */
export const productRefs = (items: StockItem[]): Suggestion[] =>
  items
    .filter((it) => !it.variant_of)
    .map((it) => ({
      kind: "item",
      id: `own:${it.id}`,
      name: it.name,
      sub:
        it.price_kobo !== undefined
          ? `Yours · ${naira(it.price_kobo)}${it.unit ? ` a ${it.unit}` : ""}`
          : "Yours · no price yet",
      category: it.category,
      words: it.description,
    }));

/** What is between us and a business, by kind: "Your invoice · ₦45,000 · sent". */
export function linkRefSuggestions(list: LinkRef[]): { invoice: Suggestion[]; order: Suggestion[] } {
  const one = (r: LinkRef): Suggestion => ({
    kind: r.kind,
    id: r.id,
    name: r.name,
    sub: `${
      r.kind === "invoice"
        ? r.side === "ours"
          ? "Your invoice"
          : "Their invoice"
        : r.side === "ours"
          ? "Their order with you"
          : "Your order with them"
    } · ${naira(r.total_kobo)} · ${refStatusWords(r.status)}`,
  });
  return {
    invoice: list.filter((r) => r.kind === "invoice").map(one),
    order: list.filter((r) => r.kind === "order").map(one),
  };
}

/** A thing a message may name with "#", to show as a chip that opens it. */
export interface KnownRef {
  kind: "invoice" | "order" | "product" | "customer";
  id: string;
  name: string;
}

/** The things of the picker's lists, as KnownRefs. */
export function knownRefs(lists: {
  invoice?: Suggestion[];
  order?: Suggestion[];
  customer?: Suggestion[];
  product?: Suggestion[];
}): KnownRef[] {
  const out: KnownRef[] = [];
  for (const k of ["invoice", "order", "customer", "product"] as const)
    for (const s of lists[k] ?? [])
      out.push({ kind: k, id: k === "product" ? s.id.replace(/^own:/, "") : s.id, name: s.name });
  return out;
}

/** A message's words in parts, each "#name" of a known thing marked (the
 * longest name first, so "#Rice 50kg" is not "#Rice"). */
export function refParts(text: string, known: KnownRef[]): { text: string; ref?: KnownRef }[] {
  if (known.length === 0 || !text.includes("#")) return [{ text }];
  const lower = text.toLowerCase();
  const byLength = [...known].sort((a, b) => b.name.length - a.name.length);
  const found: { at: number; len: number; ref: KnownRef }[] = [];
  for (const r of byLength) {
    const token = `#${r.name.toLowerCase()}`;
    for (let from = 0; ; ) {
      const at = lower.indexOf(token, from);
      if (at < 0) break;
      const end = at + token.length;
      // The name ends where a word ends.
      const boundary = end === lower.length || !/[\p{L}\p{N}-]/u.test(lower.charAt(end));
      if (boundary && !found.some((f) => at < f.at + f.len && f.at < end)) found.push({ at, len: token.length, ref: r });
      from = end;
    }
  }
  found.sort((a, b) => a.at - b.at);
  const out: { text: string; ref?: KnownRef }[] = [];
  let pos = 0;
  for (const f of found) {
    if (f.at > pos) out.push({ text: text.slice(pos, f.at) });
    out.push({ text: text.slice(f.at, f.at + f.len), ref: f.ref });
    pos = f.at + f.len;
  }
  if (pos < text.length) out.push({ text: text.slice(pos) });
  return out;
}
