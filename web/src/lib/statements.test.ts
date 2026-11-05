import { describe, expect, it } from "vitest";
import { daysValue, termsWords } from "./statements";

describe("statements", () => {
  it("says the terms", () => {
    expect(termsWords(30)).toBe("30 days to pay");
    expect(termsWords(1)).toBe("1 day to pay");
    expect(termsWords(0)).toBe("pay at once");
    expect(termsWords(undefined)).toBe("");
  });
  it("reads typed days", () => {
    expect(daysValue("")).toBeNull();
    expect(daysValue("14")).toBe(14);
    expect(daysValue("400")).toBeNaN();
    expect(daysValue("two")).toBeNaN();
  });
});
