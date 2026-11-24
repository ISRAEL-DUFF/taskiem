// A search result's line: the words around the first place the search is
// found, cut to a width, and the found words marked.

export type Part = { text: string; hit: boolean };

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** The query's words, at least a letter long, longest first. */
export function terms(q: string): string[] {
  return Array.from(new Set(q.toLowerCase().split(/\s+/).filter(Boolean))).sort(
    (a, b) => b.length - a.length,
  );
}

/** `text` in parts, those that are the query's words marked as hits. */
export function highlight(text: string, q: string): Part[] {
  const t = terms(q);
  if (t.length === 0) return [{ text, hit: false }];
  const re = new RegExp(`(${t.map(escape).join("|")})`, "gi");
  // split() with a group gives the pieces between hits at even places and
  // the hits at odd ones, so the places are read before the empty ones go.
  return text
    .split(re)
    .map((p, i) => ({ text: p, hit: i % 2 === 1 }))
    .filter((p) => p.text !== "");
}

/** The words around the first hit, `width` characters at most, with "…" where cut. */
export function around(text: string, q: string, width = 140): string {
  const flat = text.replace(/\s+/g, " ").trim();
  if (flat.length <= width) return flat;
  const low = flat.toLowerCase();
  let at = -1;
  for (const t of terms(q)) {
    const i = low.indexOf(t);
    if (i >= 0 && (at < 0 || i < at)) at = i;
  }
  if (at < 0) at = 0;
  const start = Math.max(0, Math.min(at - 30, flat.length - width));
  const end = Math.min(flat.length, start + width);
  return `${start > 0 ? "…" : ""}${flat.slice(start, end).trim()}${end < flat.length ? "…" : ""}`;
}
