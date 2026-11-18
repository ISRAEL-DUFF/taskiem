import { describe, expect, it } from "vitest";
import type { ChatListItem, PeopleChat, PeopleMessage } from "./people";
import {
  appendFound,
  chatWithPresence,
  checkPlace,
  foundPath,
  foundWords,
  mapURL,
  placeFigures,
  placeProblem,
  requestsWithChat,
  requestsWithGone,
  rowsWithChat,
  searchable,
  searchURL,
  snippetParts,
  starredURL,
  threadWithGone,
  chatsPoll,
  hasBusinessRow,
  readySays,
  threadWithLive,
  TypingGate,
  withPresence,
  type PeopleFound,
} from "./peoplelive";

const msg = (id: string, at: string, more: Partial<PeopleMessage> = {}): PeopleMessage => ({ id, mine: false, kind: "text", body: id, read_at: null, created_at: at, ...more });

const chat = (id: string, at: string, more: Partial<PeopleChat> = {}): PeopleChat => ({
  id,
  kind: "person",
  person_id: `p-${id}`,
  name: `Name ${id}`,
  gone: false,
  last: "hi",
  last_mine: false,
  last_at: at,
  unread: 0,
  muted: false,
  archived: false,
  request: false,
  waiting: false,
  blocked: false,
  ...more,
});

const row = (kind: "person" | "business", id: string, at: string): ChatListItem => ({
  kind,
  id,
  name: id,
  last: "",
  last_mine: false,
  last_at: at,
  unread: 0,
  muted: false,
  archived: false,
  blocked: false,
});

describe("the live connection's events", () => {
  it("adds a message to its open chat and changes one it has", () => {
    const cur = [msg("a", "2026-11-18T10:00:00Z"), msg("b", "2026-11-18T10:01:00Z")];
    const added = threadWithLive(cur, "c1", { chat_id: "c1", request: false, message: msg("c", "2026-11-18T10:02:00Z") });
    expect(added?.map((m) => m.id)).toEqual(["a", "b", "c"]);
    const read = threadWithLive(added, "c1", { chat_id: "c1", request: false, message: msg("b", "2026-11-18T10:01:00Z", { their_reaction: "👍" }) });
    expect(read?.find((m) => m.id === "b")?.their_reaction).toBe("👍");
    expect(read).toHaveLength(3);
  });

  it("leaves another chat's message, and a thread not loaded yet, alone", () => {
    const cur = [msg("a", "2026-11-18T10:00:00Z")];
    expect(threadWithLive(cur, "c1", { chat_id: "c2", request: false, message: msg("x", "2026-11-18T10:02:00Z") })).toBe(cur);
    expect(threadWithLive(undefined, "c1", { chat_id: "c1", request: false, message: msg("x", "2026-11-18T10:02:00Z") })).toBeUndefined();
  });

  it("shows our own message once when the connection tells of it before the send's answer", () => {
    const local = msg("local-1", "2026-11-18T10:00:05Z", { mine: true, body: "On my way", local: true });
    const cur = [msg("a", "2026-11-18T10:00:00Z"), local];
    const out = threadWithLive(cur, "c1", { chat_id: "c1", request: false, message: msg("srv", "2026-11-18T10:00:04Z", { mine: true, body: "On my way" }) });
    expect(out?.map((m) => m.id)).toEqual(["a", "srv"]);
    // Other words: the copy on its way stays.
    const other = threadWithLive(cur, "c1", { chat_id: "c1", request: false, message: msg("srv2", "2026-11-18T10:00:04Z", { mine: true, body: "Something else" }) });
    expect(other?.map((m) => m.id)).toEqual(["a", "srv2", "local-1"]);
  });

  it("changes an old message only when it is read, and never drops it into the thread to page from", () => {
    const cur = [msg("a", "2026-11-18T10:00:00Z"), msg("b", "2026-11-18T10:01:00Z")];
    // A reaction on a message from months ago, not read: left out.
    const old = msg("old", "2026-08-01T10:00:00Z", { their_reaction: "❤️" });
    expect(threadWithLive(cur, "c1", { chat_id: "c1", request: false, message: old })).toBe(cur);
    // The oldest read is changed in place.
    const a = threadWithLive(cur, "c1", { chat_id: "c1", request: false, message: msg("a", "2026-11-18T10:00:00Z", { starred: true }) });
    expect(a?.map((m) => m.id)).toEqual(["a", "b"]);
    expect(a?.[0]?.starred).toBe(true);
    // Between what was read: added.
    expect(threadWithLive(cur, "c1", { chat_id: "c1", request: false, message: msg("ab", "2026-11-18T10:00:30Z") })?.map((m) => m.id)).toEqual(["a", "ab", "b"]);
    // Only sends on their way read: nothing older to page from, so it is added.
    const local = [msg("local-1", "2026-11-18T10:05:00Z", { mine: true, local: true })];
    expect(threadWithLive(local, "c1", { chat_id: "c1", request: false, message: msg("x", "2026-11-18T09:00:00Z") })?.map((m) => m.id)).toEqual(["x", "local-1"]);
    // An empty chat takes it.
    expect(threadWithLive([], "c1", { chat_id: "c1", request: false, message: msg("x", "2026-11-18T09:00:00Z") })?.map((m) => m.id)).toEqual(["x"]);
  });

  it("reads the Chats list every 8 seconds while it has a business row, live or not", () => {
    expect(hasBusinessRow([row("person", "c1", "2026-11-18T10:00:00Z"), row("business", "shop", "2026-11-18T10:05:00Z")])).toBe(true);
    expect(hasBusinessRow([row("person", "c1", "2026-11-18T10:00:00Z")])).toBe(false);
    expect(hasBusinessRow(undefined)).toBe(false);
    expect(chatsPoll(true, true, 8000)).toBe(8000);
    expect(chatsPoll(false, true, 8000)).toBe(8000);
    // People chats only: the live connection tells; a slow look as a fallback.
    expect(chatsPoll(true, false, 8000)).toBe(60_000);
    expect(chatsPoll(false, false, 8000)).toBe(8000);
  });

  it("says in front on a reconnection that reused ?away=1, and away on one opened in front", () => {
    // Opened hidden (?away=1), the tab now seen: the browser's reconnection
    // reused the address, so in front is said.
    expect(readySays(true, true)).toBe(true);
    // Opened in front, the tab hidden since: away is said.
    expect(readySays(false, false)).toBe(false);
    // The address already says it.
    expect(readySays(true, false)).toBeNull();
    expect(readySays(false, true)).toBeNull();
  });

  it("takes out a message deleted for the person elsewhere", () => {
    const cur = [msg("a", "2026-11-18T10:00:00Z"), msg("b", "2026-11-18T10:01:00Z")];
    expect(threadWithGone(cur, "c1", { chat_id: "c1", id: "a" })?.map((m) => m.id)).toEqual(["b"]);
    expect(threadWithGone(cur, "c1", { chat_id: "c2", id: "a" })).toBe(cur);
    expect(threadWithGone(cur, "c1", { chat_id: "c1", id: null })).toBe(cur);
  });

  it("puts a chat in its place in the one list, newest first, and leaves businesses be", () => {
    const rows = [row("business", "shop", "2026-11-18T10:05:00Z"), row("person", "c1", "2026-11-18T10:00:00Z")];
    const out = rowsWithChat(rows, { id: "c1", hidden: false, chat: chat("c1", "2026-11-18T10:09:00Z", { last: "new words", unread: 2 }) }, false);
    expect(out?.map((r) => r.id)).toEqual(["c1", "shop"]);
    expect(out?.[0]).toMatchObject({ kind: "person", last: "new words", unread: 2 });
    // A new chat joins; a hidden one goes.
    const two = rowsWithChat(out, { id: "c2", hidden: false, chat: chat("c2", "2026-11-18T10:07:00Z") }, false);
    expect(two?.map((r) => r.id)).toEqual(["c1", "c2", "shop"]);
    expect(rowsWithChat(two, { id: "c1", hidden: true }, false)?.map((r) => r.id)).toEqual(["c2", "shop"]);
  });

  it("keeps requests and archived chats out of the list they are not in", () => {
    const rows = [row("person", "c1", "2026-11-18T10:00:00Z")];
    expect(rowsWithChat(rows, { id: "c9", hidden: false, chat: chat("c9", "2026-11-18T10:09:00Z", { request: true }) }, false)).toBe(rows);
    expect(rowsWithChat(rows, { id: "c1", hidden: false, chat: chat("c1", "2026-11-18T10:09:00Z", { archived: true }) }, false)).toEqual([]);
    expect(rowsWithChat([], { id: "c1", hidden: false, chat: chat("c1", "2026-11-18T10:09:00Z", { archived: true }) }, true)?.map((r) => r.id)).toEqual(["c1"]);
  });

  it("keeps Requests to requests, and a deleted request goes", () => {
    const cur = [chat("r1", "2026-11-18T10:00:00Z", { request: true })];
    const added = requestsWithChat(cur, { id: "r2", hidden: false, chat: chat("r2", "2026-11-18T10:03:00Z", { request: true }) });
    expect(added?.map((r) => r.id)).toEqual(["r2", "r1"]);
    // Accepted: no longer a request.
    expect(requestsWithChat(added, { id: "r2", hidden: false, chat: chat("r2", "2026-11-18T10:03:00Z") })?.map((r) => r.id)).toEqual(["r1"]);
    expect(requestsWithGone(added, { chat_id: "r1", id: null })?.map((r) => r.id)).toEqual(["r2"]);
    expect(requestsWithGone(added, { chat_id: "r1", id: "m1" })).toBe(added);
  });

  it("follows presence into a chat's person", () => {
    const c = chat("c1", "2026-11-18T10:00:00Z", {
      person: { id: "p-c1", name: "Bola", deleted: false, contact_name: null, in_contacts: false, they_added_you: false, photo: null, last_seen_at: null, online: false, read_ticks: true },
    });
    const p = withPresence(new Map(), { people: [{ person_id: "p-c1", chat_id: "c1", online: true, last_seen_at: null }] });
    expect(chatWithPresence(c, p)?.person?.online).toBe(true);
    const away = withPresence(p, { people: [{ person_id: "p-c1", chat_id: "c1", online: false, last_seen_at: "2026-11-18T10:05:00Z" }] });
    expect(chatWithPresence(c, away)?.person).toMatchObject({ online: false, last_seen_at: "2026-11-18T10:05:00Z" });
    expect(withPresence(away, { people: [] })).toBe(away);
    expect(chatWithPresence(c, new Map())).toBe(c);
  });
});

describe("typing", () => {
  it("says typing at once, then at most every 3 seconds, and stopped once when the box empties", () => {
    const g = new TypingGate();
    expect(g.change("H", 0)).toBe(true);
    expect(g.change("He", 1000)).toBeNull();
    expect(g.change("Hel", 2999)).toBeNull();
    expect(g.change("Hell", 3000)).toBe(true);
    expect(g.change("", 3500)).toBe(false);
    expect(g.change("", 3600)).toBeNull();
    expect(g.change("   ", 3700)).toBeNull();
    expect(g.change("A", 3800)).toBe(true);
  });

  it("starts again at once after a send, and says stopped on leaving only when it said typing", () => {
    const g = new TypingGate();
    expect(g.change("Hi", 0)).toBe(true);
    g.sent();
    expect(g.change("N", 100)).toBe(true);
    expect(g.leave()).toBe(true);
    expect(g.leave()).toBe(false);
  });
});

describe("search and the starred list", () => {
  it("searches only words the server takes", () => {
    expect(searchable("a")).toBe(false);
    expect(searchable("  ")).toBe(false);
    expect(searchable("a b")).toBe(false);
    expect(searchable("a jollof")).toBe(true);
    expect(searchable("Ọ̀ṣun")).toBe(true);
    expect(searchable("x".repeat(201))).toBe(false);
  });

  it("asks a page at a time, in one chat or all", () => {
    expect(searchURL(" jollof rice ")).toBe("/v1/me/people/search?q=jollof+rice");
    expect(searchURL("jollof", "c1", "abc")).toBe("/v1/me/people/search?q=jollof&chat=c1&before=abc");
    expect(starredURL()).toBe("/v1/me/people/starred");
    expect(starredURL("a/b")).toBe("/v1/me/people/starred?before=a%2Fb");
  });

  it("adds the next page without a message twice", () => {
    const f = (id: string): PeopleFound => ({ chat_id: "c1", chat: chat("c1", "2026-11-18T10:00:00Z"), message: msg(id, "2026-11-18T10:00:00Z") });
    expect(appendFound([f("a"), f("b")], [f("b"), f("c")]).map((x) => x.message.id)).toEqual(["a", "b", "c"]);
  });

  it("opens the chat at the message, a request in Requests", () => {
    const m = msg("m 1", "2026-11-18T10:00:00Z");
    expect(foundPath({ chat_id: "c1", chat: chat("c1", ""), message: m })).toBe("/you/people/c1?at=m%201");
    expect(foundPath({ chat_id: "c1", chat: chat("c1", "", { request: true }), message: m })).toBe("/you/people/requests/c1?at=m%201");
  });

  it("says how many were found", () => {
    expect(foundWords(0, false)).toBe("No messages found.");
    expect(foundWords(1, false)).toBe("1 message found");
    expect(foundWords(30, true)).toBe("30 messages found so far");
  });

  it("marks the words searched for in a snippet, accents and case aside", () => {
    expect(snippetParts("…we ate Jollof at Ọ̀ṣun market", "jol osun")).toEqual([
      { text: "…we ate ", hit: false },
      { text: "Jollof", hit: true },
      { text: " at ", hit: false },
      { text: "Ọ̀ṣun", hit: true },
      { text: " market", hit: false },
    ]);
    expect(snippetParts("nothing here", "a")).toEqual([{ text: "nothing here", hit: false }]);
  });
});

describe("sending a place", () => {
  it("takes a place on the earth with a short label", () => {
    expect(checkPlace(6.4523, 3.3958, "  Mama Put, Yaba ")).toEqual({ ok: true, place: { lat: 6.4523, lng: 3.3958, label: "Mama Put, Yaba" } });
    expect(checkPlace(6.4523, 3.3958, "")).toEqual({ ok: true, place: { lat: 6.4523, lng: 3.3958 } });
    expect(checkPlace(91, 3, "").ok).toBe(false);
    expect(checkPlace(6, -181, "").ok).toBe(false);
    expect(checkPlace(Number.NaN, 3, "").ok).toBe(false);
    const long = checkPlace(6, 3, "x".repeat(201));
    expect(long.ok).toBe(false);
    if (!long.ok) expect(long.words).toBe("A place's name can be up to 200 letters.");
  });

  it("says plainly why where you are could not be found", () => {
    expect(placeProblem(1)).toMatch(/blocked it/);
    expect(placeProblem(2)).toMatch(/could not be found/);
    expect(placeProblem(3)).toMatch(/too long/);
    expect(placeProblem(0)).toMatch(/can't share where you are/);
    for (const c of [0, 1, 2, 3]) expect(placeProblem(c)).not.toMatch(/error|code|permission|geolocation/i);
  });

  it("links a map and shows the figures", () => {
    expect(mapURL({ lat: 6.4523001234, lng: 3.3958 })).toBe("https://www.openstreetmap.org/?mlat=6.4523&mlon=3.3958#map=17/6.4523/3.3958");
    expect(placeFigures({ lat: 6.4523, lng: 3.3958 })).toBe("6.45230, 3.39580");
  });
});
