// Editing a sent message (docs/spec/chat-previews-and-editing.md §3): the
// rules the menu shows Edit by, the same in every chat; the server decides
// (PATCH …/messages/{id} {text, edits}) and these only keep Edit off where
// it would be refused.

import { api, ApiError } from "../api";
import type { EditTarget } from "../pages/ChatComposer";
import { toast } from "../toast";

/** How long after sending a message may be edited. */
export const EDIT_WINDOW_MS = 30 * 60 * 1000;

/** Why a message cannot be edited, as the server says it (403 not_editable). */
export type EditReason =
  | "not_yours"
  | "too_old"
  | "card"
  | "money"
  | "taskiem_acted"
  | "deleted"
  | "forwarded"
  | "voice"
  | "elsewhere";

/** What of a message, in any chat, the rules read. */
export type Editable = {
  id: string;
  mine: boolean;
  kind: string;
  body?: string;
  created_at: string;
  edits?: number;
  deleted?: boolean;
  forwarded?: boolean;
  attachment?: { kind: string } | null;
  card?: unknown;
  payment?: string | null;
  batch?: string | null;
  mentions?: unknown[];
  offer?: boolean;
  line?: string;
  /** Still waiting to send (the outbox): changed there, not here. */
  local?: unknown;
};

const MONEY_TAGS = new Set(["item", "service", "invoice", "bill", "order", "purchase"]);

/** Why the person may not edit m now, or undefined when they may. */
export function editBlock(m: Editable, now = Date.now()): EditReason | undefined {
  if (m.deleted) return "deleted";
  if (m.card || m.offer || (m.kind !== "text" && m.kind !== "media")) return "card";
  if (!m.mine || m.local) return "not_yours";
  if (m.payment || m.batch) return "money";
  for (const t of m.mentions ?? []) {
    const k = (t as { kind?: unknown } | null)?.kind;
    if (typeof k === "string" && MONEY_TAGS.has(k)) return "money";
    if (k === "saved") return "card";
  }
  if (m.attachment?.kind === "voice") return "voice";
  if (m.forwarded) return "forwarded";
  if (m.line === "whatsapp") return "elsewhere";
  if (now - Date.parse(m.created_at) > EDIT_WINDOW_MS) return "too_old";
  return undefined;
}

/** Whether the menu offers Edit for m now. */
export const canEdit = (m: Editable, now = Date.now()) =>
  editBlock(m, now) === undefined;

/** m as the composer edits it. */
export const editTarget = (m: Editable): EditTarget => ({
  id: m.id,
  text: m.body ?? "",
  edits: m.edits ?? 0,
  caption: !!m.attachment,
});

/** The latest of list the person may edit now (the up arrow). */
export function lastEditable(
  list: readonly Editable[] | undefined,
  now = Date.now(),
): EditTarget | undefined {
  const mine = (list ?? []).filter((m) => m.mine && !m.local);
  const m = mine[mine.length - 1];
  return m && canEdit(m, now) ? editTarget(m) : undefined;
}

/** An edit's body. */
export const editBody = (t: EditTarget, text: string) => ({ text, edits: t.edits });

/** What a refused edit says, in plain words, for the toast. */
export function editRefusal(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.body.stale === true)
      return "This message was changed on another device. Its newer words are shown.";
    if (e.body.not_editable === "too_old")
      return "This message can no longer be edited. Your words are back in the box, to send as a new message.";
    if (e.message) return e.message;
  }
  return "The edit was not saved. Try again.";
}

/** Whether a refused edit was refused for its time (the words go back to the box). */
export const refusedForTime = (e: unknown) =>
  e instanceof ApiError && e.body.not_editable === "too_old";

/**
 * Saves an edit: the bubble shows the new words and "Edited" at once, and
 * goes back if the server refuses, with the reason in a toast; refused for
 * its time, the words go back to the box (to send as a new message); stale,
 * the chat is read again for the newer words.
 */
export async function saveEdit<M extends Editable>(o: {
  url: string;
  t: EditTarget;
  text: string;
  set: (fn: (cur: M[] | undefined) => M[] | undefined) => void;
  /** The message in the answer (a business chat's is under `message`). */
  unwrap?: (r: unknown) => M;
  keepWords: (words: string) => void;
  reload?: () => void;
}): Promise<boolean> {
  let before: M | undefined;
  o.set((cur) =>
    cur?.map((x) => {
      if (x.id !== o.t.id) return x;
      before = x;
      return editedNow(x, o.text);
    }),
  );
  try {
    const r = await api("PATCH", o.url, editBody(o.t, o.text));
    const m = o.unwrap ? o.unwrap(r) : (r as M);
    o.set((cur) => cur?.map((x) => (x.id === m.id ? { ...x, ...m } : x)));
    return true;
  } catch (e) {
    const was = before;
    if (was) o.set((cur) => cur?.map((x) => (x.id === o.t.id ? was : x)));
    toast(editRefusal(e), "error");
    if (refusedForTime(e)) o.keepWords(o.text);
    if (e instanceof ApiError && e.body.stale === true) o.reload?.();
    return false;
  }
}

/** m with its new words shown at once, before the server answers. */
export function editedNow<M extends Editable>(m: M, text: string): M {
  return { ...m, body: text, edited_at: new Date().toISOString(), edits: (m.edits ?? 0) + 1 };
}
