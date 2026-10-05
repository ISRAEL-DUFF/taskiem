import { describe, expect, it } from "vitest";
import { isExpression, isStepId, parseDuration } from "./index.js";

describe("parseDuration", () => {
  it.each([
    ["250ms", 250],
    ["90s", 90_000],
    ["1h30m", 5_400_000],
    ["7d", 604_800_000],
    ["1m30s500ms", 90_500],
  ])("%s", (s, ms) => expect(parseDuration(s)).toBe(ms));

  it.each(["", "5", "5 minutes", "1h 30m", "-1s", "1w"])("rejects %j", (s) => {
    expect(() => parseDuration(s)).toThrow(/invalid duration/);
  });
});

describe("isExpression", () => {
  it("accepts =-prefixed strings", () => expect(isExpression("=trigger.body.x")).toBe(true));
  it.each(["=", "trigger.body", 5, null])("rejects %j", (v) => expect(isExpression(v)).toBe(false));
});

describe("isStepId", () => {
  it.each(["pay", "verify_bvn", "a1"])("accepts %s", (s) => expect(isStepId(s)).toBe(true));
  it.each(["Pay", "1a", "pay-now", "", "a".repeat(64)])("rejects %j", (s) => expect(isStepId(s)).toBe(false));
});
