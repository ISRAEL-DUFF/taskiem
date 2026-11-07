import { describe, expect, it } from "vitest";
import { FIRST_CHARS, reveal, STEP_CHARS } from "./readmore";

const words = (n: number) =>
  Array.from({ length: n }, (_, i) => `word${i}`).join(" ");

describe("reveal", () => {
  it("shows a short message whole", () => {
    expect(reveal("hello there", 0)).toEqual({
      shown: "hello there",
      folded: false,
    });
  });
  it("folds a long one at the end of a word, and each tap shows more", () => {
    const t = words(2000);
    const a = reveal(t, 0);
    expect(a.folded).toBe(true);
    expect(a.shown.length).toBeLessThanOrEqual(FIRST_CHARS);
    expect(a.shown.length).toBeGreaterThan(FIRST_CHARS - 70);
    expect(t.startsWith(a.shown)).toBe(true);
    expect(/word\d+$/.test(a.shown)).toBe(true); // a whole word
    const b = reveal(t, 1);
    expect(b.shown.length).toBeGreaterThan(a.shown.length + STEP_CHARS - 70);
    expect(b.folded).toBe(true);
  });
  it("is whole after enough taps", () => {
    const t = words(2000);
    let taps = 0;
    while (reveal(t, taps).folded && taps < 50) taps++;
    expect(taps).toBeLessThan(50);
    expect(reveal(t, taps).shown).toBe(t);
  });
  it("folds by lines too", () => {
    const t = Array.from({ length: 60 }, (_, i) => `line ${i}`).join("\n");
    const a = reveal(t, 0);
    expect(a.folded).toBe(true);
    expect(a.shown.split("\n")).toHaveLength(10);
    expect(reveal(t, 1).shown.split("\n").length).toBeGreaterThan(10);
  });
  it("does not fold what would hide only a few words", () => {
    const t = words(2000).slice(0, FIRST_CHARS + 100);
    expect(reveal(t, 0).folded).toBe(false);
  });
  it("does not cut an emoji in two", () => {
    const t = "a".repeat(FIRST_CHARS - 1) + "😀" + "b".repeat(400);
    const { shown } = reveal(t, 0);
    expect(shown).not.toMatch(/[\ud800-\udbff]$/);
  });
});
