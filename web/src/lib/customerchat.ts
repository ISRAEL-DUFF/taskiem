// Customers' conversations (docs/spec/chat-five-more-flows.md, phase 1):
// a customer who writes to the business's own WhatsApp number is a thread
// in the chat; staff who handle orders answer there, and the answer goes
// from the business's number. Pure helpers, tested on their own.

export interface CustomerChat {
  id: string;
  /** Their name in the business's book, or their number. */
  name: string;
  /** Empty when their details were erased. */
  phone: string;
  last: string;
  last_mine: boolean;
  last_at: string;
  unread: number;
  /** A reply goes as words: within 24 hours of their last message. */
  window_open: boolean;
  window_until: string | null;
  /** They sent STOP: nothing is sent until they send START. */
  stopped: boolean;
  archived: boolean;
  /** One thread only: what they owe on open invoices. */
  owes_kobo?: number;
  open_invoices?: number;
}

export interface CustomerMessage {
  id: string;
  /** From the business. */
  mine: boolean;
  kind: "text" | "media";
  body: string;
  sender_name?: string;
  sent_as?: "text" | "template";
  /** A reply's way to the customer, as WhatsApp reports it. */
  delivery?: "sending" | "sent" | "delivered" | "read" | "failed";
  /** Why WhatsApp could not deliver it. */
  failure?: string;
  read_at: string | null;
  created_at: string;
  /** Shown before the server answers. */
  local?: boolean;
}

/** The list's and the URL's key for a thread: "c-" and its id. */
export const customerKey = (id: string) => `c-${id}`;
export const customerID = (key: string | null): string | undefined =>
  key?.startsWith("c-") ? key.slice(2) : undefined;

/** What the box says about how a reply goes, or null when it goes as
 * words with nothing to say. */
export function replyNote(
  c: Pick<CustomerChat, "stopped" | "window_open" | "phone">,
): {
  tone: "warn" | "stop";
  text: string;
} | null {
  if (c.stopped)
    return {
      tone: "stop",
      text: "They asked not to get messages (they sent STOP). Nothing can be sent until they send START.",
    };
  if (!c.phone)
    return { tone: "stop", text: "This customer's details were erased." };
  if (!c.window_open)
    return {
      tone: "warn",
      text: "More than a day since they last wrote: your reply goes as a WhatsApp message they answer to carry on.",
    };
  return null;
}

/** "Owes ₦12,500 on 2 invoices", or "" when they owe nothing. */
export function owesWords(
  kobo: number | undefined,
  invoices: number | undefined,
  naira: (k: number) => string,
): string {
  if (!kobo || kobo <= 0) return "";
  const n = invoices ?? 0;
  return `Owes ${naira(kobo)}${n > 0 ? ` on ${n} invoice${n === 1 ? "" : "s"}` : ""}`;
}

/** Messages merged by id, oldest first (a page, a send, a poll). */
export function mergeCustomer(
  cur: CustomerMessage[],
  more: CustomerMessage[],
): CustomerMessage[] {
  const byID = new Map(cur.map((m) => [m.id, m]));
  for (const m of more) byID.set(m.id, m);
  return [...byID.values()].sort(
    (a, b) =>
      Date.parse(a.created_at) - Date.parse(b.created_at) ||
      (a.id < b.id ? -1 : 1),
  );
}

/** The thread after its latest page is fetched again: the page in place of
 * what it covers (so a message deleted on the server goes), the older
 * messages before it kept when there are more, and sends still under way. */
export function latestPage(
  cur: CustomerMessage[],
  page: CustomerMessage[],
  more: boolean,
): CustomerMessage[] {
  const from = page[0] ? Date.parse(page[0].created_at) : Infinity;
  const kept = cur.filter(
    (m) => m.local || (more && Date.parse(m.created_at) < from),
  );
  return mergeCustomer(kept, page);
}

/** A number as people say it: "+234 803 111 2222" (a Nigerian number in
 * its groups; another country's, as given). */
export function phoneWords(phone: string): string {
  const m = /^\+234(\d{3})(\d{3})(\d{4})$/.exec(phone);
  return m ? `+234 ${m[1]} ${m[2]} ${m[3]}` : phone;
}

/** What the thread is called: the customer's name in the book, or their
 * number as people say it. */
export function customerTitle(c: Pick<CustomerChat, "name" | "phone">): string {
  return c.name === c.phone ? phoneWords(c.phone) : c.name;
}

/** The letters in the round picture: a known customer's initials, or "C"
 * (a customer) for one known by their number only. */
export function customerLetter(
  c: Pick<CustomerChat, "name" | "phone">,
): string {
  return c.name === c.phone || /^\+?\d/.test(c.name) ? "Customer" : c.name;
}

/** A message a search found in a customer's thread. */
export interface CustomerHit {
  chat_id: string;
  message_id: string;
  name: string;
  mine: boolean;
  body: string;
  created_at: string;
}

/** The longest reply that can go after the 24 hours (the template's room). */
export const LATE_REPLY_MAX = 900;

/** Whether a reply is too long to go now: after the 24 hours it goes on
 * one line, up to LATE_REPLY_MAX characters. */
export function lateTooLong(text: string, windowOpen: boolean): boolean {
  if (windowOpen) return false;
  return lateLength(text) > LATE_REPLY_MAX;
}

/** A reply's length as it goes after the 24 hours: on one line. */
export function lateLength(text: string): number {
  return [...text.trim().split(/\s+/).join(" ")].length;
}
