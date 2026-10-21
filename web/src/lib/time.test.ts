import { describe, expect, it } from "vitest";
import { greeting, shortWhen, whenWords } from "./time";

const now = new Date(2026, 9, 21, 10, 30); // Wednesday 21 October 2026

describe("whenWords", () => {
  it("names today and yesterday", () => {
    expect(whenWords(new Date(2026, 9, 21, 9, 5), now)).toBe("Today, 09:05");
    expect(whenWords(new Date(2026, 9, 20, 18, 2), now)).toBe("Yesterday, 18:02");
  });
  it("names the weekday within the week, the date before it", () => {
    expect(whenWords(new Date(2026, 9, 17, 8, 0), now)).toBe("Sat 17 Oct, 08:00");
    expect(whenWords(new Date(2026, 9, 1, 8, 0), now)).toBe("1 Oct, 08:00");
  });
  it("adds the year only when it is not this one", () => {
    expect(whenWords(new Date(2025, 11, 31, 23, 59), now)).toBe("31 Dec 2025, 23:59");
  });
  it("keeps lists short", () => {
    expect(shortWhen(new Date(2026, 9, 21, 9, 41), now)).toBe("09:41");
    expect(shortWhen(new Date(2026, 9, 20, 9, 41), now)).toBe("Yesterday, 09:41");
  });
  it("greets by the hour", () => {
    expect(greeting(new Date(2026, 9, 21, 7))).toBe("Good morning");
    expect(greeting(new Date(2026, 9, 21, 13))).toBe("Good afternoon");
    expect(greeting(new Date(2026, 9, 21, 20))).toBe("Good evening");
  });
});
