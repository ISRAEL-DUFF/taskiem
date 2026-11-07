import { describe, expect, it } from "vitest";
import { TONES, toneOf } from "./tone";

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
