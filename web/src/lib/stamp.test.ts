import { describe, expect, it } from "vitest";
import { cmpTime, isAfter, stamp } from "./stamp";

describe("times from the server", () => {
  it("compare as times, whatever the digits after the second", () => {
    // As strings, ".5Z" sorts after ".50123Z".
    expect("2026-01-01T10:00:00.5Z" > "2026-01-01T10:00:00.50123Z").toBe(true);
    expect(
      isAfter("2026-01-01T10:00:00.5Z", "2026-01-01T10:00:00.50123Z"),
    ).toBe(false);
    expect(
      isAfter("2026-01-01T10:00:00.50123Z", "2026-01-01T10:00:00.5Z"),
    ).toBe(true);
    expect(cmpTime("2026-01-01T10:00:00Z", "2026-01-01T10:00:00.000Z")).toBe(0);
    expect(cmpTime("2026-01-01T10:00:00Z", "2026-01-01T10:00:00.1Z")).toBe(-1);
  });
  it("keep the microseconds and nanoseconds a Date would lose", () => {
    expect(
      isAfter("2026-01-01T10:00:00.862289032Z", "2026-01-01T10:00:00.862289Z"),
    ).toBe(true);
  });
  it("leave what is not a time alone", () => {
    expect(stamp("soon")).toBe("soon");
  });
});
