import { describe, expect, it } from "vitest";
import { addRecent, insertAt, insertTone, stripTone } from "./emoji";
import {
  EMOJI_GROUPS,
  nameOf,
  searchEmoji,
  takesTone,
  withTone,
} from "./emojiList";

describe("the emoji list", () => {
  it("is the whole of Unicode 14's, every emoji named and in one place", () => {
    const seen = new Set<string>();
    for (const g of EMOJI_GROUPS) {
      expect(g.items.length, g.id).toBeGreaterThan(50);
      for (const it of g.items) {
        expect(it.char, `${g.id}: ${it.name}`).not.toHaveLength(0);
        expect(it.name, it.char).not.toBe("");
        expect(it.char).not.toContain(" ");
        expect(seen.has(it.char), `${it.char} twice`).toBe(false);
        seen.add(it.char);
      }
    }
    expect(seen.size).toBeGreaterThan(1700);
    expect(EMOJI_GROUPS.map((g) => g.id)).toEqual([
      "smileys",
      "people",
      "nature",
      "food",
      "travel",
      "activity",
      "objects",
      "symbols",
      "flags",
    ]);
  });
  it("has no emoji a phone a few years old cannot draw (newer than Unicode 14)", () => {
    const all = EMOJI_GROUPS.flatMap((g) => g.items.map((i) => i.char));
    // Added in Unicode 15 and 16: a shaking face, a pink heart, a goose, a fingerprint.
    for (const newer of ["🫨", "🩷", "🪿", "🫆", "🫩"])
      expect(all).not.toContain(newer);
    // Added in 13 and 14 are there.
    for (const older of ["🥲", "🫠", "🫶", "🫡"]) expect(all).toContain(older);
  });
  it("has the flags of countries, Nigeria's among them", () => {
    const flags = EMOJI_GROUPS.find((g) => g.id === "flags");
    expect(flags?.items.some((i) => i.char === "🇳🇬")).toBe(true);
    expect(flags?.items.length).toBeGreaterThan(250);
  });
});

describe("searchEmoji", () => {
  it("finds by name and by other words", () => {
    expect(searchEmoji("red heart").some((e) => e.char === "❤️")).toBe(true);
    expect(searchEmoji("happy").some((e) => e.char === "😀")).toBe(true);
    expect(searchEmoji("delivery truck")[0]?.char).toBe("🚚");
  });
  it("finds as people here say it", () => {
    expect(searchEmoji("okada").some((e) => e.char === "🏍️")).toBe(true);
    expect(searchEmoji("naija")[0]?.char).toBe("🇳🇬");
    expect(searchEmoji("jollof").length).toBeGreaterThan(0);
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

describe("skin tones", () => {
  it("adds a tone to a hand that takes one", () => {
    expect(withTone("👍", 3)).toBe("👍🏽");
    expect(withTone("🙏", 5)).toBe("🙏🏿");
    expect(withTone("✌️", 2)).toBe("✌🏼"); // the text-style selector gives way to the tone
  });
  it("puts the tone after the person, before the rest of a longer emoji", () => {
    expect(withTone("👩‍🍳", 3)).toBe("👩🏽‍🍳");
    expect(withTone("🧑‍⚕️", 2)).toBe("🧑🏼‍⚕️");
    expect(insertTone("🤷‍♀️", 4)).toBe("🤷🏾‍♀️");
  });
  it("leaves alone what takes none, and gives the default back as the list writes it", () => {
    expect(withTone("😀", 3)).toBe("😀");
    expect(withTone("👍", 0)).toBe("👍");
    expect(withTone("👍🏽", 0)).toBe("👍");
    expect(withTone("✌🏼", 0)).toBe("✌️");
    expect(takesTone("👍")).toBe(true);
    expect(takesTone("🔥")).toBe(false);
    expect(takesTone("👨‍👩‍👧")).toBe(false);
  });
  it("puts a tone back to the plain emoji", () => {
    expect(stripTone("👍🏾")).toBe("👍");
    expect(stripTone("🔥")).toBe("🔥");
  });
  it("names an emoji with or without a tone", () => {
    expect(nameOf("👍🏽")).toBe("thumbs up");
    expect(nameOf("✌🏼")).toBe("victory hand");
  });
  it("is every emoji that has tone variants in Unicode, and the ones that do not stay plain", () => {
    expect(
      EMOJI_GROUPS.flatMap((g) => g.items).filter((i) => takesTone(i.char))
        .length,
    ).toBeGreaterThan(250);
  });
});
