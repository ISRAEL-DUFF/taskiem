import { describe, expect, it } from "vitest";
import { runStatusLine, runsWords } from "./pages";

// Pages H5 (build-plan-gaps §7).
describe("pages H5", () => {
  it("estimates a page's runs a day in words, against the plan (g-pages-runs)", () => {
    expect(runsWords(undefined)).toBeNull();
    expect(runsWords({ views_a_day: 30, tools: 0, runs_a_day: 0 })).toBeNull();
    expect(runsWords({ views_a_day: 20, tools: 2, runs_a_day: 40, runs_per_day: 500 })).toEqual({
      line: "About 40 runs a day from this page: 20 views a day, 2 tools each. Each person's answer is kept for a minute. Your plan allows 500 runs a day for everything.",
      over: false,
    });
    expect(runsWords({ views_a_day: 0, tools: 1, runs_a_day: 0 })?.line).toContain("no views yet in the last week, 1 tool each");
    expect(runsWords({ views_a_day: 300, tools: 2, runs_a_day: 600, runs_per_day: 500 })?.over).toBe(true);
    const month = runsWords({ views_a_day: 50, tools: 1, runs_a_day: 50, runs_per_month: 1000 });
    expect(month?.over).toBe(true);
    expect(month?.line).toContain("about 1,500 a month from this page");
  });

  it("says a row's run in a line (g-pages-rowstatus)", () => {
    expect(runStatusLine(undefined)).toBe("");
    expect(runStatusLine({ words: "Done", tone: "good" })).toBe("Done");
    expect(runStatusLine({ words: "Waiting", tone: "warn", waiting_for: [{ kind: "approval", who: "Ada" }, { kind: "form", who: "Sam", you: true }] })).toBe("Waiting: for Ada, you");
  });
});
