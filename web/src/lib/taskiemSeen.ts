import { useSyncExternalStore } from "react";
import type { TaskiemMessage } from "./taskiemchat";
import { isAfter } from "./stamp";

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

// The unread ones are kept by id (with their time), not as a number: the same
// message told twice (the stream's look-back after a reload, the count read
// on loading) is one message. Another tab seeing them moves the seen time
// (a "storage" event), which clears what it passed.
let unread = new Map<string, string>();
let count = 0;
const subs = new Set<() => void>();
const set = (next: Map<string, string>) => {
  unread = next;
  if (next.size !== count) {
    count = next.size;
    subs.forEach((f) => f());
  }
};

/** Said by Taskiem after `seen`: where the unread begin and how many. */
export function unreadAfter(
  messages: TaskiemMessage[],
  seen: string | undefined,
): { id: string; count: number } | undefined {
  if (!seen) return undefined;
  const later = messages.filter(
    (m) => !m.inbound && isAfter(m.created_at, seen),
  );
  const first = later[0];
  return first ? { id: first.id, count: later.length } : undefined;
}

/** Everything up to `iso` is seen (never goes back); nothing is unread. */
export function markSeen(iso: string): void {
  const was = seenAt();
  try {
    if (!was || isAfter(iso, was)) localStorage.setItem(KEY, iso);
  } catch {
    // Storage may be blocked: the count is then only kept while the page lives.
  }
  set(new Map());
}

/** A first visit begins at now. */
export function beginSeen(now = new Date().toISOString()): void {
  if (!seenAt()) markSeen(now);
}

/** More unread ones, told by the stream (the ones already known are not counted again). */
export function addTaskiemUnread(messages: TaskiemMessage[]): void {
  const next = new Map(unread);
  for (const m of messages) next.set(m.id, m.created_at);
  set(next);
}

if (typeof window !== "undefined")
  window.addEventListener("storage", (e) => {
    if (e.key !== KEY || !e.newValue) return;
    const seen = e.newValue;
    set(new Map([...unread].filter(([, at]) => isAfter(at, seen))));
  });

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
