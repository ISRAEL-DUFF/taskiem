import { describe, expect, it } from "vitest";
import { stripBadge, withBadge } from "./tabBadge";

describe("the tab's title", () => {
  it("has the count in front", () => {
    expect(withBadge("Taskiem", 3)).toBe("(3) Taskiem");
    expect(withBadge("Chat · Taskiem", 12)).toBe("(12) Chat · Taskiem");
  });
  it("has none for none, and says 99+ for many", () => {
    expect(withBadge("Taskiem", 0)).toBe("Taskiem");
    expect(withBadge("Taskiem", 250)).toBe("(99+) Taskiem");
  });
  it("does not stack counts", () => {
    expect(withBadge("(3) Taskiem", 5)).toBe("(5) Taskiem");
    expect(withBadge("(99+) Taskiem", 0)).toBe("Taskiem");
    expect(stripBadge("(3) Taskiem")).toBe("Taskiem");
    expect(stripBadge("Taskiem (beta)")).toBe("Taskiem (beta)");
  });
});
