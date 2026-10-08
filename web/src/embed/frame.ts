// The frame page's side of the postMessage protocol (docs/embedding.md#
// iframe-mode). Messages are checked both ways: the frame accepts a token
// only from its parent window and only from one of the app's origins (and,
// once it has one, only from that same origin), and it posts only to that
// origin, never to "*".

/** A message between the frame and the page that framed it. */
export interface FrameMessage {
  type: string;
  [k: string]: unknown;
}

interface MessageLike {
  source: unknown;
  origin: string;
  data: unknown;
}

/**
 * Returns the token a message carries if it is a token message from the
 * parent window, from one of the app's origins and, once the frame has a
 * parent origin, from that one; otherwise null.
 */
export function checkMessage(e: MessageLike, parent: unknown, origins: string[], parentOrigin: string): { token: string } | null {
  if (e.source !== parent || !origins.includes(e.origin)) return null;
  if (parentOrigin && e.origin !== parentOrigin) return null;
  const d = e.data as Record<string, unknown> | null;
  if (!d || typeof d !== "object" || d.type !== "taskiem:token") return null;
  const token = d.token;
  if (typeof token !== "string" || !/^tsk_eut_[A-Za-z0-9_-]{20,100}$/.test(token)) return null;
  return { token };
}

/**
 * Where to announce the frame (taskiem:ready): the parent's origin when the
 * browser tells it (location.ancestorOrigins) and it is one of the app's;
 * nowhere when it is not; otherwise each of the app's origins, of which
 * only the parent's receives it.
 */
export function frameTargets(origins: string[], ancestor: string | undefined): string[] {
  if (ancestor) return origins.includes(ancestor) ? [ancestor] : [];
  return origins;
}
