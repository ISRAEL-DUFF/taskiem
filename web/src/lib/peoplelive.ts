// What stage 5a's gaps add to people chats on the web (docs/spec/
// people-chatting.md, "As built: stage 5a gaps, chat features (server)"):
// You's live connection (GET /v1/me/live), typing, presence, search across
// the person's own people chats, the starred list, and sending a place.
// Pure helpers, tested on their own; the pages use them.

import { mergePeople, type ChatListItem, type PeopleChat, type PeopleMessage } from "./people";

// --- the live connection's events ---

/** `message`: a message of one of the person's chats is new or changed. */
export interface LiveMessage {
  chat_id: string;
  /** It waits in Requests. */
  request: boolean;
  message: PeopleMessage;
}

/** `chat`: a chat is new or changed, or (hidden) gone from the person's lists. */
export type LiveChat = { id: string; hidden: false; chat: PeopleChat } | { id: string; hidden: true };

/** `gone`: a message deleted for the person elsewhere (id), or a request they deleted (id null). */
export interface LiveGone {
  chat_id: string;
  id: string | null;
}

/** `presence`: people whose online or last seen changed (both null when hidden). */
export interface LivePresence {
  people: { person_id: string; chat_id: string; online: boolean | null; last_seen_at: string | null }[];
}

/** What the live connection tells the pages. */
export type PeopleLiveEvent =
  | { type: "message"; data: LiveMessage }
  | { type: "chat"; data: LiveChat }
  | { type: "gone"; data: LiveGone }
  /** The connection opened (again): what was missed while it was down is read once. */
  | { type: "ready" };

/** An open chat's messages after a `message` event for it. Our own message
 * told by the connection before its send was answered takes the place of
 * the copy on its way (same words, still local), so it shows once.
 *
 * A message already in the thread is changed in place; one not in it is
 * added only when it is not older than the oldest message read. An older
 * one (a reaction, star or delete on a message from months ago) is left
 * out: put in, it would leave a gap that the thread's older pages are then
 * read from, skipping everything between it and what was read. */
export function threadWithLive(cur: PeopleMessage[] | undefined, chat: string, e: LiveMessage): PeopleMessage[] | undefined {
  if (!cur || e.chat_id !== chat) return cur;
  const m = e.message;
  if (!cur.some((x) => x.id === m.id)) {
    const oldest = cur.find((x) => !x.local);
    if (oldest && Date.parse(m.created_at) < Date.parse(oldest.created_at)) return cur;
  }
  let kept = cur;
  if (m.mine && !cur.some((x) => x.id === m.id)) {
    const twin = cur.find((x) => x.local && !x.attachment && x.body === m.body && m.kind === "text");
    if (twin) kept = cur.filter((x) => x !== twin);
  }
  return mergePeople(kept, [m]);
}

/** An open chat's messages after a `gone` event: the message deleted for
 * the person on another device goes. */
export function threadWithGone(cur: PeopleMessage[] | undefined, chat: string, e: LiveGone): PeopleMessage[] | undefined {
  if (!cur || e.chat_id !== chat || !e.id) return cur;
  return cur.filter((m) => m.id !== e.id);
}

/** A people chat as a row of the one Chats list. */
export function chatAsRow(c: PeopleChat): ChatListItem {
  return {
    kind: "person",
    id: c.id,
    name: c.name,
    person_id: c.person_id,
    gone: c.gone,
    suspended: c.suspended,
    last: c.last,
    last_mine: c.last_mine,
    last_at: c.last_at,
    unread: c.unread,
    muted: c.muted,
    archived: c.archived,
    blocked: c.blocked,
    waiting: c.waiting,
    person: c.person,
  };
}

const newestFirst = (a: { last_at: string }, b: { last_at: string }) => Date.parse(b.last_at) - Date.parse(a.last_at);

/** The one Chats list (or its archived part) after a `chat` event: the
 * chat in its place, newest first; gone when hidden, a request (those are
 * in Requests) or not of this part. Business rows are left as they are. */
export function rowsWithChat(rows: ChatListItem[] | undefined, e: LiveChat, archived: boolean): ChatListItem[] | undefined {
  if (!rows) return rows;
  const others = rows.filter((r) => !(r.kind === "person" && r.id === e.id));
  if (e.hidden || e.chat.request || e.chat.archived !== archived) return others.length === rows.length ? rows : others;
  return [...others, chatAsRow(e.chat)].sort(newestFirst);
}

/** Requests after a `chat` event: a request in its place, newest first;
 * gone once accepted or hidden. */
export function requestsWithChat(rows: PeopleChat[] | undefined, e: LiveChat): PeopleChat[] | undefined {
  if (!rows) return rows;
  const others = rows.filter((r) => r.id !== e.id);
  if (e.hidden || !e.chat.request) return others.length === rows.length ? rows : others;
  return [...others, e.chat].sort(newestFirst);
}

/** Requests after a `gone` event: a request deleted on another device goes. */
export function requestsWithGone(rows: PeopleChat[] | undefined, e: LiveGone): PeopleChat[] | undefined {
  if (!rows || e.id !== null) return rows;
  return rows.filter((r) => r.id !== e.chat_id);
}

/** How often the pages poll: slowly while the live connection is open. */
export const livePoll = (connected: boolean, fallbackMs: number) => (connected ? Math.max(fallbackMs, 60_000) : fallbackMs);

/** The one Chats list has a chat with a business. */
export const hasBusinessRow = (rows: readonly Pick<ChatListItem, "kind">[] | undefined) => !!rows?.some((r) => r.kind === "business");

/** How often the one Chats list is read again: the live connection tells
 * of people chats only, so while the list has a business row it is read as
 * often as with no connection. */
export function chatsPoll(connected: boolean, business: boolean, fallbackMs: number): number {
  return business ? fallbackMs : livePoll(connected, fallbackMs);
}

/** Who is typing, for a screen reader's polite live region (the chat's
 * head, the Chats list's rows): empty when no one is. */
export function typingWords(names: readonly string[]): string {
  if (names.length === 0) return "";
  if (names.length === 1) return `${names[0]} is typing`;
  if (names.length === 2) return `${names[0]} and ${names[1]} are typing`;
  return `${names.length} people are typing`;
}

/** What a connection's `ready` says of the tab (POST /v1/me/presence
 * {online}): the server starts a connection away when its address has
 * `?away=1`, which the browser's own reconnection reuses however the tab is
 * now. In front or away when that differs from the address; null when the
 * address already says it. */
export function readySays(openedAway: boolean, front: boolean): boolean | null {
  return openedAway === !front ? null : front;
}

/** Who is online and when last seen, by person, after a `presence` event. */
export type PresenceMap = ReadonlyMap<string, { online: boolean | null; last_seen_at: string | null }>;
export function withPresence(cur: PresenceMap, e: LivePresence): PresenceMap {
  if (!e.people?.length) return cur;
  const next = new Map(cur);
  for (const p of e.people) next.set(p.person_id, { online: p.online, last_seen_at: p.last_seen_at });
  return next;
}

/** No one's presence told. */
export const NO_PRESENCE: PresenceMap = new Map();

/** The presence the pages are given: only while the connection is open
 * (one closed tells nothing, and a chat's head reads its own again). */
export const shownPresence = (connected: boolean, presence: PresenceMap): PresenceMap => (connected ? presence : NO_PRESENCE);

/** A chat with its person as the latest presence says. */
export function chatWithPresence<C extends Pick<PeopleChat, "person_id" | "person">>(c: C | undefined, presence: PresenceMap): C | undefined {
  const p = c && presence.get(c.person_id);
  if (!c || !p || !c.person) return c;
  return { ...c, person: { ...c.person, online: p.online, last_seen_at: p.last_seen_at } };
}

// --- typing ---

/** How often "typing" is said again while the words change (the server
 * passes it on at most every 3 seconds per chat). */
export const TYPING_EVERY_MS = 3000;

/**
 * When to tell the server we are typing: the first change, then at most
 * every 3 seconds while the words change; "stopped" once when the box
 * empties. A send ends it on the server, so `sent` forgets it here.
 */
export class TypingGate {
  private at = -Infinity;
  private on = false;
  /** The words changed: true (say typing), false (say stopped) or null (nothing). */
  change(text: string, now: number): boolean | null {
    if (!text.trim()) {
      if (!this.on) return null;
      this.on = false;
      this.at = -Infinity;
      return false;
    }
    if (this.on && now - this.at < TYPING_EVERY_MS) return null;
    this.on = true;
    this.at = now;
    return true;
  }
  /** A message went: the server ended the typing. */
  sent() {
    this.on = false;
    this.at = -Infinity;
  }
  /** Leaving the chat with words in the box: say stopped, if it was said. */
  leave(): boolean {
    const was = this.on;
    this.sent();
    return was;
  }
}

// --- search and the starred list ---

/** One message found (a search) or starred, with its chat. */
export interface PeopleFound {
  chat_id: string;
  chat: PeopleChat;
  message: PeopleMessage;
  snippet?: string;
  starred_at?: string;
}

export interface FoundPage {
  results?: PeopleFound[];
  starred?: PeopleFound[];
  more: boolean;
  next?: string;
}

/** The words of a search the server takes: some word of two letters or
 * more, at most 200 characters in all. */
export function searchable(q: string): boolean {
  const t = q.trim();
  if (!t || [...t].length > 200) return false;
  return t.split(/[^\p{L}\p{M}\p{N}]+/u).some((w) => [...fold(w)].length >= 2);
}

/** GET /v1/me/people/search's address: the words, one chat, the page after `before`. */
export function searchURL(q: string, chat?: string, before?: string): string {
  const p = new URLSearchParams({ q: q.trim() });
  if (chat) p.set("chat", chat);
  if (before) p.set("before", before);
  return `/v1/me/people/search?${p.toString()}`;
}

/** The starred list's address, for the page after `before`. */
export function starredURL(before?: string): string {
  return before ? `/v1/me/people/starred?before=${encodeURIComponent(before)}` : "/v1/me/people/starred";
}

/** Found messages after another page came: those not already shown, in order. */
export function appendFound(cur: PeopleFound[], page: PeopleFound[]): PeopleFound[] {
  const seen = new Set(cur.map((f) => f.message.id));
  return [...cur, ...page.filter((f) => !seen.has(f.message.id))];
}

/** Where a found or starred message opens: its chat, at the message. */
export function foundPath(f: Pick<PeopleFound, "chat_id" | "chat" | "message">): string {
  const chat = encodeURIComponent(f.chat_id);
  return `${f.chat?.request ? `/you/people/requests/${chat}` : `/you/people/${chat}`}?at=${encodeURIComponent(f.message.id)}`;
}

/** "1 message found", "30 messages found", or more to come. */
export function foundWords(n: number, more: boolean): string {
  if (n === 0) return "No messages found.";
  const words = n === 1 ? "1 message" : `${n} messages`;
  return more ? `${words} found so far` : `${words} found`;
}

/** The pieces of a snippet, the words searched for marked: each word of
 * the search marks a word of the snippet it begins (accents and case aside). */
export function snippetParts(snippet: string, q: string): { text: string; hit: boolean }[] {
  const words = q
    .trim()
    .split(/[^\p{L}\p{M}\p{N}]+/u)
    .filter((w) => [...w].length >= 2)
    .map(fold);
  if (!words.length) return [{ text: snippet, hit: false }];
  const out: { text: string; hit: boolean }[] = [];
  for (const piece of snippet.split(/([\p{L}\p{M}\p{N}]+)/u)) {
    if (!piece) continue;
    const hit = /[\p{L}\p{N}]/u.test(piece) && words.some((w) => fold(piece).startsWith(w));
    const last = out[out.length - 1];
    if (last && last.hit === hit) last.text += piece;
    else out.push({ text: piece, hit });
  }
  return out;
}

function fold(s: string): string {
  return s.normalize("NFKD").replace(/\p{M}/gu, "").toLowerCase();
}

/** How many older pages an open chat reads to find the message it was
 * opened at, at most. */
export const FIND_PAGES = 20;

/** A message opened at and not found: no longer in the chat (every page
 * read), or further back than the pages read so far. */
export type AtFind = "gone" | "far";
export function atFindState(found: boolean, more: boolean): AtFind | undefined {
  if (found) return undefined;
  return more ? "far" : "gone";
}
export const AT_FIND_WORDS: Record<AtFind, string> = {
  gone: "That message is no longer in this chat.",
  far: "That message is further back in this chat than has been read so far.",
};

// --- sending a place ---

export interface Place {
  lat: number;
  lng: number;
  label?: string;
}

/** The longest label a place may have (the server's). */
export const PLACE_LABEL_MAX = 200;

/** A place the server takes: on the earth, with a label trimmed and short
 * enough; else what is wrong, in plain words. */
export function checkPlace(lat: number, lng: number, label: string): { ok: true; place: Place } | { ok: false; words: string } {
  if (!Number.isFinite(lat) || !Number.isFinite(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180)
    return { ok: false, words: "That place could not be read. Try again." };
  const l = label.trim();
  if ([...l].length > PLACE_LABEL_MAX) return { ok: false, words: `A place's name can be up to ${PLACE_LABEL_MAX} letters.` };
  return { ok: true, place: l ? { lat, lng, label: l } : { lat, lng } };
}

/** What finding where the person is ran into, in plain words
 * (GeolocationPositionError's codes; 0 when the browser has none). */
export function placeProblem(code: number): string {
  switch (code) {
    case 1:
      return "Taskiem may not see where you are: this browser blocked it. Allow location for Taskiem in your browser's settings, then try again.";
    case 2:
      return "Where you are could not be found just now. Check that location is on for this device, then try again.";
    case 3:
      return "Finding where you are took too long. Try again, near a window or outside if you can.";
    default:
      return "This browser can't share where you are. Send it from the Taskiem app on your phone.";
  }
}

/** A map of the place, on OpenStreetMap. */
export function mapURL(p: Pick<Place, "lat" | "lng">): string {
  const lat = round(p.lat);
  const lng = round(p.lng);
  return `https://www.openstreetmap.org/?mlat=${lat}&mlon=${lng}#map=17/${lat}/${lng}`;
}

/** The place in figures, as people read them: "6.45230, 3.39580". */
export function placeFigures(p: Pick<Place, "lat" | "lng">): string {
  return `${p.lat.toFixed(5)}, ${p.lng.toFixed(5)}`;
}

const round = (x: number) => Math.round(x * 1e6) / 1e6;
