import { describe, expect, it } from "vitest";
import { layout, stillWorking, workedWords, type DrawnPart } from "./pages";

// Pages G2: when a tool worked a part out, a part still being worked out,
// and the page's buttons and a status kept apart from a row of figures.
describe("pages with tools", () => {
  it("says when a tool worked a part out", () => {
    const now = Date.parse("2026-11-23T10:00:00Z");
    expect(workedWords(undefined, now)).toBe("");
    expect(workedWords("2026-11-23T09:59:50Z", now)).toBe("Worked out just now");
    expect(workedWords("2026-11-23T09:59:00Z", now)).toBe("Worked out a minute ago");
    expect(workedWords("2026-11-23T09:48:00Z", now)).toBe("Worked out 12 minutes ago");
    expect(workedWords("2026-11-23T08:50:00Z", now)).toBe("Worked out an hour ago");
    expect(workedWords("2026-11-23T06:00:00Z", now)).toBe("Worked out 4 hours ago");
  });
  it("reads a page again while a tool is still working", () => {
    const parts: DrawnPart[] = [{ id: "a", type: "figure" }, { id: "b", type: "list", working: "Still working…" }];
    expect(stillWorking({ blocks: parts })).toBe(true);
    expect(stillWorking({ blocks: [parts[0]] })).toBe(false);
  });
  it("keeps buttons and a status on their own rows", () => {
    const parts: DrawnPart[] = [
      { id: "a", type: "figure" },
      { id: "b", type: "figure" },
      { id: "new", type: "buttons", actions: [{ index: 0, label: "New ticket", kind: "tool", tool: "new-ticket" }] },
      { id: "s", type: "status", words: "Waiting" },
    ];
    const laid = layout(parts);
    expect(laid).toHaveLength(3);
    expect(Array.isArray(laid[0])).toBe(true);
  });
});
