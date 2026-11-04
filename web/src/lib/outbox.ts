// A message being sent from the Chat page (WhatsApp Web's way): it shows in
// the thread at once with a clock, then the server's copy takes its place;
// if it cannot be sent it shows "Not sent" with Retry and Delete. Every
// send carries a client id, so a Retry after a dropped connection is the
// same message to the server, never a second one.

import type { Attachment, LinkMessage, Quote } from "./linkchat";

export interface Outgoing {
  clientId: string;
  body: string;
  /** The message quoted (its id). */
  replyTo?: string;
  quote?: Quote;
  file?: File;
  /** A photo's preview, a data URL. */
  preview?: string;
  mentions?: unknown[];
  state: "sending" | "failed";
  error?: string;
  /** Failed for want of a connection: it goes again when it is back. */
  retryOnline?: boolean;
  at: string;
}

/** An id for one send. */
export function newClientId(): string {
  const c = globalThis.crypto;
  if (c && typeof c.randomUUID === "function") return c.randomUUID();
  const hex = (n: number) =>
    Array.from({ length: n }, () =>
      Math.floor(Math.random() * 16).toString(16),
    ).join("");
  return `${hex(8)}-${hex(4)}-4${hex(3)}-a${hex(3)}-${hex(12)}`;
}

/** What kind of attachment a local file will be. */
export function attachmentOf(f: File): Attachment {
  const kind = f.type.startsWith("audio/")
    ? "voice"
    : f.type.startsWith("image/")
      ? "photo"
      : "file";
  return { kind, type: f.type, size: f.size, name: f.name };
}

/** The outgoing message as a message of the thread, ours, not yet sent. */
export function asMessage(o: Outgoing, link: string): LinkMessage {
  return {
    id: `local-${o.clientId}`,
    link,
    mine: true,
    kind: "text",
    body: o.body || undefined,
    attachment: o.file ? attachmentOf(o.file) : undefined,
    reply: o.quote,
    created_at: o.at,
    local: o.state,
    localError: o.error,
    localPreview: o.preview,
  };
}

/** Why a send failed, in words for the person: what happened, and what to do. */
export function sendError(e: unknown): string {
  const status = (e as { status?: number } | undefined)?.status;
  const message = e instanceof Error ? e.message : "";
  if (status === 429)
    return "You are sending too fast. Wait a moment and tap Retry.";
  if (status === 409 || status === 400 || status === 403 || status === 404)
    return message || "It could not be sent.";
  if (!status)
    return "There is no connection. Tap Retry when you are back online.";
  return "It could not be sent. Tap Retry.";
}

/** Why a small action (a reaction, a star) failed, in words for the person. */
export function actionError(e: unknown): Error {
  const status = (e as { status?: number } | undefined)?.status;
  const message = e instanceof Error ? e.message : "";
  if (!status)
    return new Error(
      "There is no connection, so that was not done. Try again when you are back online.",
    );
  if (status === 429)
    return new Error(
      "You are doing that too fast. Wait a moment and try again.",
    );
  return new Error(message || "That could not be done. Try again.");
}
