import { describe, expect, it } from "vitest";
import {
  EMPTY,
  feedingTools,
  linkFilters,
  newPageDoc,
  newPart,
  pageName,
  runStatusLine,
  runsWords,
  titleCols,
  titleExpr,
  valueExpr,
  valueFrom,
  whenConds,
  whenExpr,
  type PageChoices,
} from "./pages";

const choices: PageChoices = {
  tools: [
    { command: "spend", name: "Spending", answers: true, outputs: [{ name: "total", title: "Spent", type: "integer" }, { name: "rows", title: "Rows", type: "list" }], inputs: [{ name: "month", title: "Month", personal: false }], list: { from: "rows", title: "=item.who", sub: "=item.amount" } },
    { command: "log", name: "Log it", answers: false, outputs: [{ name: "n", title: "N", type: "integer" }], inputs: [] },
  ],
  workflows: [{ name: "Confirm deliveries", signals: ["confirm"], approvals: [] }],
};

describe("the editor's plain words (g-pages-editor)", () => {
  it("keeps a button's value as the document does, and reads it back", () => {
    expect(valueExpr({ from: "column", column: "id" })).toBe("=row.id");
    expect(valueExpr({ from: "filter", id: "when" })).toBe("=filters.when");
    expect(valueExpr({ from: "words", words: "page" })).toBe("page");
    expect(valueExpr({ from: "words", words: "=1+1" })).toBe("='=1+1'");
    expect(valueExpr({ from: "asked" })).toBeUndefined();
    expect(valueFrom("=row.id")).toEqual({ from: "column", column: "id" });
    expect(valueFrom("=filters.when")).toEqual({ from: "filter", id: "when" });
    expect(valueFrom("='=1+1'")).toEqual({ from: "words", words: "=1+1" });
    expect(valueFrom(undefined)).toEqual({ from: "asked" });
    expect(valueFrom("=row.a + row.b")).toBeNull();
  });

  it("writes Show it only when as the fixture's condition, and reads it back", () => {
    const conds = [
      { column: "status", how: "is" as const, value: "delivered" },
      { column: "confirmed_at", how: "is empty" as const },
    ];
    expect(whenExpr(conds)).toBe("=row.status == 'delivered' && row.confirmed_at == ''");
    expect(whenConds("=row.status == 'delivered' && row.confirmed_at == ''")).toEqual(conds);
    expect(whenExpr([{ column: "who", how: "is not", value: "Bola's" }])).toBe("=row.who != 'Bola\\'s'");
    expect(whenConds(whenExpr([{ column: "who", how: "is not", value: "Bola's" }]))).toEqual([{ column: "who", how: "is not", value: "Bola's" }]);
    expect(whenConds(undefined)).toEqual([]);
    expect(whenConds("=row.n > 3")).toBeNull();
    expect(whenExpr([])).toBeUndefined();
  });

  it("titles a row by one column or two", () => {
    expect(titleExpr("order", "rider")).toBe("=row.order + ' · ' + row.rider");
    expect(titleCols("=row.order + ' · ' + row.rider")).toEqual(["order", "rider"]);
    expect(titleCols("=row.order")).toEqual(["order"]);
    expect(titleCols("=row.order + row.x")).toEqual([]);
  });

  it("starts a part a tool works out, a status and buttons from what the business has", () => {
    expect(feedingTools(choices, "list").map((t) => t.command)).toEqual(["spend"]);
    expect(feedingTools(choices, "figure").map((t) => t.command)).toEqual(["spend"]);
    expect(newPart("tool-figure", undefined, [], choices)).toMatchObject({ type: "figure", value: "output", label: "Spent", source: { tool: "spend", output: "total" } });
    expect(newPart("tool-list", undefined, [], choices)).toMatchObject({ type: "list", source: { tool: "spend", output: "rows" }, item: { title: "=item.who", sub: "=item.amount" } });
    expect(newPart("status", undefined, [], choices)).toMatchObject({ type: "status", workflow: "Confirm deliveries" });
    expect(newPart("buttons", undefined, [], choices)).toMatchObject({ type: "buttons", actions: [] });
  });

  it("names a new page and starts it empty or over Taskiem's records", () => {
    expect(pageName("Deliveries today", [])).toBe("deliveries-today");
    expect(pageName("Deliveries today", ["deliveries-today"])).toBe("deliveries-today-2");
    expect(pageName("1 x", [])).toBe("page");
    expect(pageName("", [])).toBe("page");
    expect(EMPTY).toBe("empty:");
    const empty = newPageDoc("Deliveries today", [], undefined);
    expect(empty).toMatchObject({ name: "deliveries-today", title: "Deliveries today", blocks: [{ type: "heading", title: "Deliveries today" }] });
  });
});

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

  it("reads a page link's filters (g-pages-notif)", () => {
    expect(linkFilters(null)).toBeNull();
    expect(linkFilters('{"day":"today","host":"db-2"}')).toEqual({ day: "today", host: "db-2" });
    expect(linkFilters("[1]")).toBeNull();
    expect(linkFilters('{"Bad id":"x"}')).toBeNull();
    expect(linkFilters('{"n":3}')).toBeNull();
    expect(linkFilters("{not json")).toBeNull();
    expect(linkFilters('{"a":"1","b":"2","c":"3","d":"4","e":"5"}')).toBeNull();
  });

  it("says a row's run in a line (g-pages-rowstatus)", () => {
    expect(runStatusLine(undefined)).toBe("");
    expect(runStatusLine({ words: "Done", tone: "good" })).toBe("Done");
    expect(runStatusLine({ words: "Waiting", tone: "warn", waiting_for: [{ kind: "approval", who: "Ada" }, { kind: "form", who: "Sam", you: true }] })).toBe("Waiting: for Ada, you");
  });
});
