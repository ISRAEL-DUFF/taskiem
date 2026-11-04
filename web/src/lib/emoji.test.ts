import { describe, expect, it } from "vitest";
import { addRecent, EMOJI_GROUPS, insertAt, searchEmoji } from "./emoji";

describe("the emoji list", () => {
  it("has every emoji named and in one place", () => {
    const seen = new Set<string>();
    for (const g of EMOJI_GROUPS) {
      expect(g.items.length).toBeGreaterThan(10);
      for (const it of g.items) {
        expect(it.char, `${g.id}: ${it.name}`).not.toHaveLength(0);
        expect(it.name, it.char).not.toBe("");
        expect(it.char).not.toContain(" ");
        seen.add(it.char);
      }
    }
    expect(seen.size).toBeGreaterThan(250);
  });
});

describe("searchEmoji", () => {
  it("finds by name and by other words", () => {
    expect(searchEmoji("heart").some((e) => e.char === "❤️")).toBe(true);
    expect(searchEmoji("happy").some((e) => e.char === "😀")).toBe(true);
    expect(searchEmoji("delivery truck").map((e) => e.char)).toEqual(["🚚"]);
  });
  it("needs every word, ignores case, and finds nothing for nothing", () => {
    expect(searchEmoji("HEART love").length).toBeGreaterThan(0);
    expect(searchEmoji("heart zzzz")).toEqual([]);
    expect(searchEmoji("   ")).toEqual([]);
  });
});

describe("insertAt", () => {
  it("puts it at the caret and moves the caret after it", () => {
    expect(insertAt("Hello world", 5, 5, "😀")).toEqual({
      text: "Hello😀 world",
      caret: 7,
    });
  });
  it("replaces a selection", () => {
    expect(insertAt("Hello world", 6, 11, "🌍")).toEqual({
      text: "Hello 🌍",
      caret: 8,
    });
  });
  it("works at the ends and with an empty box", () => {
    expect(insertAt("", 0, 0, "👍")).toEqual({ text: "👍", caret: 2 });
    expect(insertAt("ab", 2, 2, "!")).toEqual({ text: "ab!", caret: 3 });
    expect(insertAt("ab", 0, 0, "!")).toEqual({ text: "!ab", caret: 1 });
  });
  it("keeps within the limit", () => {
    expect(insertAt("abc", 3, 3, "d", 3)).toEqual({ text: "abc", caret: 3 });
  });
});

describe("addRecent", () => {
  it("puts the newest first without repeating, and keeps the most recent", () => {
    expect(addRecent(["a", "b", "c"], "b")).toEqual(["b", "a", "c"]);
    expect(addRecent(["a", "b"], "c", 2)).toEqual(["c", "a"]);
    expect(addRecent([], "x")).toEqual(["x"]);
  });
});

import { stripTone, takesTone, withTone } from "./emoji";

describe("skin tones", () => {
  it("adds a tone to a hand that takes one", () => {
    expect(withTone("👍", 3)).toBe("👍🏽");
    expect(withTone("🙏", 5)).toBe("🙏🏿");
    expect(withTone("✌️", 2)).toBe("✌🏼"); // the text-style selector gives way to the tone
  });
  it("leaves alone what takes none, and the default tone", () => {
    expect(withTone("😀", 3)).toBe("😀");
    expect(withTone("👍", 0)).toBe("👍");
    expect(withTone("👍🏽", 0)).toBe("👍");
    expect(takesTone("👍")).toBe(true);
    expect(takesTone("🔥")).toBe(false);
  });
  it("puts a tone back to the plain emoji", () => {
    expect(stripTone("👍🏾")).toBe("👍");
    expect(stripTone("🔥")).toBe("🔥");
  });
  it("is searchable by the plain emoji's name", () => {
    expect(searchEmoji("thumbs up").some((e) => takesTone(e.char))).toBe(true);
  });
});

describe("the wider list", () => {
  it("has flags and more people and hearts", () => {
    expect(
      EMOJI_GROUPS.some(
        (g) => g.id === "flags" && g.items.some((i) => i.char === "🇳🇬"),
      ),
    ).toBe(true);
    expect(searchEmoji("hug").length).toBeGreaterThan(0);
    expect(searchEmoji("nigeria")[0]?.char).toBe("🇳🇬");
  });
});
