// Messages being sent from the Chat page, kept outside the conversation's
// page so that they outlive it: switching to another chat while one is
// being sent neither loses it nor shows it in the wrong thread, and a
// message that failed is still "Not sent" when you come back. One send at a
// time per conversation, in order. Cleared on sign-out.

import { useSyncExternalStore } from "react";
import { post, upload } from "../api";
import type { LinkMessage } from "./linkchat";
import { sendError, type Outgoing } from "./outbox";

const EMPTY: Outgoing[] = [];
const boxes = new Map<string, Outgoing[]>();
const watchers = new Set<() => void>();
const arrivals = new Set<(link: string, m: LinkMessage) => void>();
const chains = new Map<string, Promise<void>>();

/** How long a send that was refused as too fast waits before trying again, and how often. */
export const pacing = { waitMs: 2500, tries: 3 };

function put(link: string, list: Outgoing[]) {
  if (list.length === 0) boxes.delete(link);
  else boxes.set(link, list);
  watchers.forEach((w) => w());
}

/** The messages of a conversation not yet sent, oldest first. */
export function outboxOf(link: string): Outgoing[] {
  return boxes.get(link) ?? EMPTY;
}

/** The same, following changes. */
export function useOutbox(link: string): Outgoing[] {
  return useSyncExternalStore(
    (cb) => {
      watchers.add(cb);
      return () => watchers.delete(cb);
    },
    () => outboxOf(link),
  );
}

/** Hears every message the server accepted, with its conversation. */
export function onSent(fn: (link: string, m: LinkMessage) => void): () => void {
  arrivals.add(fn);
  return () => arrivals.delete(fn);
}

function patch(link: string, clientId: string, p: Partial<Outgoing>) {
  put(
    link,
    outboxOf(link).map((x) => (x.clientId === clientId ? { ...x, ...p } : x)),
  );
}

/** Adds fields to a message of the outbox (a photo's preview when it is read). */
export function updateOutgoing(
  link: string,
  clientId: string,
  p: Partial<Outgoing>,
) {
  patch(link, clientId, p);
}

const pause = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function attempt(link: string, clientId: string) {
  const find = () => outboxOf(link).find((x) => x.clientId === clientId);
  if (!find()) return; // deleted before its turn
  for (let n = 1; ; n++) {
    const o = find();
    if (!o) return;
    patch(link, clientId, {
      state: "sending",
      error: undefined,
      retryOnline: false,
    });
    try {
      const path = `/v1/link-chats/${link}`;
      let out: { message: LinkMessage };
      if (o.file) {
        const form = new FormData();
        form.append("file", o.file, o.file.name);
        if (o.body) form.append("text", o.body);
        if (o.replyTo) form.append("reply_to", o.replyTo);
        form.append("client_id", o.clientId);
        out = await upload<{ message: LinkMessage }>(`${path}/files`, form);
      } else {
        out = await post<{ message: LinkMessage }>(`${path}/messages`, {
          text: o.body,
          reply_to: o.replyTo,
          mentions:
            o.mentions && o.mentions.length > 0 ? o.mentions : undefined,
          client_id: o.clientId,
        });
      }
      put(
        link,
        outboxOf(link).filter((x) => x.clientId !== clientId),
      );
      arrivals.forEach((fn) => fn(link, out.message));
      return;
    } catch (e) {
      const status = (e as { status?: number } | undefined)?.status;
      // Refused as too fast: it waits its turn and tries again by itself.
      if (status === 429 && n < pacing.tries) {
        await pause(pacing.waitMs);
        continue;
      }
      patch(link, clientId, {
        state: "failed",
        error: sendError(e),
        // No connection: it goes again when the connection is back.
        retryOnline: !status,
      });
      return;
    }
  }
}

function enqueue(link: string, clientId: string) {
  const prev = chains.get(link) ?? Promise.resolve();
  chains.set(
    link,
    prev.then(() => attempt(link, clientId)).catch(() => undefined),
  );
}

/** Sends a message: it shows at once and is sent after those before it. */
export function sendOutgoing(link: string, o: Outgoing) {
  put(link, [...outboxOf(link), o]);
  enqueue(link, o.clientId);
}

/** Tries again a message that was not sent (the same send to the server). */
export function retryOutgoing(link: string, clientId: string) {
  if (outboxOf(link).some((x) => x.clientId === clientId)) {
    patch(link, clientId, { state: "sending", error: undefined });
    enqueue(link, clientId);
  }
}

/** Gives up a message that was not sent. */
export function discardOutgoing(link: string, clientId: string) {
  put(
    link,
    outboxOf(link).filter((x) => x.clientId !== clientId),
  );
}

/** Forgets everything unsent (sign-out). */
export function clearOutbox() {
  boxes.clear();
  chains.clear();
  watchers.forEach((w) => w());
}

/** The connection is back: what failed for want of it goes again. */
export function retryOffline() {
  for (const [link, list] of boxes)
    for (const o of list)
      if (o.state === "failed" && o.retryOnline)
        retryOutgoing(link, o.clientId);
}

if (
  typeof window !== "undefined" &&
  typeof window.addEventListener === "function"
)
  window.addEventListener("online", retryOffline);
