import { describe, expect, it } from "vitest";
import { phoneOf, plainValue, resolveSchema, showValue } from "./values";

describe("plain values", () => {
  it("keeps what a person types as they mean it", () => {
    expect(plainValue("Ade")).toBe("Ade");
    expect(plainValue(" Ade Stores ")).toBe("Ade Stores");
    expect(plainValue("0803 123 4567")).toBe("+2348031234567");
    expect(plainValue("+234 803 123 4567")).toBe("+2348031234567");
    expect(plainValue("2348031234567")).toBe("+2348031234567");
    expect(plainValue("100000")).toBe(100000);
    expect(plainValue("12.5")).toBe(12.5);
    expect(plainValue("0123456789")).toBe("0123456789"); // an account number stays text
    expect(plainValue("true")).toBe(true);
    expect(plainValue('"quoted"')).toBe('"quoted"');
  });
  it("finds phone numbers in any form", () => {
    expect(phoneOf("08031234567")).toBe("+2348031234567");
    expect(phoneOf("(0803) 123-4567")).toBe("+2348031234567");
    expect(phoneOf("12345")).toBeNull();
  });
  it("shows values without JSON", () => {
    expect(showValue("Ade")).toBe("Ade");
    expect(showValue(42)).toBe("42");
    expect(showValue({ a: 1 })).toBe('{"a":1}');
  });
  it("resolves an inputs schema's types for a form", () => {
    const s = resolveSchema({ $ref: "#/types/Loan" } as never, { Loan: { type: "object", properties: { amount: { type: "integer" } } } });
    expect(s?.properties?.amount?.type).toBe("integer");
  });
});
