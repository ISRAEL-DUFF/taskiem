import { describe, expect, it } from "vitest";
import { bucketLong, bucketShort, dayLine, hourWords, lagosHour, linePath, openBucket, type MoneyBucket } from "./moneyChart";

// 25 October 2026, Lagos midnight (UTC+1).
const day = Date.UTC(2026, 9, 24, 23);
const hours = (inKobo: Record<number, number>): MoneyBucket[] =>
  Array.from({ length: 24 }, (_, h) => ({ from: new Date(day + h * 3600_000).toISOString(), in_settled_kobo: inKobo[h] ?? 0, out_settled_kobo: 0, payments: inKobo[h] ? 1 : 0 }));

describe("the day's line", () => {
  it("adds up hour by hour from 6 am to the hour now", () => {
    const now = new Date(day + 14.5 * 3600_000); // 2:30 pm
    expect(lagosHour(now)).toBe(14);
    const l = dayLine(hours({ 8: 100, 9: 200, 13: 50 }), now);
    expect(l?.start).toBe(6);
    expect(l?.end).toBe(14);
    expect(l?.points).toEqual([0, 0, 100, 300, 300, 300, 300, 350, 350]);
  });

  it("starts earlier when money came in before 6 am", () => {
    const l = dayLine(hours({ 3: 500 }), new Date(day + 9 * 3600_000));
    expect(l?.start).toBe(3);
    expect(l?.points[0]).toBe(500);
  });

  it("has two points just after midnight, and none without buckets", () => {
    expect(dayLine(hours({}), new Date(day + 600_000))?.points).toEqual([0, 0]);
    expect(dayLine([], new Date())).toBeNull();
  });

  it("draws inside its box", () => {
    const { line, area } = linePath([0, 50, 100], 300, 56);
    expect(line).toBe("0,54 150,29 300,4");
    expect(area).toBe("M0 56 L0 54 L150 29 L300 4 L300 56 Z");
  });
});

describe("bars", () => {
  const week = Array.from({ length: 7 }, (_, i) => ({ from: new Date(day - (6 - i) * 86400_000).toISOString() }));
  const mon = { from: new Date(day - 6 * 86400_000).toISOString() };
  const sun = { from: new Date(day).toISOString() };
  it("labels days and hours in Lagos", () => {
    expect(bucketShort(sun, "day")).toBe("Sun");
    expect(bucketShort(mon, "day")).toBe("Mon");
    expect(bucketShort({ from: new Date(day + 9 * 3600_000).toISOString() }, "hour")).toBe("9");
    expect(bucketLong(sun, "day", new Date(day + 3600_000))).toBe("Today");
    expect(bucketLong(mon, "day", new Date(day + 3600_000))).toBe("Monday 19 October");
    expect(bucketLong({ from: new Date(day + 14 * 3600_000).toISOString() }, "hour")).toBe("2 pm to 3 pm");
    expect(hourWords(0)).toBe("midnight");
    expect(hourWords(12)).toBe("noon");
  });

  it("opens on the last day that has begun", () => {
    expect(openBucket(week, new Date(day + 3600_000))).toBe(6);
    expect(openBucket(week, new Date(day - 3 * 86400_000 + 60_000))).toBe(3);
    expect(openBucket([], new Date())).toBe(-1);
  });
});
