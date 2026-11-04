import { useSyncExternalStore } from "react";
import type { TaskiemMessage } from "./taskiemchat";

// What Taskiem has said that the person has not yet seen on the web. The
// conversation has no read marks on the server (Taskiem reads at once), so
// "seen" is a time kept in this browser: what Taskiem said after it, and not
// at the screen, is unread. The first time there is nothing unread: it
// begins now.

const KEY = "taskiem-chat-seen";

export function seenAt(): string | undefined {
  try {
    return localStorage.getItem(KEY) ?? undefined;
  } catch {
    return undefined;
  }
}

let count = 0;
const subs = new Set<() => void>();
const tell = () => subs.forEach((f) => f());

/** Said by Taskiem after `seen`: where the unread begin and how many. */
export function unreadAfter(
  messages: TaskiemMessage[],
  seen: string | undefined,
): { id: string; count: number } | undefined {
  if (!seen) return undefined;
  const later = messages.filter((m) => !m.inbound && m.created_at > seen);
  const first = later[0];
  return first ? { id: first.id, count: later.length } : undefined;
}

/** Everything up to `iso` is seen (never goes back); nothing is unread. */
export function markSeen(iso: string): void {
  const was = seenAt();
  try {
    if (!was || iso > was) localStorage.setItem(KEY, iso);
  } catch {
    // Storage may be blocked: the count is then only kept while the page lives.
  }
  if (count !== 0) {
    count = 0;
    tell();
  }
}

/** A first visit begins at now. */
export function beginSeen(now = new Date().toISOString()): void {
  if (!seenAt()) markSeen(now);
}

export function setTaskiemUnread(n: number): void {
  if (n === count) return;
  count = n;
  tell();
}
export const addTaskiemUnread = (n: number) => setTaskiemUnread(count + n);

export function useTaskiemUnread(): number {
  return useSyncExternalStore(
    (f) => {
      subs.add(f);
      return () => subs.delete(f);
    },
    () => count,
    () => 0,
  );
}
