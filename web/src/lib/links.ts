// Business links (decision 0061; docs/spec/business-links.md §5): the API's
// shapes (GET /v1/links, /v1/links/search, /v1/link-settings), how a link
// reads in a row, and how the Chat page's list is grouped.

import { naira } from "./invoices";

/** How we trade with them: they supply us, they buy from us, or both. */
export type LinkRole = "supplier" | "customer" | "both";

/** A link as our side sees it. A request we made reads "requested" until
 * it is accepted, whatever the other side did, then "not_accepted" after 30
 * days; we never learn of a decline or a block. */
export type LinkState = "active" | "ended" | "requested" | "not_accepted" | "asked_us" | "blocked";

export interface BusinessLink {
  id: string;
  business: string;
  name: string;
  slug?: string;
  state: LinkState;
  role: LinkRole;
  asked_by_us: boolean;
  requested_at: string;
  expires_at: string;
  answered_at?: string;
}

/** A request sent by phone number or storefront address: whether anyone
 * has it, we never know. */
export interface BlindLinkRequest {
  id: string;
  kind: "phone" | "address";
  shown: string;
  role: LinkRole;
  state: "sent";
  created_at: string;
  expires_at: string;
}

export interface LinksView {
  links: BusinessLink[];
  sent: BlindLinkRequest[];
}

/** A business a search found: only what its public page shows. */
export interface LinkCard {
  business: string;
  name: string;
  slug?: string;
  area?: string;
  tagline?: string;
  items: { name: string; price_kobo?: number | null }[];
  state: LinkState | "none";
}

export interface LinkSearch {
  results: LinkCard[];
  /** A phone number typed: the one row that sends a request to it. */
  blind?: { kind: "phone"; shown: string };
}

export interface LinkSettings {
  findable: boolean | null;
  findable_now: boolean;
  asked_at?: string;
}

/** What may be asked of the server about a link. */
export type LinkAnswer = "accept" | "decline" | "block" | "unblock" | "cancel" | "end";

/** The choices a requester picks from, in their words. */
export const ROLE_CHOICES: { role: LinkRole; label: string }[] = [
  { role: "supplier", label: "They supply us" },
  { role: "customer", label: "They buy from us" },
  { role: "both", label: "Both" },
];

/** How we trade with a linked business, as a row's line. */
export function roleLine(role: LinkRole): string {
  switch (role) {
    case "supplier":
      return "They supply you";
    case "customer":
      return "They buy from you";
    default:
      return "They supply you and buy from you";
  }
}

type Tone = "ok" | "warn" | "info" | "muted";

/** A link's state in a word or two, with its tone. */
export function stateWords(state: LinkState | "sent" | "none"): { label: string; tone: Tone } | null {
  switch (state) {
    case "active":
      return { label: "Linked", tone: "ok" };
    case "asked_us":
      return { label: "Asks to link", tone: "warn" };
    case "requested":
      return { label: "Asked", tone: "info" };
    case "sent":
      return { label: "Request sent", tone: "info" };
    case "not_accepted":
      return { label: "Not accepted", tone: "muted" };
    case "blocked":
      return { label: "Blocked", tone: "muted" };
    case "ended":
      return { label: "Link ended", tone: "muted" };
    default:
      return null;
  }
}

/** The Chat page's list, most urgent first: requests to answer, the
 * linked businesses, what we asked and wait for, then the rest. */
export function groupLinks(v: LinksView | undefined): {
  asked: BusinessLink[];
  linked: BusinessLink[];
  waiting: BusinessLink[];
  sent: BlindLinkRequest[];
  past: BusinessLink[];
} {
  const links = v?.links ?? [];
  const by = (s: LinkState[]) => links.filter((l) => s.includes(l.state)).sort((a, b) => a.name.localeCompare(b.name));
  return {
    asked: links.filter((l) => l.state === "asked_us"),
    linked: by(["active"]),
    waiting: by(["requested", "not_accepted"]),
    sent: v?.sent ?? [],
    past: by(["ended", "blocked"]),
  };
}

/** Linked businesses whose name has every word of the query, as the
 * search's Chats group lists them. */
export function matchLinked(links: BusinessLink[], q: string): BusinessLink[] {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [];
  return links.filter((l) => l.state === "active" && words.every((w) => l.name.toLowerCase().includes(w)));
}

/** Whether what was typed is a phone number rather than words: never a
 * search, only a request sent to it (the server decides the same way). */
export function looksLikePhone(q: string): boolean {
  return (q.match(/\d/g) ?? []).length >= 7 && /^[+\d\s\-()]+$/.test(q.trim());
}

/** A business found, in one line: its area, then the items that matched
 * with the prices its page shows. */
export function cardSub(c: LinkCard): string {
  const items = c.items.map((it) => (it.price_kobo != null ? `${it.name} ${naira(it.price_kobo)}` : it.name));
  return [c.area, ...items].filter(Boolean).join(" · ") || c.tagline || "On Taskiem";
}

/** Whether we may ask a business found to link, or it is ours to answer,
 * or there is nothing to do. */
export function cardAction(c: LinkCard): "request" | "answer" | null {
  if (c.state === "none" || c.state === "ended") return "request";
  if (c.state === "asked_us") return "answer";
  return null;
}

/** What "Let other businesses find us" says, from the settings. */
export function findableLine(s: LinkSettings): string {
  if (s.findable === null) {
    return s.findable_now
      ? "On, because your storefront is published: businesses find you by your name, what you sell and your area."
      : "Off until you publish your storefront or turn it on.";
  }
  return s.findable ? "On: businesses find you by your name, what you sell and your area." : "Off: only someone who has your phone number or page address can ask.";
}
