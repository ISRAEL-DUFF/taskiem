import { describe, expect, it } from "vitest";
import { byMonth, peopleWords, scheduleIcon, scheduleSub, scheduleTone, scopeWords, type PayrollSchedule } from "./payrollreports";

const s = (over: Partial<PayrollSchedule>): PayrollSchedule => ({
  id: "x",
  run_id: "r",
  kind: "paye",
  title: "Tax (PAYE)",
  to: "Lagos State Internal Revenue Service",
  period: "2026-11",
  period_words: "November 2026",
  total_kobo: 100,
  people: 3,
  state: "to_pay",
  state_words: "To pay",
  ...over,
});

describe("tax and pension schedules", () => {
  it("groups schedules by payroll month, keeping their order", () => {
    const months = byMonth([s({ id: "a" }), s({ id: "b", kind: "pension" }), s({ id: "c", period: "2026-10", period_words: "October 2026" })]);
    expect(months.map((m) => [m.words, m.schedules.map((x) => x.id)])).toEqual([
      ["November 2026", ["a", "b"]],
      ["October 2026", ["c"]],
    ]);
    expect(byMonth([])).toEqual([]);
  });

  it("says when and for how many", () => {
    expect(scheduleSub(s({ when: "Due 10 Dec" }))).toBe("Due 10 Dec · 3 people");
    expect(scheduleSub(s({ state: "preparing", state_words: "Being prepared", people: 1 }))).toBe("Being prepared · 1 person");
    expect(peopleWords(2)).toBe("2 people");
  });

  it("tints to pay amber and paid green", () => {
    expect(scheduleTone(s({}))).toBe("warn");
    expect(scheduleTone(s({ state: "paid" }))).toBe("ok");
    expect(scheduleTone(s({ state: "preparing" }))).toBe("muted");
    expect(scheduleIcon("pension")).toBe("members");
    expect(scheduleIcon("paye")).toBe("reports");
  });

  it("asks an owner to reconnect when Payrolla doesn't allow reports", () => {
    expect(scopeWords("scope_required", true)).toEqual({ reconnect: true });
    expect(scopeWords("scope_required", false)).toEqual({ reconnect: false });
    expect(scopeWords("reconnect", true)).toBeNull();
  });
});
