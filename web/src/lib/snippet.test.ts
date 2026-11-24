import { describe, expect, it } from "vitest";
import { around, highlight, terms } from "./snippet";

describe("highlight", () => {
  it("marks the words found, whatever their case", () => {
    expect(highlight("Rice is Ready, rice!", "rice")).toEqual([
      { text: "Rice", hit: true },
      { text: " is Ready, ", hit: false },
      { text: "rice", hit: true },
      { text: "!", hit: false },
    ]);
  });
  it("marks each word of a query, and is safe with what a regex would read", () => {
    const parts = highlight("2 bags (50kg) of rice", "bags (50kg");
    expect(parts.filter((p) => p.hit).map((p) => p.text)).toEqual(["bags", "(50kg"]);
    expect(highlight("a.b", "a.b").some((p) => p.hit)).toBe(true);
    expect(highlight("axb", "a.b").some((p) => p.hit)).toBe(false);
  });
  it("is the text whole when there is nothing to find", () => {
    expect(highlight("hello", "  ")).toEqual([{ text: "hello", hit: false }]);
  });
});

describe("around", () => {
  it("keeps a short message whole, on one line", () => {
    expect(around("a\n\nb", "b")).toBe("a b");
  });
  it("cuts a long one around the first hit, with dots where it is cut", () => {
    const text = `${"word ".repeat(60)}needle ${"word ".repeat(60)}`;
    const s = around(text, "needle", 100);
    expect(s).toContain("needle");
    expect(s.length).toBeLessThanOrEqual(104);
    expect(s.startsWith("…")).toBe(true);
    expect(s.endsWith("…")).toBe(true);
  });
  it("starts at the beginning when the hit is near it", () => {
    const s = around(`needle ${"word ".repeat(80)}`, "needle", 100);
    expect(s.startsWith("needle")).toBe(true);
    expect(s.endsWith("…")).toBe(true);
  });
});

describe("terms", () => {
  it("are the query's words, longest first, once each", () => {
    expect(terms("Rice rice bags")).toEqual(["rice", "bags"]);
  });
});
