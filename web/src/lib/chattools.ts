// Chat tools, phase 1 (docs/spec/chat-tools.md): what a send in a business
// conversation can answer beyond the message itself.

import type { LinkMessage } from "./linkchat";
import type { TaskiemMessage } from "./taskiemchat";

/** A copy sent to another linked business the message named. */
export interface AlsoSent {
  link: string;
  name: string;
  sent: boolean;
  reason?: string;
}

/** The answer to a send: the message (201/200), or Taskiem's answer (202). */
export interface SendAnswer {
  message?: LinkMessage;
  also?: AlsoSent[];
  messages?: TaskiemMessage[];
}

export interface Note {
  text: string;
  kind: "success" | "error" | "info";
  href?: string;
  linkText?: string;
}

const list = (names: string[]) =>
  names.length <= 1
    ? names.join("")
    : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;

/** What to tell the sender about where the message went, besides here. */
export function sendNotes(a: SendAnswer): Note[] {
  if (!a.message && a.messages) {
    return [
      {
        text: "Only Taskiem saw that. Its answer is in your Taskiem chat.",
        kind: "info",
        href: "/chat?with=taskiem",
        linkText: "Open",
      },
    ];
  }
  const out: Note[] = [];
  const sent = (a.also ?? []).filter((x) => x.sent).map((x) => x.name);
  if (sent.length > 0)
    out.push({ text: `Also sent to ${list(sent)}.`, kind: "success" });
  for (const x of (a.also ?? []).filter((x) => !x.sent))
    out.push({
      text: `Not sent to ${x.name}: ${x.reason ?? "try again"}.`,
      kind: "error",
    });
  return out;
}
