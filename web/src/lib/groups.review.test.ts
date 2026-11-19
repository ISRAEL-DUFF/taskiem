// Review fixes for groups on the web (docs/spec/people-chatting.md, "As
// built: stage 5b, groups (web)", "Review fixes").
import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import { inviteGone, invitesWithInvite, mayDeleteGroupForEveryone, mentionedIn, newGroupOpensAtOnce, shownFound, type FoundRow, type GroupInvite } from "./groups";

describe("an invite that went", () => {
  const inv = (id: string): GroupInvite => ({ group_id: id, name: id, invited_by: "p1", by: "Ada", at: "2026-11-17T09:00:00Z" }) as GroupInvite;
  it("leaves Requests on its live event (invite null)", () => {
    expect(invitesWithInvite([inv("g1"), inv("g2")], { group_id: "g1", invite: null })?.map((i) => i.group_id)).toEqual(["g2"]);
  });
  it("a 404 from Accept or Decline reads the invites again", () => {
    expect(inviteGone(new ApiError(404, "gone"))).toBe(true);
    expect(inviteGone(new ApiError(500, "no"))).toBe(false);
    expect(inviteGone(new Error("x"))).toBe(false);
  });
});

describe("@ mentions with names two members share", () => {
  const ms = [
    { person_id: "a1", name: "Ada" },
    { person_id: "a2", name: "Ada" },
    { person_id: "b", name: "Bola" },
  ];
  it("names only the member picked, not every one of that name", () => {
    expect(mentionedIn("Hi @Ada", ms, [{ id: "a2", name: "Ada" }])).toEqual(["a2"]);
    expect(mentionedIn("Hi @Ada", ms), "not picked: neither guessed").toEqual([]);
    expect(mentionedIn("Hi @Ada and @Bola", ms, [{ id: "a1", name: "Ada" }])).toEqual(["a1", "b"]);
  });
  it("whole words only", () => {
    expect(mentionedIn("Hi @Adamu", ms, [{ id: "a1", name: "Ada" }])).toEqual([]);
  });
});

describe("a new group's photo", () => {
  it("a failed photo keeps the sheet open to say so, even when nobody was added", () => {
    expect(newGroupOpensAtOnce([], false)).toBe(true);
    expect(newGroupOpensAtOnce([], true)).toBe(false);
    expect(newGroupOpensAtOnce([{ person_id: "a", state: "added" }], false)).toBe(false);
  });
});

describe("Delete for everyone in a group", () => {
  const now = Date.parse("2026-11-17T12:00:00Z");
  const m = { mine: true, created_at: "2026-11-17T11:00:00Z" };
  it("only while a member", () => {
    expect(mayDeleteGroupForEveryone({ left: false }, m, now)).toBe(true);
    expect(mayDeleteGroupForEveryone({ left: true }, m, now)).toBe(false);
  });
});

const row = (key: string, at: string): FoundRow => ({ key, path: "/", title: key, group: false, at, mine: false, line: "" });

describe("search and starred: Show more keeps time order", () => {
  it("holds back rows older than the oldest read of a list with more", () => {
    const people = { rows: [row("p10", "2026-11-17T10:00:00Z"), row("p8", "2026-11-17T08:00:00Z")], more: true };
    const groups = { rows: [row("g9", "2026-11-17T09:00:00Z"), row("g7", "2026-11-17T07:00:00Z")], more: false };
    expect(shownFound([people, groups]).map((r) => r.key)).toEqual(["p10", "g9", "p8"]);
    const paged = { rows: [...people.rows, row("p6", "2026-11-17T06:00:00Z")], more: false };
    expect(shownFound([paged, groups]).map((r) => r.key)).toEqual(["p10", "g9", "p8", "g7", "p6"]);
  });
  it("the frontier is the newest of the lists' oldest rows", () => {
    const a = { rows: [row("a9", "2026-11-17T09:00:00Z"), row("a5", "2026-11-17T05:00:00Z")], more: true };
    const b = { rows: [row("b8", "2026-11-17T08:00:00Z"), row("b7", "2026-11-17T07:00:00Z")], more: true };
    expect(shownFound([a, b]).map((r) => r.key)).toEqual(["a9", "b8", "b7"]);
  });
});
