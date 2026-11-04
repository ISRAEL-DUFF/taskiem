// The email trigger (decision 0038) in an owner's words.
import type { EmailMessage } from "../api";

/** Senders typed as a list ("alerts@bank.ng, shop.ng" or one per line):
 * addresses and domains, lower-cased, without repeats or blanks. */
export function parseSenders(text: string): string[] {
  const out: string[] = [];
  for (const s of text.split(/[\s,;]+/)) {
    const v = s.trim().toLowerCase().replace(/^<|>$/g, "");
    if (v && !out.includes(v)) out.push(v);
  }
  return out;
}

const SENDER = /^([^@\s]+@)?@?[a-z0-9.-]+\.[a-z]{2,}$/;

/** Senders that are neither an address nor a domain. */
export function badSenders(list: string[]): string[] {
  return list.filter((s) => !SENDER.test(s));
}

/** What came of a message, as a short phrase. */
export function messageWords(m: EmailMessage): string {
  switch (m.status) {
    case "started":
      return m.attachments > 0 ? `Started a run, with ${m.attachments} attachment${m.attachments > 1 ? "s" : ""}` : "Started a run";
    case "confirmation":
      return "A request to confirm forwarding";
    default:
      return m.note ?? "Not started";
  }
}

/** Who sent a message, as far as Taskiem shows it without opening it. */
export function fromWords(m: EmailMessage): string {
  return m.sender_domain ? `From ${m.sender_domain}` : "From an unknown sender";
}
