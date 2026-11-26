import { describe, expect, it } from "vitest";
import cases from "./linkify.cases.json";
import { findLinks, firstLink, linkParts, shownLink } from "./linkify";

// The cases shared with the app and the server: one rule everywhere.
describe("findLinks", () => {
  for (const c of cases.cases) {
    it(JSON.stringify(c.text), () => {
      const got = findLinks(c.text);
      expect(got.map((l) => ({ text: l.text, url: l.url }))).toEqual(c.links.map((l) => ({ text: l.text, url: l.url })));
      for (const [i, l] of got.entries()) {
        expect(c.text.slice(l.start, l.end)).toBe(l.text);
        const shown = (c.links[i] as { shown?: string }).shown;
        if (shown) expect(shownLink(l.text)).toBe(shown);
      }
    });
  }

  it("finds the first and splits the words", () => {
    expect(firstLink("no links")).toBe("");
    expect(firstLink("a example.com b https://x.org")).toBe("https://example.com");
    expect(linkParts("see example.com.").map((p) => [p.text, !!p.link])).toEqual([
      ["see ", false],
      ["example.com", true],
      [".", false],
    ]);
  });
});
