// Review fixes for groups on the web (docs/spec/people-chatting.md, "As
// built: stage 5b, groups (web)", "Review fixes").
import { describe, expect, it } from "vitest";
import { shownFound, type FoundRow } from "./groups";

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
