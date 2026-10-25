import { describe, expect, it } from "vitest";
import { PULL_AT, pullRefreshes, pullShown } from "./pull";

describe("pull to refresh", () => {
  it("does not move for no pull or a push up", () => {
    expect(pullShown(0)).toBe(0);
    expect(pullShown(-40)).toBe(0);
  });

  it("follows the finger more slowly the further it goes, and stops short of 96", () => {
    expect(pullShown(40)).toBeLessThan(40);
    expect(pullShown(80)).toBeGreaterThan(pullShown(40));
    expect(pullShown(80) - pullShown(40)).toBeLessThan(pullShown(40));
    expect(pullShown(2000)).toBeLessThanOrEqual(96);
  });

  it("refreshes only past the mark", () => {
    expect(pullRefreshes(60)).toBe(false);
    expect(pullShown(220)).toBeGreaterThanOrEqual(PULL_AT);
    expect(pullRefreshes(220)).toBe(true);
  });
});
