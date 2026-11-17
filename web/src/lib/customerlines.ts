// One customer across the app and the business's own WhatsApp number
// (docs/spec/taskiem-for-everyone.md §11): a thread with both lines, where
// a reply goes, and what each line says of itself. Pure helpers, tested on
// their own.

import { appNote } from "./customerlinks";
import { replyNote, type CustomerChat } from "./customerchat";

export type Line = "whatsapp" | "app";

/** One of a customer's lines in a thread (CustomerThreadLine). */
export interface CustomerThreadLine {
  line: Line;
  id: string;
  /** The number on the line; empty once erased. */
  phone: string;
  /** A reply goes as words now (WhatsApp: within 24 hours of their last
   * message there; the app: always, unless blocked). */
  window_open: boolean;
  window_until: string | null;
  /** WhatsApp: STOP. The app: they blocked the business, or left. */
  stopped: boolean;
  /** The business blocked them in the app. */
  blocked: boolean;
  notice?: string;
  last_in_at: string | null;
}

type ThreadOf = Pick<CustomerChat, "line" | "phone" | "window_open" | "window_until" | "stopped" | "blocked" | "notice"> & {
  id?: string;
  lines?: CustomerThreadLine[];
  reply_line?: Line;
};

/** The thread's lines: as the server says, or the one line a thread had
 * before there were two. */
export function threadLines(c: ThreadOf): CustomerThreadLine[] {
  if (c.lines && c.lines.length > 0) return c.lines;
  return [
    {
      line: c.line ?? "whatsapp",
      id: c.id ?? "",
      phone: c.phone,
      window_open: c.window_open,
      window_until: c.window_until,
      stopped: c.stopped,
      blocked: !!c.blocked,
      notice: c.notice,
      last_in_at: null,
    },
  ];
}

/** Both lines: the customer writes on WhatsApp and in the app. */
export const bothLines = (c: ThreadOf) => threadLines(c).length > 1;

/** "on WhatsApp", "in the app": where a message went. */
export const lineWords = (l: Line) => (l === "app" ? "in the app" : "on WhatsApp");

/** Why a reply cannot go on a line now, or null when it can (on WhatsApp
 * after the 24 hours it still goes, as a message they answer). */
export function lineClosed(l: CustomerThreadLine): string | null {
  if (!l.phone) return "Their details were erased";
  if (l.line === "whatsapp") return l.stopped ? "They sent STOP on WhatsApp" : null;
  if (l.blocked) return "You blocked them in the app";
  if (l.stopped) return l.notice || "They stopped messages in the app";
  return null;
}

/** The line a reply goes on: the one chosen while it can take a message,
 * else where the server says (the line they last wrote on). */
export function chosenLine(c: ThreadOf, picked: Line | undefined): Line {
  const lines = threadLines(c);
  const p = picked && lines.find((l) => l.line === picked);
  if (p && !lineClosed(p)) return p.line;
  return c.reply_line ?? c.line ?? lines[0]?.line ?? "whatsapp";
}

/** What the box says about a reply on this line, or null when it goes as
 * words with nothing to say. */
export function lineNote(l: CustomerThreadLine): { tone: "warn" | "stop"; text: string } | null {
  if (l.line === "app") {
    if (!l.phone) return { tone: "stop", text: "This customer's details were erased." };
    return appNote(l);
  }
  const n = replyNote(l);
  if (n) return n;
  // A thread parted from the app when its customer moved their number.
  if (l.notice) return { tone: "warn", text: `${l.notice}.` };
  return null;
}

/** What a line of a thread with both says of itself while it still takes
 * a reply (a line that cannot is said by the chooser: STOP on WhatsApp, a
 * block in the app). */
export function lineStates(c: ThreadOf): { line: Line; text: string }[] {
  const lines = threadLines(c);
  if (lines.length < 2) return [];
  return lines.filter((l) => l.notice && !lineClosed(l)).map((l) => ({ line: l.line, text: `${l.notice}.` }));
}

/** The small mark after a customer's name in the list. */
export function listMark(c: ThreadOf): string {
  if (bothLines(c)) return "WhatsApp · App";
  return c.line === "app" ? "in the app" : "";
}

/** A row's preview for a customer who cannot take messages on the line a
 * reply goes on. */
export function stoppedPreview(c: ThreadOf): string {
  return (c.reply_line ?? c.line) === "app" ? "Stopped messages" : "Sent STOP";
}

/** The reply's body: the line it goes on, for a thread with both. */
export function replyLineBody(c: ThreadOf, line: Line): { line?: Line } {
  return bothLines(c) ? { line } : {};
}
