import { describe, expect, it } from "vitest";
import { unreadAfter } from "./taskiemSeen";
import type { TaskiemMessage } from "./taskiemchat";

const m = (id: string, inbound: boolean, at: string): TaskiemMessage => ({
  id,
  channel: "web",
  inbound,
  kind: "text",
  text: id,
  created_at: at,
});

describe("unreadAfter", () => {
  const list = [
    m("a", true, "2026-01-01T10:00:00Z"),
    m("b", false, "2026-01-01T10:00:01Z"),
    m("c", true, "2026-01-01T10:05:00Z"),
    m("d", false, "2026-01-01T10:05:01Z"),
    m("e", false, "2026-01-01T10:06:00Z"),
  ];
  it("starts at the first thing Taskiem said after what was seen, and counts them", () => {
    expect(unreadAfter(list, "2026-01-01T10:05:00Z")).toEqual({
      id: "d",
      count: 2,
    });
  });
  it("is nothing when all is seen, or when nothing was ever seen", () => {
    expect(unreadAfter(list, "2026-01-01T10:06:00Z")).toBeUndefined();
    expect(unreadAfter(list, undefined)).toBeUndefined();
  });
  it("does not count the person's own", () => {
    expect(unreadAfter(list, "2026-01-01T10:00:01Z")?.count).toBe(2);
  });
});
