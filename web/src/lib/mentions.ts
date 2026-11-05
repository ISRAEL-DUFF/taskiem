// Mentions in the composer (business links L4, §5.5): "@" suggests linked
// businesses, "#" items with their unit and price; a pick puts the name
// in the text and, in a conversation, a structured reference beside it.

export interface Suggestion {
  kind: "business" | "item";
  /** The link (a business) or the seller's item id. */
  id: string;
  name: string;
  /** A second line: "₦9,500 a bag", or the business. */
  sub?: string;
}

/** A mention as a message stores it. */
export interface Mention {
  kind: "business" | "item";
  name: string;
  link?: string;
  item_id?: string;
  unit?: string;
  price_kobo?: number;
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

/** The suggestions a query fits: a word of the name starting with it
 * first, then the name holding it; six at most. */
export function suggest(list: Suggestion[], query: string): Suggestion[] {
  const q = query.trim().toLowerCase();
  const starts: Suggestion[] = [];
  const holds: Suggestion[] = [];
  for (const s of list) {
    const n = s.name.toLowerCase();
    if (
      q === "" ||
      n.startsWith(q) ||
      n.split(/\s+/).some((w) => w.startsWith(q))
    )
      starts.push(s);
    else if (n.includes(q)) holds.push(s);
  }
  return [...starts, ...holds].slice(0, 6);
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
    const token = (p.kind === "item" ? "#" : "@") + p.name.toLowerCase();
    if (
      lower.includes(token) &&
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
    const token = (m.kind === "item" ? "#" : "@") + m.name;
    let from = 0;
    for (;;) {
      const at = lower.indexOf(token.toLowerCase(), from);
      if (at < 0) break;
      if (!found.some((f) => at < f.at + f.len && f.at < at + token.length))
        found.push({ at, len: token.length, m });
      from = at + token.length;
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
