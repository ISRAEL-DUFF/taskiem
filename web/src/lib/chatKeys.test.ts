import { describe, expect, it } from "vitest";
import { shortcutOf, step } from "./chatKeys";

describe("shortcutOf", () => {
  it("answers the plain keys", () => {
    expect(shortcutOf({ key: "/" }, false)).toBe("search");
    expect(shortcutOf({ key: "n" }, false)).toBe("new");
    expect(shortcutOf({ key: "j" }, false)).toBe("next");
    expect(shortcutOf({ key: "k" }, false)).toBe("previous");
    expect(shortcutOf({ key: "?" }, false)).toBe("help");
    expect(shortcutOf({ key: "@" }, false)).toBe("tag");
  });
  it("is silent while typing", () => {
    for (const k of ["/", "n", "j", "k", "?", "@"])
      expect(shortcutOf({ key: k }, true)).toBeUndefined();
  });
  it("leaves the browser's own shortcuts alone", () => {
    expect(shortcutOf({ key: "n", ctrlKey: true }, false)).toBeUndefined();
    expect(shortcutOf({ key: "k", metaKey: true }, false)).toBeUndefined();
    expect(shortcutOf({ key: "j", altKey: true }, false)).toBeUndefined();
  });
  it("is the same with Caps Lock on", () => {
    expect(shortcutOf({ key: "J" }, false)).toBe("next");
    expect(shortcutOf({ key: "N" }, false)).toBe("new");
  });
  it("ignores other keys", () => {
    expect(shortcutOf({ key: "x" }, false)).toBeUndefined();
  });
});

describe("step", () => {
  const ids = ["taskiem", "a", "b"];
  it("goes on and back, and stops at the ends", () => {
    expect(step(ids, "a", 1)).toBe("b");
    expect(step(ids, "b", 1)).toBe("b");
    expect(step(ids, "a", -1)).toBe("taskiem");
    expect(step(ids, "taskiem", -1)).toBe("taskiem");
  });
  it("starts at the top going on, and at the bottom going back, with none open", () => {
    expect(step(ids, undefined, 1)).toBe("taskiem");
    expect(step(ids, undefined, -1)).toBe("b");
    expect(step(ids, "gone", 1)).toBe("taskiem");
  });
  it("has nothing in an empty list", () => {
    expect(step([], undefined, 1)).toBeUndefined();
  });
});
