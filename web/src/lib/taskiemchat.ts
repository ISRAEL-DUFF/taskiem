// The Taskiem conversation on the web (business links L3, §5.5): the
// API's shapes, and how its messages merge and read.

import { cmpTime } from "./stamp";

export interface TaskiemButton {
  id?: string;
  title: string;
}

export interface TaskiemMessage {
  id: string;
  channel: "whatsapp" | "telegram" | "slack" | "web";
  inbound: boolean;
  kind: string;
  text: string;
  buttons?: TaskiemButton[];
  created_at: string;
  /** Being sent from this page: shown at once, until the answer comes. */
  local?: boolean;
}

export const NIL = "00000000-0000-0000-0000-000000000000";

/** Messages merged by id, oldest first; one not kept (a code) has no id and stays as sent. */
export function mergeTaskiem(
  a: TaskiemMessage[],
  b: TaskiemMessage[],
): TaskiemMessage[] {
  // A message of ours that is being sent, and whose stored copy has now come
  // (by the stream, before the answer to the request): the copy replaces it.
  const arrived = b.filter(
    (m) =>
      !m.local &&
      m.inbound &&
      m.channel === "web" &&
      !a.some((x) => x.id === m.id),
  );
  const sent = (m: TaskiemMessage) => {
    if (!m.local) return false;
    const at = arrived.findIndex((x) => x.text === m.text);
    if (at < 0) return false;
    arrived.splice(at, 1);
    return true;
  };
  const seen = new Map<string, TaskiemMessage>();
  let n = 0;
  for (const m of [...a.filter((x) => !sent(x)), ...b]) {
    const key = m.id === NIL ? `local-${n++}-${m.created_at}` : m.id;
    const had = seen.get(key);
    // The history keeps a message's buttons by title only; the answer that
    // was just given carries their ids, which are what presses them. The
    // copy with the ids is kept.
    const keep =
      had?.buttons?.some((x) => x.id) && !m.buttons?.some((x) => x.id);
    seen.set(key, keep ? { ...m, buttons: had?.buttons } : m);
  }
  return [...seen.values()].sort((x, y) => cmpTime(x.created_at, y.created_at));
}

/** A message's text in parts, WhatsApp's *bold* marked. */
export function boldParts(text: string): { text: string; bold: boolean }[] {
  return text
    .split(/(\*[^*\n]+\*)/g)
    .filter((p) => p !== "")
    .map((p) =>
      /^\*[^*\n]+\*$/.test(p)
        ? { text: p.slice(1, -1), bold: true }
        : { text: p, bold: false },
    );
}

/** A message being sent when the page went away, kept until its answer came. */
export type Inflight = { text: string; at: string };

/**
 * What became of it, from the history read on coming back: it is there (the
 * server had it: nothing to do), or it is not (it may never have gone: the
 * words go back to the box, and the person is told to look before sending).
 */
export function inflightOutcome(
  messages: TaskiemMessage[],
  f: Inflight,
): "sent" | "not-sent" {
  const from = Date.parse(f.at) - 60_000;
  return messages.some(
    (m) => m.inbound && m.text === f.text && Date.parse(m.created_at) >= from,
  )
    ? "sent"
    : "not-sent";
}

const INFLIGHT = "taskiem-chat-inflight";
export function keepInflight(f: Inflight | undefined): void {
  try {
    if (f) sessionStorage.setItem(INFLIGHT, JSON.stringify(f));
    else sessionStorage.removeItem(INFLIGHT);
  } catch {
    // Storage may be blocked: a reload then loses it, as before.
  }
}
/**
 * Forgets the message kept as on its way, only if it is still `f`: a page
 * that has sent one of its own since keeps that one.
 */
export function forgetInflight(f: Inflight): void {
  const now = inflight();
  if (now && now.text === f.text && now.at === f.at) keepInflight(undefined);
}
export function inflight(): Inflight | undefined {
  try {
    const v = sessionStorage.getItem(INFLIGHT);
    return v ? (JSON.parse(v) as Inflight) : undefined;
  } catch {
    return undefined;
  }
}
