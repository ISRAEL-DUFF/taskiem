import { describe, expect, it } from "vitest";
import {
  addDays,
  addMonths,
  dayKey,
  monthGrid,
  sameDay,
  sameMonth,
} from "./calendar";

describe("monthGrid", () => {
  it("is six weeks of seven, from a Sunday", () => {
    const g = monthGrid(2026, 10); // November 2026
    expect(g).toHaveLength(6);
    for (const w of g) expect(w).toHaveLength(7);
    expect(g[0]?.[0]?.getDay()).toBe(0);
    // November 1st, 2026 is a Sunday: it starts the first week.
    expect(dayKey(g[0]?.[0] ?? new Date())).toBe("2026-11-01");
    // The last week runs into December.
    expect(dayKey(g[5]?.[6] ?? new Date())).toBe("2026-12-12");
  });
  it("starts the first week in the month before when the 1st is not a Sunday", () => {
    const g = monthGrid(2026, 11); // December 2026: the 1st is a Tuesday
    expect(dayKey(g[0]?.[0] ?? new Date())).toBe("2026-11-29");
    expect(g.flat().filter((d) => d.getMonth() === 11)).toHaveLength(31);
  });
  it("has every day of a leap February once", () => {
    const g = monthGrid(2028, 1);
    expect(g.flat().filter((d) => d.getMonth() === 1)).toHaveLength(29);
  });
});

describe("days and months", () => {
  it("adds days across a month's end", () => {
    expect(dayKey(addDays(new Date(2026, 0, 31), 1))).toBe("2026-02-01");
    expect(dayKey(addDays(new Date(2026, 2, 1), -1))).toBe("2026-02-28");
  });
  it("adds months, keeping the day or the last of a shorter month", () => {
    expect(dayKey(addMonths(new Date(2026, 0, 31), 1))).toBe("2026-02-28");
    expect(dayKey(addMonths(new Date(2026, 10, 4), -1))).toBe("2026-10-04");
    expect(dayKey(addMonths(new Date(2026, 11, 15), 1))).toBe("2027-01-15");
  });
  it("tells the same day and month", () => {
    expect(sameDay(new Date(2026, 10, 4, 1), new Date(2026, 10, 4, 23))).toBe(true);
    expect(sameDay(new Date(2026, 10, 4), new Date(2026, 10, 5))).toBe(false);
    expect(sameMonth(new Date(2026, 10, 4), new Date(2026, 10, 30))).toBe(true);
  });
});
