import { describe, expect, it } from "vitest";
import { pinProblem, PIN_DIGITS, weakPin } from "./pin";

describe("the PIN", () => {
  it("is four digits", () => {
    expect(PIN_DIGITS).toBe(4);
    expect(pinProblem("48291", "48291")).toBe("Type the 4 digits of your new PIN.");
    expect(pinProblem("482", "482")).toBe("Type the 4 digits of your new PIN.");
    expect(pinProblem("48a9", "48a9")).toBe("Type the 4 digits of your new PIN.");
  });
  it("is not one digit repeated nor a run", () => {
    for (const p of ["1111", "1234", "9876", "7890", "0987", "3210"]) expect(weakPin(p), p).toBe(true);
    for (const p of ["4829", "1243", "2580"]) expect(weakPin(p), p).toBe(false);
    expect(pinProblem("1234", "1234")).toMatch(/run like 1234/);
  });
  it("is typed the same twice", () => {
    expect(pinProblem("4829", "4828")).toBe("The two PINs are not the same.");
    expect(pinProblem("4829", "4829")).toBeNull();
  });
});
