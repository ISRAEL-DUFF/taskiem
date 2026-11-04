import { describe, expect, it } from "vitest";
import { dateLabel } from "./invoices";
import {
  MAX_REMIND,
  channelsLine,
  checkedLine,
  daysWords,
  debtorFlags,
  debtorsPath,
  graceProblem,
  outcomeLine,
  planLine,
  promiseProblem,
  remindedLine,
  renderPreview,
  selectUpToCap,
  selectionCounter,
  toggleSelection,
  totalLine,
  wordingProblem,
  type PlanStanding,
} from "./schoolChasing";

describe("the selection", () => {
  it("adds and removes, and refuses past the cap", () => {
    expect(toggleSelection([], "a")).toEqual(["a"]);
    expect(toggleSelection(["a"], "a")).toEqual([]);
    const full = Array.from({ length: MAX_REMIND }, (_, i) => `s${i}`);
    expect(toggleSelection(full, "more")).toBeNull();
    expect(toggleSelection(full, "s3")).toHaveLength(MAX_REMIND - 1);
    expect(selectionCounter(3)).toBe("3 of 50 selected");
  });
  it("selects the page up to the cap", () => {
    const ids = Array.from({ length: 60 }, (_, i) => `s${i}`);
    expect(selectUpToCap(["x"], ids)).toHaveLength(MAX_REMIND);
    expect(selectUpToCap(["s1"], ["s1", "s2"])).toEqual(["s1", "s2"]);
  });
});

describe("words", () => {
  it("ages and totals", () => {
    expect(daysWords({ days_past_due: 0, due_on: "2026-09-07" })).toBe(`Due ${dateLabel("2026-09-07")}`);
    expect(daysWords({ days_past_due: 1 })).toBe("1 day past due");
    expect(daysWords({ days_past_due: 10 })).toBe("10 days past due");
    expect(totalLine({ students: 3, balance_kobo: 30_000_000 })).toBe("3 students · ₦300,000");
    expect(totalLine(undefined)).toBe("None");
  });
  it("says how a send went", () => {
    expect(remindedLine({ sent: 2, skipped: 1 })).toBe("2 sent, 1 skipped.");
    expect(remindedLine({ sent: 2, skipped: 1, replayed: true })).toContain("nothing went out again");
    expect(outcomeLine({ student_id: "s", admission_number: "GCA/001", rung: 1, status: "sent", channels: { sms: 1 } })).toBe("GCA/001: sent (rung 1), by sms to 1 guardian");
    expect(outcomeLine({ student_id: "s", admission_number: "GCA/003", status: "skipped", reason: "a promise to pay by 25 Oct 2026" })).toBe("GCA/003: skipped: a promise to pay by 25 Oct 2026");
    expect(channelsLine({ whatsapp: 2 }, false)).toBe("On WhatsApp to 2 guardians");
    expect(channelsLine({}, true)).toContain("No guardian could be reached");
    expect(checkedLine({ promises_kept: 1, promises_missed: 1, arrears: 2 })).toBe("Checked: 1 promise missed, 1 kept, 2 instalments newly in arrears.");
  });
  it("flags promises and plans", () => {
    const plan: PlanStanding = {
      plan_id: "p",
      student_id: "s",
      state: "arrears",
      approved_at: "2026-10-01T09:00:00Z",
      instalments: [],
      grace_days: 0,
      paid_since_kobo: 0,
      due_so_far_kobo: 5_000_000,
      missed: [{ instalment: 1, due_on: "2026-11-01", amount_kobo: 5_000_000, short_kobo: 5_000_000, days_past: 3 }],
    };
    expect(debtorFlags({ promise_missed: true, plan }).map((f) => f.label)).toEqual(["Promise missed", "Plan in arrears"]);
    expect(debtorFlags({ promise: { status: "open", promised_on: "2026-10-25" } as never }).map((f) => f.label)).toEqual([`Promised by ${dateLabel("2026-10-25")}`]);
    expect(planLine(plan)).toBe(`Instalment 1 of ₦50,000 due ${dateLabel("2026-11-01")}, ₦50,000 short (3 days)`);
    expect(planLine({ ...plan, state: "good", missed: [], next_due_on: "2026-11-15", next_amount_kobo: 2_000_000 })).toBe(`Next instalment ₦20,000 by ${dateLabel("2026-11-15")}`);
    expect(planLine({ ...plan, state: "settled", missed: [], paid_since_kobo: 10_000_000 })).toBe("₦100,000 paid since the plan was approved");
  });
  it("builds the Debtors path", () => {
    expect(debtorsPath({})).toBe("/v1/schools/debtors");
    expect(debtorsPath({ class: "c1", bucket: "0_7", has: "promise" }, "csv")).toBe("/v1/schools/debtors?class=c1&bucket=0_7&has=promise&format=csv");
  });
});

describe("checks", () => {
  it("checks a promise", () => {
    const today = new Date("2026-10-24T10:00:00Z");
    expect(promiseProblem("", "", today)).toContain("Choose the date");
    expect(promiseProblem("2026-10-23", "", today)).toBe("The date is today or later.");
    expect(promiseProblem("2026-10-24", "", today)).toBeNull();
    expect(promiseProblem("2027-11-01", "", today)).toBe("The date is within a year.");
    expect(promiseProblem("2026-10-25", "x".repeat(301), today)).toContain("300");
  });
  it("checks and previews the wording", () => {
    expect(wordingProblem("")).toBeNull();
    expect(wordingProblem("Pay {balance} by {tomorrow}")).toContain("{tomorrow}");
    expect(wordingProblem("x".repeat(601))).toContain("600");
    expect(wordingProblem("Pay {balance} for {child}")).toBeNull();
    expect(renderPreview("Pay {balance} for {child}.")).toBe("Pay ₦40,000 for Ebube. Your fees page: https://pay.example/s/abc");
    expect(renderPreview("  ")).toBe("");
  });
  it("checks the grace", () => {
    expect(graceProblem("0")).toBeNull();
    expect(graceProblem("60")).toBeNull();
    expect(graceProblem("61")).not.toBeNull();
    expect(graceProblem("-1")).not.toBeNull();
    expect(graceProblem("x")).not.toBeNull();
  });
});
