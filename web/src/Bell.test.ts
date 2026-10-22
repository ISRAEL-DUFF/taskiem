import { describe, expect, it } from "vitest";
import { byDay, inAppPath, type Notification } from "./Bell";

const n = (id: string, updated_at: string): Notification => ({ id, kind: "run_failed", level: "problem", title: id, body: "", count: 1, created_at: updated_at, updated_at });

describe("inAppPath", () => {
  it("keeps app paths, takes the path of this app's own address, and leaves others", () => {
    expect(inAppPath("/runs/1", "https://app.test")).toBe("/runs/1");
    expect(inAppPath("https://app.test/runs/1", "https://app.test")).toBe("/runs/1");
    expect(inAppPath("https://elsewhere.test/x", "https://app.test")).toBe("");
    expect(inAppPath(undefined, "https://app.test")).toBe("");
  });
});

describe("byDay", () => {
  it("splits today from earlier", () => {
    const now = new Date("2026-10-22T15:00:00");
    const { today, earlier } = byDay([n("a", "2026-10-22T09:00:00"), n("b", "2026-10-21T23:00:00")], now);
    expect(today.map((x) => x.id)).toEqual(["a"]);
    expect(earlier.map((x) => x.id)).toEqual(["b"]);
  });
});
