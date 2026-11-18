// People chatting with people (docs/spec/people-chatting.md, decision 0069),
// stage 5a on the web: what the API answers for You's one Chats list, a
// chat with a person, requests, finding people, contacts, the privacy
// settings, blocks, reports and share cards, and the words the screens
// build from it. Pure helpers, tested on their own.
//
// People chats are kept by Taskiem and readable by its servers (§6). No
// word here may say or suggest that they are end-to-end encrypted.

import { ApiError } from "../api";
import { clock, dayLabel } from "./linkchat";
import type { DeliveryProgress } from "./personchat";

export type Audience = "everyone" | "contacts" | "nobody";

export interface PersonPhoto {
  url: string;
  updated_at: string;
}

/** A person as the viewer may see them, by that person's settings. */
export interface PersonCard {
  id: string;
  name: string;
  deleted: boolean;
  contact_name: string | null;
  in_contacts: boolean;
  they_added_you: boolean;
  photo: PersonPhoto | null;
  last_seen_at: string | null;
  online: boolean | null;
  read_ticks: boolean;
}

/** GET /v1/people/{id}. */
export interface PersonView extends PersonCard {
  number: string | null;
}

/** One of the person's contacts. */
export interface PersonContact extends PersonCard {
  source: "number" | "link" | "contacts" | "chat";
  added_at: string;
}

/** POST /v1/people/lookup, GET /v1/people/links/{code}. */
export interface PersonFound {
  found: boolean;
  self?: boolean;
  person?: PersonCard;
  add_token?: string;
  invite?: { words: string; link: string };
}

/** GET/POST /v1/me/link. */
export interface PersonLink {
  code: string;
  url: string;
  created_at: string;
  words: string;
  qr: string;
}

/** GET /v1/person-links/{code}, for anyone. */
export interface PersonLinkCard {
  code: string;
  link: string;
  name: string;
  photo: PersonPhoto | null;
  app?: string;
}

/** GET/PUT /v1/me/privacy/people. */
export interface PeoplePrivacy {
  find_by_number: Audience;
  find_from_contacts: boolean;
  message: Audience;
  last_seen: Audience;
  read_ticks: boolean;
  photo: Audience;
  show_number: boolean;
}

/** One row of You's one Chats list (GET /v1/me/conversations). */
export interface ChatListItem {
  kind: "business" | "person";
  id: string;
  name: string;
  business_id?: string;
  person_id?: string;
  gone?: boolean;
  last: string;
  last_mine: boolean;
  last_at: string;
  unread: number;
  muted: boolean;
  archived: boolean;
  blocked: boolean;
  waiting?: boolean;
  person?: PersonCard;
}

export interface ChatList {
  chats: ChatListItem[];
  unread: number;
  requests: number;
}

/** A chat with a person, the person's own copy. */
export interface PeopleChat {
  id: string;
  kind: "person";
  person_id: string;
  name: string;
  gone: boolean;
  last: string;
  last_mine: boolean;
  last_at: string;
  unread: number;
  muted: boolean;
  archived: boolean;
  request: boolean;
  waiting: boolean;
  blocked: boolean;
  contact?: boolean;
  phone?: string | null;
  kept?: string;
  person?: PersonCard;
}

export interface PayoutAccount {
  bank: string;
  account_number: string;
  account_name: string;
}

/** A card in a people chat (§10.1, §11 A). */
export interface PeopleCard {
  type: "product" | "business" | "order" | "delivery" | "split";
  title: string;
  business_id?: string;
  business?: string;
  area?: string;
  item_id?: string;
  price_kobo?: number;
  reference?: string;
  state?: string;
  delivery?: DeliveryProgress;
  amount_kobo?: number;
  what?: string;
  pay_to?: PayoutAccount;
  paid?: boolean;
  paid_at?: string;
  paid_by_me?: boolean;
}

/** What a card is made from, as sent (POST …/messages {card}). */
export interface CardAsk {
  type: PeopleCard["type"];
  business_id?: string;
  item_id?: string;
  reference?: string;
  amount_kobo?: number;
  what?: string;
  pay_to?: PayoutAccount;
}

export interface PeopleAttachment {
  kind: "photo" | "voice" | "file";
  type: string;
  size: number;
  name?: string;
}

/** One message of the person's copy. */
export interface PeopleMessage {
  id: string;
  mine: boolean;
  kind: "text" | "media" | "card" | "location";
  body: string;
  card?: PeopleCard;
  location?: { lat: number; lng: number; label?: string };
  attachment?: PeopleAttachment;
  reply_to?: string;
  forwarded?: boolean;
  forwarded_many?: boolean;
  status?: "sent" | "delivered" | "read";
  starred?: boolean;
  my_reaction?: string;
  their_reaction?: string;
  deleted?: boolean;
  warn_transfer?: boolean;
  warn?: boolean;
  read_at: string | null;
  created_at: string;
  /** Ours, still on its way (not from the server). */
  local?: boolean;
  /** A photo on its way: this device's own copy, to show meanwhile. */
  localPreview?: string;
}

/** GET …/messages/{message}/card. */
export interface OpenedCard {
  card: PeopleCard;
  live: {
    yours: boolean;
    product?: { id: string; name: string; price_kobo?: number; available: boolean; description?: string; few_left?: number };
    business?: { name: string; tagline?: string; area?: string; open_now?: boolean; takes_chats: boolean; shop?: string };
    order?: { business_id: string; business: string; reference: string; state: string; summary: string; total_kobo: number };
    delivery?: DeliveryProgress;
  };
}

/** One person blocked (GET /v1/me/people/blocked). */
export interface BlockedPerson {
  person_id: string;
  name: string;
  chat_id: string;
  at: string;
}

// --- words ---

/** How people chats are kept, for a chat's info (§6.2, §6.3). Plain, and
 * never a word that suggests only the two people can read them. */
export const KEPT_WORDS =
  "Chats with people are kept by Taskiem, stored securely on its servers. No one at Taskiem reads them, except a moderator when a chat is reported to us, or when the law requires it.";

/** What the report sheet asks consent for (§8.3). */
export const REPORT_CONSENT = "Send the last 20 messages of this chat to Taskiem's moderators, so they can see what happened.";

/** Under a warned message, on the side that received it (§8.4). */
export const SCAM_WARNING = "Be careful: this message looks like a scam. Never send a code, a PIN or money to someone you don't know.";

/** Contacts on the web: matching works only from a phone (§5.3). */
export const CONTACTS_ON_PHONE = "Finding friends from your phone's contacts works in the Taskiem app on your phone.";

/** The name the viewer shows for someone: the name they gave them, else theirs. */
export function shownName(p: { name: string; contact_name?: string | null; deleted?: boolean }): string {
  if (p.deleted) return "Deleted account";
  return p.contact_name?.trim() || p.name;
}

/** "online", "last seen today at 14:05", "last seen yesterday at 09:12",
 * "last seen on 3 March 2026"; empty when their settings hide it. */
export function lastSeenWords(p: Pick<PersonCard, "online" | "last_seen_at"> | undefined, now: Date = new Date()): string {
  if (!p) return "";
  if (p.online) return "online";
  if (!p.last_seen_at) return "";
  const day = dayLabel(p.last_seen_at, now);
  const at = clock(p.last_seen_at);
  if (day === "Today") return `last seen today at ${at}`;
  if (day === "Yesterday") return `last seen yesterday at ${at}`;
  if (/^[A-Z][a-z]+day$/.test(day)) return `last seen on ${day} at ${at}`;
  return `last seen on ${day}`;
}

/** A phone number as people read it: +234 803 123 4567. */
export function phoneWords(e164: string | null | undefined): string {
  if (!e164) return "";
  const m = /^\+234(\d{3})(\d{3})(\d{4})$/.exec(e164);
  return m ? `+234 ${m[1]} ${m[2]} ${m[3]}` : e164;
}

export type TickState = "sending" | "sent" | "delivered" | "read";

/** Our message's ticks: one while not delivered, two once it is, coloured
 * only when the answer says it was read (read ticks off on either side,
 * or a request not accepted yet, never say so). */
export function tickState(m: Pick<PeopleMessage, "local" | "status">): TickState {
  if (m.local) return "sending";
  if (m.status === "read") return "read";
  if (m.status === "delivered") return "delivered";
  return "sent";
}

export const TICK_WORDS: Record<TickState, string> = { sending: "Sending", sent: "Sent", delivered: "Delivered", read: "Read" };

/** How many chats a message may be forwarded to at once (§8.2). */
export const FORWARD_MAX = 5;
export const forwardCap = (m: Pick<PeopleMessage, "forwarded_many">) => (m.forwarded_many ? 1 : FORWARD_MAX);

/** The mark on a forwarded message. */
export function forwardWords(m: Pick<PeopleMessage, "forwarded" | "forwarded_many">): string {
  if (m.forwarded_many) return "Forwarded many times";
  return m.forwarded ? "Forwarded" : "";
}

/** Delete for everyone: our own, within 24 hours (§7). */
export const EVERYONE_MS = 24 * 60 * 60 * 1000;
export function mayDeleteForEveryone(m: Pick<PeopleMessage, "mine" | "deleted" | "local" | "created_at">, now: number = Date.now()): boolean {
  return m.mine && !m.deleted && !m.local && now - Date.parse(m.created_at) < EVERYONE_MS;
}

/** Where a row of the one Chats list opens. */
export function rowPath(c: Pick<ChatListItem, "kind" | "id">): string {
  return c.kind === "person" ? `/you/people/${encodeURIComponent(c.id)}` : `/you/chats/${encodeURIComponent(c.id)}`;
}

/** A row's last line. */
export function rowPreview(c: Pick<ChatListItem, "last" | "last_mine" | "blocked" | "waiting" | "gone">): string {
  if (c.blocked) return "You blocked them";
  if (!c.last) return c.waiting ? "Waiting for them to accept" : "No messages yet";
  return `${c.last_mine ? "You: " : ""}${c.last}`;
}

/** "1 message request", "3 message requests". */
export const requestsWords = (n: number) => (n === 1 ? "1 message request" : `${n} message requests`);

/** A row's photo: the person's when their setting shows it. */
export const photoOf = (p: PersonCard | undefined): string | undefined => p?.photo?.url || undefined;

/** The address of our own copy of a message's file. */
export const peopleFileURL = (chat: string, message: string) => `/v1/me/people/${encodeURIComponent(chat)}/files/${encodeURIComponent(message)}`;

/** What a message holds, in a line (a reply's quote, the menu's name). */
export function messageLine(m: Pick<PeopleMessage, "body" | "card" | "attachment" | "deleted" | "location"> | undefined): string {
  if (!m) return "a message";
  if (m.deleted) return "This message was deleted";
  if (m.card) return m.card.type === "split" ? splitTitle(m.card) : m.card.title;
  if (m.location) return m.location.label || "A place";
  if (m.attachment) {
    const kind = m.attachment.kind === "photo" ? "Photo" : m.attachment.kind === "voice" ? "Voice note" : m.attachment.name || "File";
    return m.body && m.body !== kind ? `${kind}: ${m.body}` : kind;
  }
  return m.body || "a message";
}

/** "Your share: ₦3,000 for the cake". */
export function splitTitle(c: Pick<PeopleCard, "amount_kobo" | "what">, naira: (kobo: number) => string = nairaPlain): string {
  const amount = naira(c.amount_kobo ?? 0);
  return c.what ? `Your share: ${amount} for ${c.what}` : `Your share: ${amount}`;
}

function nairaPlain(kobo: number): string {
  const whole = Math.floor(kobo / 100);
  const frac = kobo % 100;
  return `₦${whole.toLocaleString("en-NG")}${frac ? `.${String(frac).padStart(2, "0")}` : ""}`;
}

/** A card's line above its title. */
export function cardKind(c: Pick<PeopleCard, "type">): string {
  return { product: "Product", business: "Business", order: "Order", delivery: "Delivery", split: "Your share" }[c.type];
}

/** The code in a person's link, typed or pasted as a link or bare: "", when it is not one. */
export function linkCode(text: string): string {
  const t = text.trim();
  const m = /(?:^|\/)p\/([A-Za-z0-9]{4,40})\/?(?:[?#].*)?$/.exec(t);
  const code = (m?.[1] ?? t).toLowerCase();
  return /^[a-z0-9]{4,40}$/.test(code) ? code : "";
}

/** The ten digits of a bank account, spaces and dashes taken out; "" if not ten. */
export function accountDigits(text: string): string {
  const d = text.replace(/[\s-]/g, "");
  return /^\d{10}$/.test(d) ? d : "";
}

/** The choices of each privacy setting, in the words the page shows. */
export const AUDIENCE_WORDS: Record<Audience, string> = { everyone: "Everyone", contacts: "My contacts", nobody: "Nobody" };

export interface PrivacyRow {
  key: "find_by_number" | "message" | "last_seen" | "photo";
  label: string;
  hint: string;
  choices: { value: Audience; words: string }[];
}

/** The audience settings, in the order of §5.4. */
export const PRIVACY_ROWS: PrivacyRow[] = [
  {
    key: "find_by_number",
    label: "Who can find me by my number",
    hint: "People who type your number in New chat.",
    choices: [
      { value: "everyone", words: "Everyone" },
      { value: "contacts", words: "My contacts" },
      { value: "nobody", words: "Nobody" },
    ],
  },
  {
    key: "message",
    label: "Who can message me",
    hint: "Someone new writes as a request: you choose to accept it, delete it, or block them.",
    choices: [
      { value: "everyone", words: "Everyone, as a request" },
      { value: "contacts", words: "My contacts" },
      { value: "nobody", words: "Nobody" },
    ],
  },
  {
    key: "last_seen",
    label: "Who sees when I was last here",
    hint: "If you hide yours, you don't see other people's either.",
    choices: [
      { value: "everyone", words: "Everyone" },
      { value: "contacts", words: "My contacts" },
      { value: "nobody", words: "Nobody" },
    ],
  },
  {
    key: "photo",
    label: "Who sees my photo",
    hint: "Your photo in chats with people.",
    choices: [
      { value: "everyone", words: "Everyone" },
      { value: "contacts", words: "My contacts" },
      { value: "nobody", words: "Nobody" },
    ],
  },
];

/** The two switches, in the words the page shows. */
export const PRIVACY_SWITCHES: { key: "read_ticks" | "show_number" | "find_from_contacts"; label: string; hint: string }[] = [
  { key: "read_ticks", label: "Read ticks", hint: "Off: people don't see when you read their messages, and you don't see when they read yours." },
  { key: "show_number", label: "Show my number", hint: "To people you chat with or added. Off: they see your name only." },
  { key: "find_from_contacts", label: "Find me from contacts", hint: "People who have your number in their phone's contacts can find you in the Taskiem app." },
];

/** What a refused send says, in the sender's chat: the server's plain words
 * (a held message, someone not taking messages, too many waiting). */
export function sendProblem(e: unknown): string | null {
  if (!(e instanceof ApiError)) return null;
  if (e.status === 403 || e.status === 409 || e.status === 429) return e.message.replace(/^./, (x) => x.toUpperCase());
  return null;
}

/** What starting a chat ran into, in plain words. */
export function startWords(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.status === 410) return "This link no longer works. Ask them for their new link.";
    if (e.status === 404) return "This link does not open a chat with anyone. Check it, or ask them for it.";
    if (e.status === 403 || e.status === 429) return e.message.replace(/^./, (x) => x.toUpperCase());
    return e.message.replace(/^./, (x) => x.toUpperCase());
  }
  return "The chat could not be opened. Check your connection and try again.";
}

/** The messages after a page came: by id, oldest first. */
export function mergePeople(cur: PeopleMessage[], more: PeopleMessage[]): PeopleMessage[] {
  const byID = new Map(cur.map((m) => [m.id, m]));
  for (const m of more) byID.set(m.id, m);
  return [...byID.values()].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at) || (a.id < b.id ? -1 : 1));
}

/** The thread after its latest page was read again: the page in place of
 * what it covers, older messages kept when there are more, and sends on
 * their way. */
export function withLatestPeople(cur: PeopleMessage[], page: PeopleMessage[], more: boolean): PeopleMessage[] {
  const from = page[0] ? Date.parse(page[0].created_at) : Infinity;
  const kept = cur.filter((m) => m.local || (more && Date.parse(m.created_at) < from));
  return mergePeople(kept, page);
}

/** A chat's line in a share picker: the chats a card or a forward may go to
 * (requests waiting, blocked people and deleted accounts take nothing). */
export function canSendTo(c: Pick<PeopleChat, "gone" | "blocked" | "request">): boolean {
  return !c.gone && !c.blocked && !c.request;
}
