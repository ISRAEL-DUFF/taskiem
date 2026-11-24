// How a conversation's thread scrolls (the Chat page, as WhatsApp Web's):
// stay at the bottom only when the reader is there, mark where the unread
// messages begin, and count what arrived while they read further up.

import type { LinkMessage } from "./linkchat";
import { isAfter } from "./stamp";

/** How close to the bottom still counts as "at the bottom", in pixels. */
export const NEAR_BOTTOM = 80;

/** Whether a scroll position is at (or near) the bottom of its content. */
export function atBottom(m: {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
}): boolean {
  return m.scrollHeight - m.scrollTop - m.clientHeight <= NEAR_BOTTOM;
}

/**
 * Where to scroll to show the newest message at the bottom of the view,
 * when something follows it in the thread (the alert cards after the
 * Taskiem conversation's messages): the scrollTop that puts the end of the
 * newest message, less a little room, at the view's end. Undefined when
 * nothing of note follows it, so the end of the thread is the place.
 * `newestEnd` is how far down the content the newest message ends.
 */
export function newestTop(m: {
  scrollHeight: number;
  clientHeight: number;
  newestEnd: number;
}): number | undefined {
  if (m.scrollHeight - m.newestEnd <= NEAR_BOTTOM) return undefined;
  return Math.max(0, m.newestEnd + 16 - m.clientHeight);
}

/** Whether the reader is at the newest message: the thread's end, or the
 * newest message ending within the view, with only what follows it below. */
export function atNewest(m: {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
  newestEnd?: number;
}): boolean {
  if (atBottom(m)) return true;
  if (m.newestEnd === undefined) return false;
  return (
    m.newestEnd <= m.scrollTop + m.clientHeight + NEAR_BOTTOM &&
    m.newestEnd > m.scrollTop
  );
}

/** The first message the other side sent that this side has not read, and how
 * many there are: where "N unread messages" goes. */
export function firstUnread(
  messages: LinkMessage[],
): { id: string; count: number } | undefined {
  const unread = messages.filter((m) => !m.mine && !m.read_at);
  const first = unread[0];
  return first ? { id: first.id, count: unread.length } : undefined;
}

/** What a thread does when its messages change. */
export type Follow =
  | { kind: "none" }
  | { kind: "bottom" }
  | { kind: "pill"; count: number };

/**
 * `prevNewest` is the newest message's time before the change. Messages that
 * arrived after it are new (older ones paged in above, or a message changing
 * in place, are not). The reader is taken to the bottom when they were there
 * or when they sent the new one; otherwise a count is shown for the pill.
 */
export function followNew<T extends { created_at: string }>(
  prevNewest: string | undefined,
  messages: T[],
  wasAtBottom: boolean,
  isMine: (m: T) => boolean = (m) => (m as { mine?: boolean }).mine === true,
): Follow {
  if (!prevNewest) return { kind: "none" };
  const arrived = messages.filter((m) => isAfter(m.created_at, prevNewest));
  if (arrived.length === 0) return { kind: "none" };
  if (wasAtBottom || arrived.some(isMine)) return { kind: "bottom" };
  return { kind: "pill", count: arrived.filter((m) => !isMine(m)).length };
}

/**
 * Scrolls the thread (and only the thread) so that `target` is at its top
 * or its middle. `scrollIntoView` also scrolls every page around it, which
 * pushes the header of a full-window chat out of sight.
 */
export function scrollWithin(
  thread: HTMLElement,
  target: Element,
  block: "start" | "center",
): void {
  const t = target.getBoundingClientRect();
  const s = thread.getBoundingClientRect();
  // At the top, clear of the sticky day chip.
  const gap = block === "start" ? 40 : Math.max(0, (s.height - t.height) / 2);
  thread.scrollTop += t.top - s.top - gap;
}

/** Whether a message starts a run: the first of its day, or after one from
 * the other side or another sender, or a document card. The first of a run
 * has the bubble's tail and the sender's name; the rest sit close under it. */
export function startsRun(
  prev: Pick<LinkMessage, "mine" | "kind" | "sender_name"> | undefined,
  m: Pick<LinkMessage, "mine" | "kind" | "sender_name">,
): boolean {
  if (!prev) return true;
  if (prev.kind === "card" || m.kind === "card") return true;
  return prev.mine !== m.mine || prev.sender_name !== m.sender_name;
}
