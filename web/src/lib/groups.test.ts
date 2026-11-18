import { describe, expect, it } from "vitest";
import {
  addedWords,
  GROUP_KEPT_WORDS,
  GROUP_REPORT_CONSENT,
  groupAsRow,
  groupFoundPath,
  groupHeadWords,
  groupLinkCode,
  groupPreview,
  groupSearchURL,
  groupStarredURL,
  groupThreadWithLive,
  groupThreadWithReceipts,
  invitesWithInvite,
  joinWords,
  membersInOrder,
  mentionable,
  mentionedIn,
  mentionPieces,
  mergeFound,
  noPostWords,
  fromTaskiem,
  readByWords,
  rowsWithGroup,
  typingByGroup,
  type FoundRow,
  type Group,
  type GroupMember,
  type GroupMessage,
} from "./groups";
import { rowPath, type ChatListItem } from "./people";

const group = (over: Partial<Group> = {}): Group => ({
  id: "g1",
  kind: "group",
  name: "Yaba friends",
  description: "",
  photo: null,
  members: 3,
  role: "member",
  left: false,
  admins_only_post: false,
  admins_only_edit: true,
  approve_joins: false,
  can_post: true,
  can_edit: false,
  muted: false,
  archived: false,
  mentions: true,
  unread: 0,
  mentioned: false,
  last: "Hello",
  last_mine: false,
  last_sender: "Ada",
  last_at: "2026-11-18T10:00:00Z",
  joined_at: "2026-11-01T10:00:00Z",
  ...over,
});

const msg = (over: Partial<GroupMessage> = {}): GroupMessage => ({
  id: "m1",
  group_id: "g1",
  mine: false,
  sender_id: "p1",
  sender: "Ada",
  kind: "text",
  body: "Hi",
  created_at: "2026-11-18T10:00:00Z",
  ...over,
});

const member = (over: Partial<GroupMember>): GroupMember => ({ person_id: "p", name: "X", role: "member", joined_at: "2026-11-01T10:00:00Z", you: false, ...over });

describe("the words", () => {
  it("never says end-to-end encrypted", () => {
    expect(GROUP_KEPT_WORDS).toMatch(/not end-to-end encrypted/);
    expect(GROUP_KEPT_WORDS.replace(/not end-to-end encrypted/, "")).not.toMatch(/end-to-end|encrypted/i);
    expect(GROUP_REPORT_CONSENT).toMatch(/last 20 messages/);
  });
  it("says each add as the server answered it, and nothing more", () => {
    expect(addedWords("added")).toBe("Added");
    expect(addedWords("invited")).toBe("Invited: they'll see it in their Requests");
    expect(addedWords("member")).toBe("Already in the group");
    expect(addedWords("full")).toBe("The group is full");
    expect(addedWords("unavailable")).toBe("Not on Taskiem");
  });
  it("says a row's last line with who wrote it", () => {
    expect(groupPreview(group())).toBe("Ada: Hello");
    expect(groupPreview(group({ last_mine: true, last_sender: "You" }))).toBe("You: Hello");
    expect(groupPreview(group({ last: "Ada added Bola", last_sender: undefined }))).toBe("Ada added Bola");
    expect(groupPreview(group({ last: "", left: true, left_how: "removed" }))).toBe("You were removed");
    expect(groupPreview(group({ last: "" }))).toBe("No messages yet");
  });
  it("says the head's line: typing, left, closed, or the count", () => {
    expect(groupHeadWords(group())).toBe("3 members");
    expect(groupHeadWords(group({ members: 1 }))).toBe("1 member");
    expect(groupHeadWords(group(), ["Ada"])).toBe("Ada is typing…");
    expect(groupHeadWords(group(), ["Ada", "Bola"])).toBe("Ada and Bola are typing…");
    expect(groupHeadWords(group(), ["A", "B", "C"])).toBe("3 people are typing…");
    expect(groupHeadWords(group({ left: true, left_how: "left" }))).toBe("You left this group");
    expect(groupHeadWords(group({ closed: { at: "", until: null, words: "Closed.", can_appeal: false } }))).toBe("Closed by Taskiem's moderators");
  });
  it("says why there is no composer", () => {
    expect(noPostWords(group())).toBeNull();
    expect(noPostWords(group({ can_post: false, closed: { at: "", until: null, words: "Taskiem's moderators closed this group.", can_appeal: false } }))).toBe("Taskiem's moderators closed this group.");
    expect(noPostWords(group({ can_post: false, left: true, left_how: "removed" }))).toMatch(/You were removed/);
    expect(noPostWords(group({ can_post: false, admins_only_post: true }))).toBe("Only admins can send messages in this group.");
  });
  it("says read by, joins and Taskiem's own events", () => {
    expect(readByWords({ read_by: [{ person_id: "a", name: "A", at: "x" }], delivered_to: [{ person_id: "b", name: "B", at: null }], waiting: 1 })).toBe("Read by 1, delivered to 1, 1 still to get it");
    expect(readByWords({ read_by: [], delivered_to: [], waiting: 0 })).toBe("No one has it yet");
    expect(joinWords("waiting", "Yaba friends")).toMatch(/waits for an admin/);
    expect(joinWords("joined", "Yaba friends")).toBe("You joined Yaba friends.");
    expect(fromTaskiem(msg({ kind: "event", sender_id: null, sender: "Taskiem" }))).toBe(true);
    expect(fromTaskiem(msg({ kind: "event", sender: "Ada" }))).toBe(false);
  });
});

describe("members and mentions", () => {
  const ms = [member({ person_id: "b", name: "Bola" }), member({ person_id: "me", name: "You", you: true }), member({ person_id: "a", name: "Ada Obi", role: "admin" }), member({ person_id: "c", name: "Ada" }), member({ person_id: "d", name: "Deleted account" })];
  it("puts you first, then admins, then by name", () => {
    expect(membersInOrder(ms).map((m) => m.person_id)).toEqual(["me", "a", "c", "b", "d"]);
  });
  it("offers members, never yourself or a deleted account", () => {
    expect(mentionable(ms).map((m) => m.person_id)).toEqual(["b", "a", "c"]);
  });
  it("finds who a message names, the longest name first", () => {
    expect(mentionedIn("@Ada Obi and @bola, see this", ms)).toEqual(["a", "b"]);
    expect(mentionedIn("@Ada come", ms)).toEqual(["c"]);
    expect(mentionedIn("mail@Ada.com @Adamu", ms)).toEqual([]);
    expect(mentionedIn("@Ada @Ada", ms)).toEqual(["c"]);
  });
  it("marks the mentions in a message's words", () => {
    expect(mentionPieces("Hi @Ada Obi, come", ["Ada Obi", "Ada"])).toEqual([
      { text: "Hi ", mention: false },
      { text: "@Ada Obi", mention: true },
      { text: ", come", mention: false },
    ]);
    expect(mentionPieces("no one", ["Ada"])).toEqual([{ text: "no one", mention: false }]);
  });
});

describe("links and addresses", () => {
  it("reads a group's code from a link or bare", () => {
    expect(groupLinkCode("https://taskiem.com/g/AbC123xyz9")).toBe("abc123xyz9");
    expect(groupLinkCode("abc123xyz9")).toBe("abc123xyz9");
    expect(groupLinkCode("https://taskiem.com/p/abc123xyz9")).toBe("");
    expect(groupLinkCode("no!")).toBe("");
  });
  it("opens a group, its found messages and its search", () => {
    expect(rowPath({ kind: "group", id: "g1" })).toBe("/you/groups/g1");
    expect(groupFoundPath({ group_id: "g1", message: msg({ id: "m9" }) })).toBe("/you/groups/g1?at=m9");
    expect(groupSearchURL(" zobo ", "g1", "n1")).toBe("/v1/me/groups/search?q=zobo&group=g1&before=n1");
    expect(groupStarredURL()).toBe("/v1/me/groups/starred");
    expect(groupStarredURL("x")).toBe("/v1/me/groups/starred?before=x");
  });
});

describe("live", () => {
  it("puts a group's row in its place, newest first, and takes it out", () => {
    const person: ChatListItem = { kind: "person", id: "c1", name: "Ada", last: "", last_mine: false, last_at: "2026-11-18T09:00:00Z", unread: 0, muted: false, archived: false, blocked: false };
    const rows = rowsWithGroup([person], { id: "g1", hidden: false, group: group() }, false);
    expect(rows?.map((r) => `${r.kind}:${r.id}`)).toEqual(["group:g1", "person:c1"]);
    expect(rowsWithGroup(rows, { id: "g1", hidden: true }, false)?.map((r) => r.id)).toEqual(["c1"]);
    expect(rowsWithGroup(rows, { id: "g1", hidden: false, group: group({ archived: true }) }, false)?.map((r) => r.id)).toEqual(["c1"]);
    expect(groupAsRow(group()).group?.name).toBe("Yaba friends");
  });
  it("adds a message, shows our own once, and leaves an old one out", () => {
    const cur = [msg({ id: "m1" }), msg({ id: "local-1", mine: true, local: true, body: "Yo", created_at: "2026-11-18T10:01:00Z" })];
    const mine = msg({ id: "m2", mine: true, body: "Yo", created_at: "2026-11-18T10:01:01Z" });
    expect(groupThreadWithLive(cur, "g1", { group_id: "g1", message: mine })?.map((m) => m.id)).toEqual(["m1", "m2"]);
    expect(groupThreadWithLive(cur, "g2", { group_id: "g1", message: mine })).toBe(cur);
    const old = msg({ id: "m0", created_at: "2026-11-01T10:00:00Z" });
    expect(groupThreadWithLive(cur, "g1", { group_id: "g1", message: old })).toBe(cur);
  });
  it("moves our ticks", () => {
    const cur = [msg({ id: "m1", mine: true, status: "sent" }), msg({ id: "m2", status: undefined })];
    const next = groupThreadWithReceipts(cur, "g1", { group_id: "g1", messages: [{ id: "m1", status: "read" }, { id: "m2", status: "read" }] });
    expect(next?.map((m) => m.status)).toEqual(["read", undefined]);
    expect(groupThreadWithReceipts(cur, "g1", { group_id: "g1", messages: [] })).toBe(cur);
  });
  it("keeps invites newest first and typing per group", () => {
    const inv = (id: string, at: string) => ({ group_id: id, name: id, photo: null, invited_by: "a", by: "Ada", at });
    expect(invitesWithInvite([inv("g1", "2026-11-18T09:00:00Z")], { group_id: "g2", invite: inv("g2", "2026-11-18T10:00:00Z") })?.map((i) => i.group_id)).toEqual(["g2", "g1"]);
    const t = typingByGroup({ groups: [{ group_id: "g1", people: ["a"] }, { group_id: "g2", people: [] }] });
    expect([...t.keys()]).toEqual(["g1"]);
  });
});

describe("search and starred with people chats", () => {
  it("merges by time, each once", () => {
    const row = (key: string, at: string): FoundRow => ({ key, path: "/", title: key, group: false, at, mine: false, line: "" });
    expect(mergeFound([row("a", "2026-11-18T09:00:00Z"), row("b", "2026-11-18T07:00:00Z")], [row("c", "2026-11-18T08:00:00Z"), row("a", "2026-11-18T09:00:00Z")]).map((r) => r.key)).toEqual(["a", "c", "b"]);
  });
});
