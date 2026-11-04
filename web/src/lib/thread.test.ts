import { describe, expect, it } from "vitest";
import { atBottom, firstUnread, followNew, startsRun } from "./thread";
import type { LinkMessage } from "./linkchat";

const msg = (
  id: string,
  at: string,
  o: Partial<LinkMessage> = {},
): LinkMessage => ({
  id,
  link: "l",
  mine: false,
  kind: "text",
  body: id,
  created_at: at,
  ...o,
});

describe("atBottom", () => {
  it("is true near the end and false further up", () => {
    expect(
      atBottom({ scrollTop: 920, scrollHeight: 1500, clientHeight: 500 }),
    ).toBe(true);
    expect(
      atBottom({ scrollTop: 1000, scrollHeight: 1500, clientHeight: 500 }),
    ).toBe(true);
    expect(
      atBottom({ scrollTop: 400, scrollHeight: 1500, clientHeight: 500 }),
    ).toBe(false);
  });
  it("is true for a thread shorter than its window", () => {
    expect(
      atBottom({ scrollTop: 0, scrollHeight: 300, clientHeight: 500 }),
    ).toBe(true);
  });
});

describe("firstUnread", () => {
  it("marks the first unread message of theirs and counts them", () => {
    const list = [
      msg("a", "2026-01-01T10:00:00Z", { read_at: "2026-01-01T10:01:00Z" }),
      msg("b", "2026-01-01T10:02:00Z"),
      msg("c", "2026-01-01T10:03:00Z", { mine: true }),
      msg("d", "2026-01-01T10:04:00Z"),
    ];
    expect(firstUnread(list)).toEqual({ id: "b", count: 2 });
  });
  it("is nothing when all are read, or only ours are unread", () => {
    expect(
      firstUnread([msg("a", "2026-01-01T10:00:00Z", { read_at: "x" })]),
    ).toBeUndefined();
    expect(
      firstUnread([msg("a", "2026-01-01T10:00:00Z", { mine: true })]),
    ).toBeUndefined();
    expect(firstUnread([])).toBeUndefined();
  });
});

describe("followNew", () => {
  const old = [msg("a", "2026-01-01T10:00:00Z")];
  const withNew = [
    ...old,
    msg("b", "2026-01-01T10:05:00Z"),
    msg("c", "2026-01-01T10:06:00Z"),
  ];
  it("follows to the bottom when the reader was there", () => {
    expect(followNew("2026-01-01T10:00:00Z", withNew, true)).toEqual({
      kind: "bottom",
    });
  });
  it("leaves a reader further up alone and counts what arrived", () => {
    expect(followNew("2026-01-01T10:00:00Z", withNew, false)).toEqual({
      kind: "pill",
      count: 2,
    });
  });
  it("takes the reader down to a message they sent", () => {
    const mine = [...old, msg("b", "2026-01-01T10:05:00Z", { mine: true })];
    expect(followNew("2026-01-01T10:00:00Z", mine, false)).toEqual({
      kind: "bottom",
    });
  });
  it("takes who sent a message from isMine (the Taskiem conversation's inbound)", () => {
    type T = { id: string; created_at: string; inbound: boolean };
    const list: T[] = [
      { id: "a", created_at: "2026-01-01T10:00:00Z", inbound: false },
      { id: "b", created_at: "2026-01-01T10:05:00Z", inbound: true },
    ];
    const [a, b] = list;
    if (!a || !b) throw new Error("two");
    const mine = (m: T) => m.inbound;
    expect(followNew("2026-01-01T10:00:00Z", list, false, mine)).toEqual({
      kind: "bottom",
    });
    expect(
      followNew(
        "2026-01-01T10:00:00Z",
        [a, { ...b, inbound: false }],
        false,
        mine,
      ),
    ).toEqual({ kind: "pill", count: 1 });
  });
  it("does nothing for older messages paged in, or the first load", () => {
    const paged = [msg("z", "2025-12-31T09:00:00Z"), ...old];
    expect(followNew("2026-01-01T10:00:00Z", paged, false)).toEqual({
      kind: "none",
    });
    expect(followNew(undefined, withNew, false)).toEqual({ kind: "none" });
  });
});

describe("startsRun", () => {
  const m = (
    mine: boolean,
    sender?: string,
    kind: "text" | "card" = "text",
  ) => ({ mine, sender_name: sender, kind });
  it("starts with the first of a day and when the sender changes", () => {
    expect(startsRun(undefined, m(false, "Musa"))).toBe(true);
    expect(startsRun(m(false, "Musa"), m(false, "Musa"))).toBe(false);
    expect(startsRun(m(false, "Musa"), m(false, "Ada"))).toBe(true);
    expect(startsRun(m(false, "Musa"), m(true, "Owner"))).toBe(true);
    expect(startsRun(m(true, "Owner"), m(true, "Owner"))).toBe(false);
  });
  it("gives a document card its own run", () => {
    expect(startsRun(m(false, "Musa"), m(false, undefined, "card"))).toBe(true);
    expect(startsRun(m(false, undefined, "card"), m(false, "Musa"))).toBe(true);
  });
});
