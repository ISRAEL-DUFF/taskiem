// What "/invoice", "/bill", "/order", "/purchase", "/product" and
// "/customer" offer to tag, the cards of what is tagged
// (docs/spec/chat-five-more-flows.md, phase 2 additions), and the
// business's own products on "#" in the Taskiem chat. Pure, tested.

import type { Suggestion } from "./mentions";
import { refStatusWords } from "./mentions";
import { naira } from "./invoices";
import type { StockItem } from "./stock";

export type ChatRefKind = "invoice" | "bill" | "order" | "purchase" | "customer";

/** A thing that may be tagged (GET /v1/chat-refs, /link-chats/{link}/refs). */
export interface ChatRef {
  kind: ChatRefKind;
  id: string;
  /** Its number or reference, or a customer's name. */
  name: string;
  /** Who it is with: a business, a customer (masked); a customer's number. */
  party?: string;
  when: string;
  total_kobo?: number;
  status?: string;
}

/** A thing's card, read now (GET /v1/chat-card, /link-chats/{link}/card). */
export interface ChatCard extends ChatRef {
  due_on?: string;
  paid_kobo?: number;
  due_kobo?: number;
  lines?: { name: string; quantity: number; total_kobo: number }[];
  more?: number;
  orders?: number;
  owes_kobo?: number;
  last_order?: { reference: string; total_kobo: number; status: string; when: string };
  action?: "send" | "remind" | "pay" | "message";
}

/** What each kind is called, on a row's badge and a card's kicker. */
export const KIND_WORD: Record<ChatRefKind, string> = {
  invoice: "Invoice",
  bill: "Bill",
  order: "Order",
  purchase: "Your order",
  customer: "Customer",
};

/** "9 Nov", or "9 Nov 2025" in another year. */
export function shortDate(iso: string, now = new Date()): string {
  const d = new Date(iso);
  return d.toLocaleDateString("en-GB", {
    day: "numeric",
    month: "short",
    ...(d.getFullYear() !== now.getFullYear() ? { year: "numeric" } : {}),
  });
}

/** A row of the picker: "Ada's Kitchen · 3 Nov · ₦45,000 · overdue". */
export function refSuggestion(r: ChatRef, now = new Date()): Suggestion {
  const sub =
    r.kind === "customer"
      ? [r.party, `last wrote ${shortDate(r.when, now)}`]
      : [r.party, shortDate(r.when, now), r.total_kobo !== undefined ? naira(r.total_kobo) : "", r.status ? refStatusWords(r.status) : ""];
  return {
    kind: r.kind,
    id: r.id,
    name: r.name,
    sub: sub.filter(Boolean).join(" · "),
    badge: r.kind === "customer" ? undefined : KIND_WORD[r.kind],
    words: r.party,
  };
}

/** What each "/" word offers: "/invoice" invoices and bills, "/order"
 * orders and purchases, the rest their own. */
export function refSources(list: ChatRef[], now = new Date()): Record<"invoice" | "bill" | "order" | "purchase" | "customer", Suggestion[]> {
  const of = (...kinds: ChatRefKind[]) => list.filter((r) => kinds.includes(r.kind)).map((r) => refSuggestion(r, now));
  return {
    invoice: of("invoice", "bill"),
    bill: of("bill"),
    order: of("order", "purchase"),
    purchase: of("purchase"),
    customer: of("customer"),
  };
}

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

/** A thing a message may name with "#", to show as a chip and a card. */
export interface KnownRef {
  kind: ChatRefKind | "product";
  id: string;
  name: string;
}

/** The things the Taskiem chat knows, as KnownRefs. */
export function knownRefs(list: ChatRef[], products: Suggestion[] = []): KnownRef[] {
  return [
    ...list.map((r) => ({ kind: r.kind, id: r.id, name: r.name })),
    ...products.map((s) => ({ kind: "product" as const, id: s.id.replace(/^own:/, ""), name: s.name })),
  ];
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

/** The things a message names that have cards, once each, three at most. */
export function cardRefs(text: string, known: KnownRef[]): KnownRef[] {
  const out: KnownRef[] = [];
  for (const p of refParts(text, known))
    if (p.ref && p.ref.kind !== "product" && !out.some((r) => r.kind === p.ref?.kind && r.id === p.ref.id)) out.push(p.ref);
  return out.slice(0, 3);
}

/** The card's pill: where it stands, and its tone. */
export function cardPill(c: Pick<ChatCard, "kind" | "status" | "due_kobo">): { text: string; tone: "lime" | "warn" | "danger" | "plain" } | undefined {
  if (!c.status) return undefined;
  const s = c.status;
  const text = refStatusWords(s);
  const word = text.charAt(0).toUpperCase() + text.slice(1);
  if (s === "paid" || s === "delivered" || s === "completed" || s === "received") return { text: word, tone: "lime" };
  if (s === "overdue" || s === "expired" || s === "declined" || s === "cancelled" || s === "void") return { text: word, tone: s === "overdue" ? "danger" : "plain" };
  if ((c.due_kobo ?? 0) > 0 || s === "part_paid" || s === "disputed" || s === "awaiting_payment") return { text: word, tone: "warn" };
  return { text: word, tone: "plain" };
}

/** How much of it is paid, from 0 to 1, for the bar. */
export const paidShare = (c: Pick<ChatCard, "total_kobo" | "paid_kobo">) =>
  c.total_kobo && c.total_kobo > 0 ? Math.min(1, Math.max(0, (c.paid_kobo ?? 0) / c.total_kobo)) : 0;

/** The card's one action, in words. */
export const ACTION_WORD: Record<NonNullable<ChatCard["action"]>, string> = {
  send: "Send it",
  remind: "Send a reminder",
  pay: "Pay",
  message: "Message them",
};

/** The invoices, bills and orders a business conversation's message names,
 * once each, three at most (a customer is never named there). */
export function docMentions(
  mentions: { kind: string; name: string }[] | undefined,
): { kind: "invoice" | "bill" | "order" | "purchase"; name: string }[] {
  const out: { kind: "invoice" | "bill" | "order" | "purchase"; name: string }[] = [];
  for (const m of mentions ?? [])
    if ((m.kind === "invoice" || m.kind === "bill" || m.kind === "order" || m.kind === "purchase") && !out.some((x) => x.name === m.name))
      out.push({ kind: m.kind, name: m.name });
  return out.slice(0, 3);
}
