// Typed requests (docs/spec/chat-tools.md §3, phase 2): words that ask for
// something a tool does open that tool's form filled in. Nothing is ever
// saved from words: saving is always a person's tap on the form.

import type { ToolKey } from "./tools";

/** What a form starts with. */
export interface Prefill {
  priceKobo?: number;
  count?: number;
  name?: string;
  minutes?: number;
}

export interface Request {
  tool: ToolKey;
  /** The row it is about (by id), or a new one. */
  rowId?: string;
  add?: boolean;
  prefill: Prefill;
  /** The chip's words: "Change the price of Rice, 50kg bag to ₦80,000". */
  words: string;
}

export interface Named {
  id: string;
  name: string;
}

/** "80k", "₦80,000", "1.5m" as kobo; NaN when not an amount. */
export function amountKobo(t: string): number {
  const m = /^₦?\s*([\d,]+(?:\.\d+)?)\s*([km])?$/i.exec(t.trim());
  if (!m?.[1]) return NaN;
  const n = Number(m[1].replace(/,/g, ""));
  const times = m[2]?.toLowerCase() === "k" ? 1_000 : m[2]?.toLowerCase() === "m" ? 1_000_000 : 1;
  return Math.round(n * times * 100);
}

const naira = (kobo: number) => `₦${(kobo / 100).toLocaleString("en-NG")}`;

const words = (s: string) =>
  s
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s]/gu, " ")
    .split(/\s+/)
    .filter(Boolean);

/** The thing a few words name: every word of them found in its name, the
 * one with the fewest other words first; none when two fit as well. */
export function matchThing<T extends Named>(said: string, things: T[]): T | undefined {
  const want = words(said).filter((w) => !["the", "of", "a", "an", "my", "our"].includes(w));
  if (want.length === 0) return undefined;
  const fits = things
    .map((t) => ({ t, have: words(t.name) }))
    .filter(({ have }) => want.every((w) => have.some((h) => h.startsWith(w) || (w.length > 3 && h.startsWith(w.replace(/s$/, ""))))))
    .sort((a, b) => a.have.length - b.have.length);
  const [first, second] = fits;
  if (!first) return undefined;
  if (second && second.have.length === first.have.length) return undefined;
  return first.t;
}

const UNITS = "(?:bags?|cartons?|pieces?|pcs|packs?|crates?|kegs?|bottles?|tins?|boxes|box|units?|kg|tubers?|sacks?)";

/** The request a message makes, or null (most messages make none). */
export function parseRequest(text: string, products: Named[], orders: { id: string; reference: string; buyer: string }[]): Request | null {
  const t = text.trim().replace(/\s+/g, " ");
  if (t.length > 160) return null;

  // "change the price of rice 50kg to 80k", "rice 50kg price to ₦80,000"
  let m = /^(?:(?:please\s+)?(?:change|set|make|update|put)\s+)?(?:the\s+)?price\s+(?:of\s+)?(.+?)\s+(?:to|at|=|is)\s+(₦?\s*[\d,.]+\s*[km]?)\s*\.?$/i.exec(t);
  m ??= /^(.+?)\s+price\s+(?:to|is|now|=)\s+(₦?\s*[\d,.]+\s*[km]?)\s*\.?$/i.exec(t);
  if (m?.[1] && m[2]) {
    const kobo = amountKobo(m[2]);
    const it = matchThing(m[1], products);
    if (it && kobo > 0)
      return { tool: "products", rowId: it.id, prefill: { priceKobo: kobo }, words: `Change the price of ${it.name} to ${naira(kobo)}` };
  }

  // "20 bags of rice 25kg came in", "received 20 rice 25kg", "stock in 20 rice"
  m = new RegExp(`^(\\d+)\\s+(?:${UNITS}\\s+(?:of\\s+)?)?(.+?)\\s+(?:came in|arrived|have come|has come|just came)\\s*\\.?$`, "i").exec(t);
  m ??= new RegExp(`^(?:received|we received|got|we got|stock in|record stock in|add)\\s+(\\d+)\\s+(?:${UNITS}\\s+(?:of\\s+)?)?(.+?)(?:\\s+(?:to|into)\\s+(?:the\\s+)?stock)?\\s*\\.?$`, "i").exec(t);
  if (m?.[1] && m[2]) {
    const n = Number(m[1]);
    const it = matchThing(m[2], products);
    if (it && n > 0) return { tool: "stock", rowId: it.id, prefill: { count: n }, words: `Record ${n} of ${it.name} coming in` };
  }

  // "add a service: rice milling, 30 min, 2500"
  m = /^(?:add|new|create)\s+(?:a\s+)?service[:\s]+(.+?)[,\s]+(\d+)\s*(min|mins|minutes|hr|hrs|hour|hours)\b(?:[,\s]+(?:for\s+|at\s+)?(₦?\s*[\d,.]+\s*[km]?))?\s*\.?$/i.exec(t);
  if (m?.[1] && m[2] && m[3]) {
    const minutes = Number(m[2]) * (/^h/i.test(m[3]) ? 60 : 1);
    const kobo = m[4] ? amountKobo(m[4]) : NaN;
    const name = m[1].trim().replace(/[,:]$/, "");
    return {
      tool: "services",
      add: true,
      prefill: { name, minutes, ...(kobo > 0 ? { priceKobo: kobo } : {}) },
      words: `Add the service ${name}, ${minutes} min${kobo > 0 ? `, ${naira(kobo)}` : ""}`,
    };
  }

  // "accept order AD-7Q4K2P"
  m = /^accept\s+(?:the\s+)?(?:order\s+)?([A-Z]{1,4}-[A-Z0-9]{4,})\s*\.?$/i.exec(t);
  if (m?.[1]) {
    const ref = m[1].toUpperCase();
    const o = orders.find((x) => x.reference.toUpperCase() === ref);
    if (o) return { tool: "orders", rowId: o.id, prefill: {}, words: `Look at order ${o.reference} from ${o.buyer}, to accept it` };
  }
  return null;
}

/** Whether words might be a request at all (before anything is loaded). */
export const mightBeRequest = (text: string) =>
  /\b(price|came in|arrived|received|stock in|service|accept)\b/i.test(text) && text.trim().length <= 160;
