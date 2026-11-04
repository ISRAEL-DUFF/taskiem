// The conversation between linked businesses (business links L3;
// docs/spec/business-links.md §5.5, §8): the API's shapes, and how a
// message, a card and the list read, in WhatsApp's words.

import { naira } from "./invoices";
import { cmpTime } from "./stamp";
import type { Mention } from "./mentions";

export type DocKind = "order" | "purchase" | "invoice" | "bill";

/** A document event in the conversation, naming our own document. */
export interface DocCard {
  doc: DocKind;
  event: string;
  reference: string;
  id?: string;
  amount_kobo?: number;
  note?: string;
}

/** One emoji on a message: how many gave it, whether you did, and who (first names). */
export interface Reaction {
  emoji: string;
  count: number;
  mine?: boolean;
  by?: string[];
}

/** The six emoji a message may be reacted to with. */
export const REACTIONS = ["👍", "❤️", "😂", "😮", "😢", "🙏"] as const;

/** The reactions with `emoji` given or taken back by us, as the server will answer. */
export function toggleReaction(
  list: Reaction[] | undefined,
  emoji: string,
  mine: boolean,
  me = "You",
): Reaction[] {
  // Our own reaction (one at most) comes off wherever it was.
  const out: Reaction[] = [];
  for (const r of list ?? []) {
    if (!r.mine) out.push(r);
    else if (r.count > 1)
      out.push({
        ...r,
        count: r.count - 1,
        mine: false,
        by: r.by?.filter((n) => n !== me),
      });
  }
  if (!mine) return out;
  const at = out.findIndex((r) => r.emoji === emoji);
  if (at >= 0) {
    const r = out[at] as Reaction;
    out[at] = {
      ...r,
      count: r.count + 1,
      mine: true,
      by: [...(r.by ?? []), me],
    };
  } else out.push({ emoji, count: 1, mine: true, by: [me] });
  return out;
}

/** The words for a reaction chip: "👍 2: Owner, Ada". */
export function reactionWords(r: Reaction): string {
  const who = (r.by ?? []).join(", ");
  return `${r.emoji} ${r.count}${who ? `: ${who}` : ""}`;
}

export interface Quote {
  pair_id: string;
  mine: boolean;
  kind: "text" | "card";
  body?: string;
  card?: DocCard;
  attachment?: Attachment;
}

/** A photo, a PDF or a voice note sent with a message; each business
 * keeps its own copy, streamed by fileURL. */
export interface Attachment {
  kind: "photo" | "file" | "voice";
  type: string;
  size: number;
  name?: string;
}

/** A link's preview: the page's host, title and description. */
export interface LinkPreview {
  url: string;
  host: string;
  title: string;
  description?: string;
}

export interface LinkMessage {
  id: string;
  link: string;
  mine: boolean;
  kind: "text" | "card";
  body?: string;
  sender_name?: string;
  card?: DocCard;
  attachment?: Attachment;
  forwarded?: boolean;
  preview?: LinkPreview;
  mentions?: Mention[];
  reply?: Quote;
  read_at?: string;
  created_at: string;
  /** The people's emoji on it, from both businesses (see Reaction). */
  reactions?: Reaction[];
  /** You starred it (a star is yours alone). */
  starred?: boolean;
  /** Only in the browser: a message still being sent, or not sent (Retry). */
  local?: "sending" | "failed";
  /** Why a local message was not sent, in words. */
  localError?: string;
  /** A local photo's preview (a data URL: the page shows no blob URLs). */
  localPreview?: string;
}

export interface Conversation {
  link: string;
  business: string;
  name: string;
  state: "active" | "ended";
  last?: LinkMessage;
  unread: number;
  muted: boolean;
  archived: boolean;
  pinned: boolean;
  typing: boolean;
}

const VERB: Record<string, string> = {
  ordered: "sent order",
  auto_accepted: "sent order (accepted by itself)",
  auto_changed: "sent order (changed to our prices by itself)",
  proposed: "drafted order",
  confirmed: "confirmed order",
  accept: "accepted order",
  decline: "declined order",
  change: "asked to change order",
  change_accepted: "accepted the change to order",
  ready: "has ready order",
  dispatched: "sent on its way order",
  delivered: "delivered order",
  cancel: "cancelled order",
  cancel_agreed: "cancelled, as asked, order",
  cancelled_by_buyer: "cancelled order",
  cancel_asked: "asked to cancel order",
  keep: "kept order",
  received: "received all of order",
  short: "said not all came of order",
  agree: "agreed the count for order",
  bill: "sent invoice",
  declared: "said it paid invoice",
  queried: "queried invoice",
  resolved: "settled the query on invoice",
  reminder: "sent a reminder about invoice",
};

/** A card in words: "You sent order P-7QX4KD", "Dangote Stores sent invoice INV-0042". */
export function cardLine(card: DocCard, mine: boolean, other: string): string {
  const who = mine ? "You" : other;
  const verb =
    VERB[card.event] ??
    `${card.event.replaceAll("_", " ")} ${card.doc === "invoice" || card.doc === "bill" ? "invoice" : "order"}`;
  return `${who} ${verb} ${card.reference}`;
}

/** Where a card's document opens. */
export function cardHref(card: DocCard): string {
  const id = encodeURIComponent(card.id ?? card.reference);
  switch (card.doc) {
    case "order":
      return `/trade-orders/${id}`;
    case "purchase":
      return `/purchases/${id}`;
    case "invoice":
      return `/invoices/${id}`;
    default:
      return `/bills/${id}`;
  }
}

/** A card's second line: the amount, and a word. */
export function cardSub(card: DocCard): string {
  return [
    card.amount_kobo !== undefined ? naira(card.amount_kobo) : "",
    card.note ? `“${card.note}”` : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

/** What an attachment is, in a word: "Photo", "Voice note", or the file's name. */
export function attachmentLabel(a: Attachment): string {
  if (a.kind === "photo") return "Photo";
  if (a.kind === "voice") return "Voice note";
  return a.name || "File";
}

/** A file's size: 820 KB, 1.4 MB. */
export function fileSize(bytes: number): string {
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/** Where this business's own copy of a message's file is streamed from. */
export function fileURL(m: Pick<LinkMessage, "link" | "id">): string {
  return `/v1/link-chats/${encodeURIComponent(m.link)}/files/${encodeURIComponent(m.id)}`;
}

/** A message as one line: the list's preview, and a quote. */
export function previewLine(
  m: Pick<LinkMessage, "mine" | "kind" | "body" | "card" | "attachment">,
  other: string,
): string {
  if (m.kind === "card" && m.card) return cardLine(m.card, m.mine, other);
  const words = m.attachment
    ? [attachmentLabel(m.attachment), m.body].filter(Boolean).join(": ")
    : (m.body ?? "");
  return (m.mine ? "You: " : "") + words;
}

/** The day a message falls in, as the stream's divider says it. */
export function dayLabel(iso: string, now: Date = new Date()): string {
  const d = new Date(iso);
  const day = (x: Date) =>
    new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day(now) - day(d)) / 86_400_000);
  if (diff === 0) return "Today";
  if (diff === 1) return "Yesterday";
  if (diff < 7) return d.toLocaleDateString("en-GB", { weekday: "long" });
  return d.toLocaleDateString("en-GB", {
    day: "numeric",
    month: "long",
    year: "numeric",
  });
}

/** A message's time in its bubble's corner: 09:41. */
export function clock(iso: string): string {
  return new Date(iso).toLocaleTimeString("en-GB", {
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** The list's time: today's clock, else the day. */
export function listTime(iso: string, now: Date = new Date()): string {
  const label = dayLabel(iso, now);
  return label === "Today" ? clock(iso) : label;
}

/** Messages grouped by day, oldest first. */
export function byDay(
  list: LinkMessage[],
  now: Date = new Date(),
): { day: string; messages: LinkMessage[] }[] {
  const out: { day: string; messages: LinkMessage[] }[] = [];
  for (const m of list) {
    const day = dayLabel(m.created_at, now);
    const last = out[out.length - 1];
    if (last && last.day === day) last.messages.push(m);
    else out.push({ day, messages: [m] });
  }
  return out;
}

/** Messages merged by id, oldest first (a newer page after an older one). */
export function merge(a: LinkMessage[], b: LinkMessage[]): LinkMessage[] {
  const seen = new Map<string, LinkMessage>();
  for (const m of [...a, ...b]) seen.set(m.id, m);
  return [...seen.values()].sort(
    (x, y) =>
      cmpTime(x.created_at, y.created_at) || x.id.localeCompare(y.id),
  );
}
