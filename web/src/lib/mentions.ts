import type { ExploreCard } from "./explore";
// Mentions in the composer (business links L4, §5.5): "@" suggests linked
// businesses, "#" items with their unit and price; a pick puts the name
// in the text and, in a conversation, a structured reference beside it.

export interface Suggestion {
  /** A linked business, an item of a price list, a service, Taskiem, or
   * a category of items (picking it opens them, filtered). */
  kind: "business" | "item" | "service" | "taskiem" | "category";
  /** The link (a business) or the seller's item id. */
  id: string;
  name: string;
  /** A second line: "₦9,500 a bag", or the business. */
  sub?: string;
  /** A short word on the right: "4 left". */
  badge?: string;
  /** A business we are not linked with, found in Explore: picking it
   * shows its card (with Connect) rather than tagging it. */
  explore?: ExploreCard;
  /** An item's category ("Sneakers") and its words (the storefront's
   * description): search reads both, after the name. */
  category?: string;
  words?: string;
}

/** A mention as a message stores it. */
export interface Mention {
  kind: "business" | "item" | "service";
  name: string;
  link?: string;
  item_id?: string;
  unit?: string;
  price_kobo?: number;
  /** One of the sender's own services, and how long it takes. */
  service_id?: string;
  minutes?: number;
}

export interface MentionQuery {
  mark: "@" | "#";
  query: string;
  /** Where the mark is in the text. */
  start: number;
}

/** The mention being typed before the caret: "@" or "#" at the start or
 * after a space, then up to 40 characters on the line; null when none. */
export function mentionQuery(text: string, caret: number): MentionQuery | null {
  const before = text.slice(0, caret);
  for (let i = before.length - 1; i >= 0 && before.length - i <= 41; i--) {
    const ch = before.charAt(i);
    if (ch === "\n") return null;
    if (
      (ch === "@" || ch === "#") &&
      (i === 0 || /\s|\(/.test(before.charAt(i - 1)))
    ) {
      return { mark: ch, query: before.slice(i + 1), start: i };
    }
  }
  return null;
}

/** Whether a word of the text starts with q. */
const wordStarts = (text: string, q: string) =>
  text.startsWith(q) || text.split(/\s+/).some((w) => w.startsWith(q));

/** The suggestions a query fits: a word of the name starting with it
 * first, then the name holding it, then those whose category or words
 * hold it ("nike" finds a shoe whose description says Nike); six at most. */
export function suggest(list: Suggestion[], query: string): Suggestion[] {
  const q = query.trim().toLowerCase();
  const starts: Suggestion[] = [];
  const holds: Suggestion[] = [];
  const about: Suggestion[] = [];
  for (const s of list) {
    const n = s.name.toLowerCase();
    if (q === "" || wordStarts(n, q)) starts.push(s);
    else if (n.includes(q)) holds.push(s);
    else if (
      (s.category ?? "").toLowerCase().includes(q) ||
      (s.words ?? "").toLowerCase().includes(q)
    )
      about.push(s);
  }
  return [...starts, ...holds, ...about].slice(0, 6);
}

/** The categories of a list with how many items each has, the most used
 * first (then by name). */
export function categoriesOf(
  list: { category?: string }[],
): { name: string; count: number }[] {
  const n = new Map<string, { name: string; count: number }>();
  for (const it of list) {
    const c = it.category?.trim();
    if (!c) continue;
    const k = c.toLowerCase();
    const got = n.get(k);
    if (got) got.count++;
    else n.set(k, { name: c, count: 1 });
  }
  return [...n.values()].sort(
    (a, b) => b.count - a.count || a.name.localeCompare(b.name),
  );
}

/** The categories a query names, as the picker's first rows: "Sneakers ·
 * 48 items"; two at most, none for an empty query. */
export function categorySuggestions(
  categories: { name: string; count: number }[],
  query: string,
): Suggestion[] {
  const q = query.trim().toLowerCase();
  if (q === "") return [];
  return categories
    .filter((c) => wordStarts(c.name.toLowerCase(), q))
    .slice(0, 2)
    .map((c) => ({
      kind: "category" as const,
      id: c.name,
      name: c.name,
      sub: `${c.count} ${c.count === 1 ? "item" : "items"}`,
    }));
}

/** The text with the query replaced by the picked name and a space, and
 * where the caret goes. */
export function insertMention(
  text: string,
  q: MentionQuery,
  caret: number,
  name: string,
): { text: string; caret: number } {
  const head = text.slice(0, q.start) + q.mark + name + " ";
  return { text: head + text.slice(caret), caret: head.length };
}

/** The mentions still named in the text (a name typed over is gone). */
export function stillNamed<T extends { kind: string; name: string }>(
  text: string,
  picked: T[],
): T[] {
  const lower = text.toLowerCase();
  const out: T[] = [];
  for (const p of picked) {
    const name = p.name.toLowerCase();
    // An item may be written "#Rice" or, from the picker, "@Rice".
    const named =
      lower.includes("@" + name) ||
      (p.kind !== "business" && lower.includes("#" + name));
    if (
      named &&
      !out.some((o) => o.kind === p.kind && o.name === p.name)
    )
      out.push(p);
  }
  return out;
}

/** A message's text in parts, its mentions marked, in order. */
export function mentionParts(
  text: string,
  mentions: Mention[] = [],
): { text: string; mention?: Mention }[] {
  if (mentions.length === 0) return [{ text }];
  const lower = text.toLowerCase();
  const found: { at: number; len: number; m: Mention }[] = [];
  for (const m of mentions) {
    const tokens = m.kind === "business" ? ["@" + m.name] : ["#" + m.name, "@" + m.name];
    for (const token of tokens) {
      let from = 0;
      for (;;) {
        const at = lower.indexOf(token.toLowerCase(), from);
        if (at < 0) break;
        if (!found.some((f) => at < f.at + f.len && f.at < at + token.length))
          found.push({ at, len: token.length, m });
        from = at + token.length;
      }
    }
  }
  found.sort((a, b) => a.at - b.at);
  const out: { text: string; mention?: Mention }[] = [];
  let pos = 0;
  for (const f of found) {
    if (f.at > pos) out.push({ text: text.slice(pos, f.at) });
    out.push({ text: text.slice(f.at, f.at + f.len), mention: f.m });
    pos = f.at + f.len;
  }
  if (pos < text.length) out.push({ text: text.slice(pos) });
  return out;
}

/** A group of the picker: "Products", "Services", "Businesses", or the
 * Taskiem row (no heading). */
export interface SuggestionGroup {
  label: string;
  items: Suggestion[];
}

export const TASKIEM: Suggestion = {
  kind: "taskiem",
  id: "taskiem",
  name: "Taskiem",
  sub: "Ask Taskiem to do it for you",
};

/** The picker's groups for "@" with chat tools (chat-tools.md §3): things
 * first (products, services), then businesses, then Taskiem; a few of
 * each, the ones a word of the name starts with first. */
export function groupSuggestions(
  query: string,
  src: {
    items: Suggestion[];
    services?: Suggestion[];
    businesses: Suggestion[];
    /** Found in Explore for these words: not linked, after the linked. */
    unlinked?: Suggestion[];
    /** The categories offered (the business's own, in its Taskiem chat);
     * else the items' own. */
    categories?: { name: string; count: number }[];
  },
  taskiem: boolean,
): SuggestionGroup[] {
  const out: SuggestionGroup[] = [];
  const add = (label: string, list: Suggestion[] | undefined, n: number, more: Suggestion[] = []) => {
    const got = suggest(list ?? [], query).slice(0, n);
    const names = new Set(got.map((s) => s.name.toLowerCase()));
    const all = [...got, ...more.filter((s) => !names.has(s.name.toLowerCase())).slice(0, 3)];
    if (all.length > 0) out.push({ label, items: all });
  };
  // A category the words name comes first: it opens its items, filtered.
  const cats = categorySuggestions(src.categories ?? categoriesOf(src.items), query);
  if (cats.length > 0) out.push({ label: "Categories", items: cats });
  add("Products", src.items, 4);
  add("Services", src.services, 3);
  add("Businesses", src.businesses, 4, src.unlinked);
  // Taskiem is always offered last, as the designs show: it can do what
  // the words ask, whatever they name.
  if (taskiem) out.push({ label: "", items: [TASKIEM] });
  return out;
}

/** A business found in Explore, as the picker lists it: "Not linked ·
 * Yaba" (or where the asking stands). Linked ones and those we blocked
 * are not offered here. */
export function unlinkedSuggestion(c: ExploreCard): Suggestion | null {
  const where = c.area ? ` · ${c.area}` : "";
  switch (c.state) {
    case "none":
    case "ended":
    case "not_accepted":
      return { kind: "business", id: c.business, name: c.name, sub: `Not linked${where}`, explore: c };
    case "requested":
      return { kind: "business", id: c.business, name: c.name, sub: "Not linked · you asked to link", explore: c };
    case "asked_us":
      return { kind: "business", id: c.business, name: c.name, sub: "Not linked · asked you to link", explore: c };
    default:
      return null;
  }
}
