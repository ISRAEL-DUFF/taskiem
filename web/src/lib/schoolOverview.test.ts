import { describe, expect, it } from "vitest";
import {
  clearanceLine,
  closeMatchedLine,
  closeMoneyLine,
  closeSections,
  closeStatus,
  closesPath,
  collectedLine,
  dueByNowLine,
  lastCloseLine,
  matchedLine,
  overviewPath,
  percentOf,
  type CloseItem,
} from "./schoolOverview";

describe("the overview's words", () => {
  it("builds the paths", () => {
    expect(overviewPath("")).toBe("/v1/schools/overview");
    expect(overviewPath("t1", true)).toBe("/v1/schools/overview?term=t1&reveal=true");
    expect(closesPath()).toBe("/v1/schools/closes");
    expect(closesPath("2026-10-01", "2026-10-31")).toBe("/v1/schools/closes?from=2026-10-01&to=2026-10-31");
  });

  it("says what was collected, what was due and how it was matched", () => {
    expect(percentOf(14_000_000, 30_000_000)).toBe(46);
    expect(percentOf(5, 0)).toBe(0);
    expect(percentOf(50, 10)).toBe(100);
    expect(collectedLine({ collected_kobo: 14_000_000, expected_kobo: 30_000_000, collected_percent: 46 })).toBe("₦140,000 of ₦300,000 collected (46%)");
    expect(dueByNowLine({ expected_by_now_kobo: 0, overdue_kobo: 0 })).toBe("Nothing is due yet.");
    expect(dueByNowLine({ expected_by_now_kobo: 30_000_000, overdue_kobo: 0 })).toBe("₦300,000 was due by today, all of it paid.");
    expect(dueByNowLine({ expected_by_now_kobo: 30_000_000, overdue_kobo: 16_000_000 })).toBe("₦300,000 was due by today; ₦160,000 of it is still unpaid.");
    expect(matchedLine({ auto_kobo: 0, person_kobo: 0, auto_percent: 0 })).toBe("Nothing counted yet.");
    expect(matchedLine({ auto_kobo: 10_000_000, person_kobo: 4_000_000, auto_percent: 71 })).toBe("₦100,000 matched by itself, ₦40,000 by a person.");
    expect(clearanceLine({ students: 3, cleared: 1, partly_paid: 1, not_paid: 1 })).toBe("1 cleared, 1 partly paid, 1 not paid of 3");
  });
});

describe("the close's words", () => {
  const item = (kind: CloseItem["kind"], message: string): CloseItem => ({ kind, message, path: "/x" });

  it("says whether a day reconciles", () => {
    expect(closeStatus({ reconciles: true, problems: 0 })).toEqual({ label: "Reconciles", tone: "ok" });
    expect(closeStatus({ reconciles: false, problems: 1 })).toEqual({ label: "Does not reconcile: 1 problem", tone: "warn" });
    expect(closeStatus({ reconciles: false, problems: 3 }).label).toBe("Does not reconcile: 3 problems");
    expect(lastCloseLine(undefined)).toBe("No close taken yet: the first is taken after midnight.");
    expect(lastCloseLine({ id: "c", day: "2026-10-24", ran_at: "", ran_by: "platform", reconciles: true, problems: 0 })).toBe("24 Oct 2026: reconciles.");
  });

  it("sums the day's money and matching", () => {
    expect(closeMoneyLine({ money_in: { count: 2, kobo: 15_000_000 }, into_accounts: { count: 1, kobo: 10_000_000 }, at_the_desk: { count: 1, kobo: 5_000_000 } })).toBe(
      "₦150,000 in on students (2 payments): ₦100,000 into account numbers, ₦50,000 at the desk",
    );
    expect(closeMoneyLine({ money_in: { count: 1, kobo: 100 }, into_accounts: { count: 0, kobo: 0 }, at_the_desk: { count: 1, kobo: 100 } })).toContain("(1 payment)");
    expect(closeMatchedLine({ matched_auto: { count: 1, kobo: 10_000_000 }, matched_person: { count: 1, kobo: 5_000_000 }, unmatched: { count: 0, kobo: 0 } })).toBe(
      "₦100,000 matched by itself, ₦50,000 by a person; ₦0 not matched",
    );
  });

  it("lists the problems first and leaves empty lists out", () => {
    const s = closeSections({ problem_list: [item("part_without_receipt", "no receipt")], pending: [], claims: [item("claim", "says it paid")], unmatched_list: [] });
    expect(s.map((x) => x.head)).toEqual(["Does not reconcile", "Families who say they paid"]);
    expect(s.map((x) => x.problem)).toEqual([true, false]);
    expect(closeSections({ problem_list: [], pending: [], claims: [], unmatched_list: [] })).toEqual([]);
  });
});
