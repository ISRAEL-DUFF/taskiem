import { describe, expect, it } from "vitest";
import { pageEmbedSnippet, siteState } from "./embedsites";

describe("embedding the page", () => {
  it("is the same code the designer's email has", () => {
    expect(pageEmbedSnippet("https://adas-kitchen.pages.test/", `Ada's "Kitchen"`)).toBe(
      `<iframe src="https://adas-kitchen.pages.test/embed" title="Ada's &quot;Kitchen&quot;" style="width:100%;min-height:720px;border:0" loading="lazy"></iframe>`,
    );
  });
  it("says where a site stands", () => {
    const base = { id: "1", origin: "https://ada.ng", method: "dns" as const, token: "t", verified_at: null, prove: {} };
    expect(siteState(base)).toMatch(/^Waiting/);
    expect(siteState({ ...base, verified_at: "2026-10-21T00:00:00Z" })).toBe("Shows your page.");
  });
});
