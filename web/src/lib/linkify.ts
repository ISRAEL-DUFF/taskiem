// The web addresses in a chat message's words
// (docs/spec/chat-previews-and-editing.md §2.2). One rule with the app
// (mobile/lib/chat/linkify.dart) and the server (engine/linkify), tested
// against the same cases (linkify.cases.json).
//
// What counts: http:// and https:// addresses, and bare ones such as
// jumia.com.ng/phones or www.example.com, taken as https. Nothing else:
// no other scheme, no email address, no address with a user name in it.

export type FoundLink = {
  /** Offsets of `text` in the words (UTF-16, as JavaScript counts). */
  start: number;
  end: number;
  /** The address as written. */
  text: string;
  /** What it opens: https:// in front of a bare one. */
  url: string;
};

// No look-behind, as Go's expressions have none: what comes before and
// after a candidate is checked by hand, the same in every language.
const candidate =
  /https?:\/\/[^\s<>"]+|(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,24}(?::[0-9]{1,5})?(?:[/?#][^\s<>"]*)?/gi;

const bareTLDs = new Set(
  "com net org ng io co app dev ai info biz me shop store online site xyz tech africa edu gov uk us ca za ke gh de fr ly gl tv link page blog news".split(
    " ",
  ),
);

const trailing = `.,;:!?)"'`;

const isWord = (c: string) => /^[A-Za-z0-9_]$/.test(c);

function validHost(h: string): boolean {
  if (!h || h.startsWith(".") || h.endsWith(".") || h.includes("..")) return false;
  return /^[a-z0-9.-]+$/.test(h);
}

const count = (s: string, c: string) => s.split(c).length - 1;

/** Every web address in text, in order. */
export function findLinks(text: string): FoundLink[] {
  const out: FoundLink[] = [];
  for (const m of text.matchAll(candidate)) {
    const start = m.index ?? 0;
    const end = start + m[0].length;
    if (start > 0) {
      const prev = text[start - 1] ?? "";
      if (isWord(prev) || "@._/-:".includes(prev)) continue;
    }
    if (end < text.length) {
      const next = text[end] ?? "";
      if (next === "@" || isWord(next)) continue;
    }
    let raw = m[0];
    while (raw && trailing.includes(raw[raw.length - 1] ?? "")) {
      if (raw.endsWith(")") && count(raw, "(") >= count(raw, ")")) break;
      raw = raw.slice(0, -1);
    }
    const lower = raw.toLowerCase();
    const scheme = lower.startsWith("http://") || lower.startsWith("https://");
    let host = scheme ? raw.slice(raw.indexOf("://") + 3) : raw;
    const cut = host.search(/[/?#]/);
    if (cut >= 0) host = host.slice(0, cut);
    if (!host || host.includes("@")) continue;
    let name = host.toLowerCase();
    const colon = name.lastIndexOf(":");
    if (colon >= 0) name = name.slice(0, colon);
    if (!scheme) {
      if (!name.startsWith("www.") && !bareTLDs.has(name.slice(name.lastIndexOf(".") + 1))) continue;
    } else if (!validHost(name)) continue;
    const i = raw.indexOf("://");
    const url = scheme ? lower.slice(0, i) + raw.slice(i) : "https://" + raw;
    out.push({ start, end: start + raw.length, text: raw, url });
  }
  return out;
}

/** The first web address in text, or "". */
export function firstLink(text: string): string {
  return findLinks(text)[0]?.url ?? "";
}

/** How a long address is shown, shortened in the middle
 * (jumia.com.ng/…/phones); it opens in full. Counted in characters. */
export function shownLink(text: string): string {
  const most = 40;
  const chars = (s: string) => [...s];
  if (chars(text).length <= most) return text;
  let rest = text;
  const i = rest.indexOf("://");
  if (i >= 0) rest = rest.slice(i + 3);
  let host = rest;
  let path = "";
  const slash = rest.indexOf("/");
  if (slash >= 0) {
    host = rest.slice(0, slash);
    path = rest.slice(slash + 1);
  }
  const segs = path.split("/").filter(Boolean);
  if (segs.length > 0) {
    let last = segs[segs.length - 1] ?? "";
    if (chars(last).length > 20) last = chars(last).slice(0, 19).join("") + "…";
    if (chars(host).length <= 30) return segs.length === 1 ? `${host}/${last}` : `${host}/…/${last}`;
  }
  const r = chars(rest);
  if (r.length <= most) return rest;
  return r.slice(0, 24).join("") + "…" + r.slice(r.length - 15).join("");
}

/** Words split into plain parts and links, for showing them. */
export function linkParts(text: string): { text: string; link?: FoundLink }[] {
  const out: { text: string; link?: FoundLink }[] = [];
  let at = 0;
  for (const l of findLinks(text)) {
    if (l.start > at) out.push({ text: text.slice(at, l.start) });
    out.push({ text: l.text, link: l });
    at = l.end;
  }
  if (at < text.length) out.push({ text: text.slice(at) });
  return out;
}
