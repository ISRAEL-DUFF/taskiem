import { describe, expect, it } from "vitest";
import { cronOf, earlierSteps, rulesExpr, rulesOf, scheduleOf, scheduleWords, sourcesFor, DEFAULT_SCHEDULE } from "./plaincanvas";

describe("schedules", () => {
  it("says a cron in words and back", () => {
    const fri = scheduleOf("0 10 * * 5");
    expect(fri && scheduleWords(fri)).toBe("Every Friday at 10:00");
    expect(fri && cronOf(fri)).toBe("0 10 * * 5");
    expect(scheduleOf("30 9 * * 1-5")?.every).toBe("weekdays");
    expect(scheduleOf("0 8 25 * *")).toMatchObject({ every: "month", day: 25, time: "08:00" });
    expect(scheduleOf("15 * * * *")).toMatchObject({ every: "hour", minute: 15 });
    expect(scheduleOf("0 9 * * 0")?.weekday).toBe(0);
    expect(scheduleOf("0 9 * * 7")?.weekday).toBe(0);
  });
  it("leaves to Advanced what it cannot say", () => {
    for (const c of ["*/5 * * * *", "0 9 1 1 *", "0 9 * * 1,3", "0 9 30 * *", "bad"]) expect(scheduleOf(c)).toBeNull();
  });
  it("makes the cron of each choice", () => {
    expect(cronOf({ ...DEFAULT_SCHEDULE, every: "day", time: "07:30" })).toBe("30 7 * * *");
    expect(cronOf({ ...DEFAULT_SCHEDULE, every: "month", day: 1, time: "08:00" })).toBe("0 8 1 * *");
  });
});

describe("rules", () => {
  it("makes an expression and reads it back", () => {
    const r = { all: true, rules: [{ left: "=steps.approve.output.decision", op: "is" as const, right: "approved" }, { left: "=trigger.body.amount", op: "more than" as const, right: "5000" }] };
    const e = rulesExpr(r);
    expect(e).toBe("=steps.approve.output.decision == 'approved' && trigger.body.amount > 5000");
    expect(rulesOf(e)).toEqual(r);
  });
  it("quotes words safely and reads contains and or", () => {
    const e = rulesExpr({ all: false, rules: [{ left: "=trigger.body.note", op: "contains", right: "Ada's" }, { left: "=trigger.body.paid", op: "is", right: "true" }] });
    expect(e).toBe("=trigger.body.note.contains('Ada\\'s') || trigger.body.paid == true");
    expect(rulesOf(e)?.rules[0]?.right).toBe("Ada's");
    expect(rulesOf(e)?.all).toBe(false);
  });
  it("leaves to Advanced what it cannot read", () => {
    for (const e of ["=has(item.x) && item.x != null", "=a == 1 && b == 2 || c == 3", "=size(steps.x.output.rows) > 0"]) expect(rulesOf(e)).toBeNull();
    expect(rulesOf("")).toEqual({ rules: [], all: true });
  });
});

describe("values from earlier steps", () => {
  const def = {
    inputs: { schema: { properties: { supplier: { type: "string", title: "Supplier" }, amount: { type: "number" } } } },
    steps: [
      { id: "read", type: "connector", name: "Read the sheet", connector: "googlesheets@1", action: "get_values" },
      { id: "approve", type: "approval", needs: ["read"] },
      { id: "pay", type: "connector", needs: ["approve"] },
      { id: "other", type: "transform", config: { output: { x: 1 } } },
    ],
  };
  const connectors = [{ ref: "googlesheets@1", actions: { get_values: { output: { properties: { objects: { type: "array", title: "The rows" } } } } } }];
  it("lists what started the run and the steps it comes after", () => {
    expect(earlierSteps(def.steps, "pay").map((s) => s.id)).toEqual(["read", "approve"]);
    const labels = sourcesFor(def, "pay", connectors).map((s) => s.label);
    expect(labels).toEqual(["What was filled in: Supplier", "What was filled in: amount", "From “Read the sheet”: The rows", "From “approve”: the decision (approved or rejected)"]);
    expect(sourcesFor(def, "pay", connectors)[1]).toMatchObject({ expr: "=trigger.body.amount", number: true });
  });
});

// The acceptance check (no-technical-steps N8): a workflow with a
// schedule, a value from an earlier step and a rule, made with the
// pickers alone. api/owner_canvas_test.go publishes this definition.
describe("a workflow made without typing an expression", () => {
  it("is the definition the server publishes", () => {
    const steps = [
      { id: "approve", type: "approval", name: "Get it approved", config: { role: "owner", timeout: "24h", subject: { week: "this week" } } },
      { id: "note", type: "transform", name: "Note the decision", needs: ["approve"] },
    ];
    const cron = cronOf({ ...DEFAULT_SCHEDULE, every: "week", weekday: 5, time: "10:00" });
    const sources = sourcesFor({ steps }, "note", []);
    const decision = sources.find((s) => s.label === "From “Get it approved”: the decision (approved or rejected)");
    const when = rulesExpr({ all: true, rules: [{ left: decision?.expr ?? "", op: "is", right: "approved" }] });
    expect(cron).toBe("0 10 * * 5");
    expect(decision?.expr).toBe("=steps.approve.output.decision");
    expect(when).toBe("=steps.approve.output.decision == 'approved'");
  });
});
