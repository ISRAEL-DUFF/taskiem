import { describe, expect, it } from "vitest";
import { TONES, tileLetters, toneOf } from "./tone";

describe("toneOf", () => {
  it("gives a name the same tone every time, whatever its case or spaces", () => {
    expect(toneOf("Jollof rice")).toBe(toneOf("  jollof RICE "));
  });
  it("spreads names over the pairs", () => {
    const names = ["Jollof rice", "Fried rice and turkey", "Pepper soup", "Egusi and pounded yam", "Zobo", "Chapman", "Bayo Rice Mills", "Rivers Foods Ltd", "Kola's Bakery", "Zuri Atelier"];
    expect(new Set(names.map(toneOf)).size).toBeGreaterThan(4);
    for (const n of names) expect(TONES).toContain(toneOf(n));
  });
});

describe("tileLetters", () => {
  it("draws the boards' letters", () => {
    expect(tileLetters("Jollof rice")).toBe("JR");
    expect(tileLetters("Party pack, 10 plates")).toBe("PP");
    expect(tileLetters("Zobo")).toBe("ZB");
    expect(tileLetters("Chapman")).toBe("CH");
  });
});
