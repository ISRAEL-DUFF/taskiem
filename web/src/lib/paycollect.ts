// Collecting people to pay from several messages (paying people, P3;
// docs/spec/chat-pay-people.md, "Pay several people", From messages):
// "Add to the list" beside a message's "Pay this account" keeps a row on
// the person's own list, kept by the server across every chat
// (/v1/pay-collection), so "3 to pay" stays at the foot of each chat until
// the list is paid as a batch or cleared. Here: a row as the server takes
// it from a message's reading, where each row came from in words and as a
// link back, and the words of the foot.

import { naira } from "./invoices";
import type { PayReading } from "./paymessage";
import { payAmountKobo, purposeOf, type PaymentPurpose } from "./payments";

/** The kind of chat a message is in, as the server keeps a row's source. */
export type ChatKind = "taskiem" | "link" | "customer" | "person" | "group";

/** Where a row came from: the chat's kind (photo for a chat's photo), the
 * chat and the message. A photo's chat is "<kind>:<id>". */
export interface CollectedSource {
  kind: ChatKind | "photo";
  chat: string;
  message: string;
  /** Who sent it (a group's name), kept by the server for every device. */
  from?: string;
}

/** A row of the list (GET /v1/pay-collection), the account masked. */
export interface CollectedRow {
  id: string;
  saved_id: string | null;
  /** The saved account's @name, when it is one. */
  payee: string;
  title: string;
  /** The name the message gave. */
  name: string;
  bank_name: string;
  last4: string;
  amount_kobo: number | null;
  purpose: string;
  note: string;
  source: CollectedSource | null;
  added_at: string;
}

/** The person's list, and the batch made from it waiting to be sent. */
export interface Collection {
  rows: CollectedRow[];
  count: number;
  total_kobo: number;
  no_amount: number;
  in_batch: string | null;
}

/** The chat a chip or a photo is in. */
export interface ChatPlace {
  kind: ChatKind;
  id: string;
  /** Who is on the other side (a group's name), to say where a row came from. */
  name?: string;
}

/** The source a message's row is added with. */
export function sourceFor(chat: ChatPlace, message: string, photo = false): CollectedSource {
  const from = chat.name?.trim() ? { from: chat.name.trim().slice(0, 80) } : {};
  return photo ? { kind: "photo", chat: `${chat.kind}:${chat.id}`, message, ...from } : { kind: chat.kind, chat: chat.id, message, ...from };
}

/** The body of POST /v1/pay-collection for a message's reading: the saved
 * account by its @name when there is one (the only way for staff, as the
 * chip pays), else the number and its bank; the amount and the name the
 * message gives. bank is the bank chosen when none was read. */
export function addBody(r: PayReading, source: CollectedSource, bank?: string): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  const payee = (r.payee ?? "").trim().replace(/^@/, "");
  if (payee) body.payee = payee;
  else {
    body.account_number = r.account_number ?? "";
    body.bank_name = (bank ?? r.bank_name ?? "").trim();
  }
  if (r.amount_kobo && r.amount_kobo > 0) body.amount_kobo = r.amount_kobo;
  const name = r.name?.trim();
  if (name) body.name = name;
  body.source = source;
  return body;
}

/** Whether adding needs a bank chosen first: an account number with no
 * bank read and no saved account. */
export const needsBank = (r: PayReading) => !r.payee?.trim() && !r.bank_name?.trim();

/** Whether this message is on the list already. */
export function onList(list: Collection | undefined, source: CollectedSource): boolean {
  return !!list?.rows.some((x) => x.source && x.source.kind === source.kind && x.source.chat === source.chat && x.source.message === source.message);
}

/** "3 to pay". */
export const toPayWords = (n: number) => `${n} to pay`;

/** Said once a row is added: "Added: 3 to pay". */
export const addedWords = (list: Pick<Collection, "count">) => `Added: ${toPayWords(list.count)}`;

/** The foot of a chat: "3 to pay · ₦95,000", and "1 needs an amount". */
export function footWords(list: Pick<Collection, "count" | "total_kobo" | "no_amount">): { main: string; needs?: string } {
  const main = list.total_kobo > 0 ? `${toPayWords(list.count)} · ${naira(list.total_kobo)}` : toPayWords(list.count);
  if (list.no_amount <= 0) return { main };
  return { main, needs: list.no_amount === 1 ? "1 needs an amount" : `${list.no_amount} need an amount` };
}

/** The chat a row came from, unwrapping a photo's. */
export function sourceChat(s: CollectedSource | null): { kind: ChatKind; id: string; photo: boolean } | null {
  if (!s || !s.chat) return null;
  if (s.kind !== "photo") return { kind: s.kind, id: s.chat, photo: false };
  const at = s.chat.indexOf(":");
  if (at <= 0) return null;
  const kind = s.chat.slice(0, at) as ChatKind;
  if (!["taskiem", "link", "customer", "person", "group"].includes(kind)) return null;
  return { kind, id: s.chat.slice(at + 1), photo: true };
}

/** Where a row's chat opens, at its message where the chat can. */
export function chatPath(s: CollectedSource | null): string | null {
  const c = sourceChat(s);
  if (!c || !c.id) return null;
  const id = encodeURIComponent(c.id);
  switch (c.kind) {
    case "link":
      return `/chat?with=${id}&msg=${encodeURIComponent(s?.message ?? "")}`;
    case "customer":
      return `/chat?with=c-${id}`;
    case "person":
      return `/you/chats/${id}`;
    case "group":
      return `/you/groups/${id}`;
    case "taskiem":
      return "/chat?with=taskiem";
  }
}

/** A key for a chat's name in the names this browser remembers. */
export const chatKey = (kind: ChatKind, id: string) => `${kind}:${id}`;

/** Where a row came from, in words: "from Chidi Okeke's message", "from
 * a photo in Market women"; without the chat's name, its kind. */
export function sourceWords(s: CollectedSource | null, names: Record<string, string> = {}): string {
  const c = sourceChat(s);
  if (!c) return "added by hand";
  const name = s?.from?.trim() || names[chatKey(c.kind, c.id)]?.trim();
  const what = c.photo ? "a photo" : "a message";
  if (c.kind === "taskiem") return `from ${what} in your chat with Taskiem`;
  if (c.kind === "group") return name ? `from ${what} in ${name}` : `from ${what} in a group`;
  if (name) return c.photo ? `from a photo ${name} sent` : `from ${name}'s message`;
  switch (c.kind) {
    case "link":
      return `from ${what} from a linked business`;
    case "customer":
      return `from ${what} from a customer`;
    default:
      return `from ${what} in a chat`;
  }
}

/** Who a row is, as its title: the saved account's name, the name the
 * message gave, or the account. */
export function rowWho(x: Pick<CollectedRow, "title" | "name" | "payee" | "last4">): string {
  return x.name.trim() || x.title.trim() || (x.payee ? `@${x.payee}` : `Account ••${x.last4}`);
}

/** The line under a row: the bank and account, and the @name if saved. */
export function rowAccount(x: Pick<CollectedRow, "bank_name" | "last4" | "payee">): string {
  const acct = [x.bank_name, x.last4 ? `••${x.last4}` : ""].filter(Boolean).join(" ");
  return x.payee ? [`@${x.payee}`, acct].filter(Boolean).join(" · ") : acct;
}

/** An amount typed in a row as kobo to send: undefined when it is not an
 * amount above ₦1 (said by amountProblem). */
export function typedKobo(text: string): number | undefined {
  const k = payAmountKobo(text);
  return k === undefined || Number.isNaN(k) || k < 100 ? undefined : k;
}

/** What is wrong with an amount typed in a row, or null. */
export function amountProblem(text: string): string | null {
  const k = payAmountKobo(text);
  if (k === undefined) return "How much?";
  if (Number.isNaN(k)) return "Type an amount, like 45,000 or 45k.";
  if (k < 100) return "Pay at least ₦1.";
  return null;
}

/** "What for" as the list keeps it: the payment's purpose. */
export const purposeKey = (typed: string): PaymentPurpose | "" => purposeOf(typed) ?? "";

/** Why the list cannot be checked yet, or null: amounts missing, or the
 * list waiting in a batch. */
export function listBlocked(list: Pick<Collection, "count" | "no_amount" | "in_batch">): string | null {
  if (list.in_batch) return "Some of your list waits to be sent: open it to send or cancel it first.";
  if (list.count === 0) return "Your list is empty: add someone from a message.";
  if (list.no_amount > 0) return list.no_amount === 1 ? "Say how much for 1 person first." : `Say how much for ${list.no_amount} people first.`;
  return null;
}

// The names of the chats rows came from, as this browser last saw them
// (the server keeps only the chat's id). A convenience: without it a row
// says the kind of chat.
const NAMES = "taskiem-pay-chats";

/** The chat names this browser remembers. */
export function chatNames(): Record<string, string> {
  try {
    const v = JSON.parse(localStorage.getItem(NAMES) ?? "{}") as unknown;
    return v && typeof v === "object" ? (v as Record<string, string>) : {};
  } catch {
    return {};
  }
}

/** Remembers a chat's name, keeping the last 200. */
export function rememberChat(kind: ChatKind, id: string, name: string | undefined) {
  if (!id || !name?.trim()) return;
  try {
    const key = chatKey(kind, id);
    const all = Object.entries(chatNames()).filter(([k]) => k !== key);
    if (chatNames()[key] === name.trim()) return;
    all.push([key, name.trim()]);
    localStorage.setItem(NAMES, JSON.stringify(Object.fromEntries(all.slice(-200))));
  } catch {
    // not remembered: rows say the kind of chat
  }
}
