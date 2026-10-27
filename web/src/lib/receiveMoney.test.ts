import { describe, expect, it } from "vitest";
import {
  accountProblem,
  approvalLine,
  bankOf,
  ownerTypeShort,
  receiveHero,
  sweepLine,
  sweepRequest,
  turnOnBlocker,
  withdrawAmount,
  type ReceiveMoney,
  type Sweep,
} from "./receiveMoney";

const base: ReceiveMoney = {
  available: true,
  plan: true,
  on: false,
  default_owner_type: "client_end_business",
  two_approvers_above_kobo: 0,
};

describe("receive money", () => {
  it("says why it cannot be turned on", () => {
    expect(turnOnBlocker(base, true)).toBe("");
    expect(turnOnBlocker({ ...base, available: false }, true)).toContain(
      "not available",
    );
    expect(
      turnOnBlocker(
        {
          ...base,
          plan: false,
          plan_note:
            "the Shop plan does not include receiving money with Taskiem",
        },
        true,
      ),
    ).toContain("Shop plan");
    expect(turnOnBlocker(base, false)).toBe("Only an owner can turn this on.");
  });

  it("shows the number as banks print it, or why it is not ready", () => {
    const on = {
      ...base,
      on: true,
      mode: "live" as const,
      account: {
        account_number: "8800000001",
        bank_name: "Wema Bank",
        account_name: "Ada Obi Stores",
      },
    };
    expect(receiveHero(on)).toEqual({
      label: "Your account number",
      figure: "8800 000 001",
      sub: "Wema Bank · Ada Obi Stores",
      tag: "Live",
    });
    const waiting = receiveHero({
      ...base,
      on: true,
      mode: "test",
      account_problem: "iswallet needs the director's BVN",
    });
    expect(waiting.figure).toBe("Not ready yet");
    expect(waiting.sub).toContain("BVN");
    expect(waiting.tag).toBe("Practice");
  });

  it("words the approval rule and where money is held", () => {
    expect(approvalLine(0)).toBe("An owner approves each withdrawal.");
    expect(approvalLine(50000000)).toBe(
      "An owner approves each withdrawal; two owners approve ₦500,000 or more.",
    );
    expect(ownerTypeShort("client_end_user_business")).toBe(
      "In your own account",
    );
    expect(ownerTypeShort(undefined)).toBe("");
  });

  it("checks amounts and account numbers", () => {
    expect(withdrawAmount("")).toEqual({
      problem: "Type the amount to withdraw.",
    });
    expect(withdrawAmount("abc").problem).toContain("number of naira");
    expect(withdrawAmount("25,000.50")).toEqual({ kobo: 2500050 });
    expect(withdrawAmount("90000", 7000000).problem).toBe(
      "Your wallet holds ₦70,000: withdraw that much or less.",
    );
    expect(accountProblem("0123456789")).toBe("");
    expect(accountProblem("12345")).toBe("An account number is 10 digits.");
  });

  it("reads a bank whichever names iswallet uses", () => {
    expect(bankOf({ code: "058", name: "GTBank" })).toEqual({
      code: "058",
      name: "GTBank",
    });
    expect(bankOf({ bank_code: "044", bank_name: "Access Bank" })).toEqual({
      code: "044",
      name: "Access Bank",
    });
  });

  it("words the sweep to the bank and checks its settings", () => {
    const sw: Sweep = {
      mode: "weekly",
      day: "friday",
      time: "17:00",
      above_kobo: 0,
      keep_kobo: 2000000,
      fee_allowance_kobo: 10000,
      minimum_kobo: 100000,
    };
    expect(sweepLine(sw)).toBe(
      "Every Friday at 17:00, what your wallet holds goes to your bank, keeping ₦20,000 in your wallet.",
    );
    expect(
      sweepLine({ ...sw, mode: "above", above_kobo: 20000000, keep_kobo: 0 }),
    ).toBe(
      "Whenever your wallet holds more than ₦200,000, what it holds goes to your bank. Checked every hour.",
    );
    expect(sweepLine(undefined)).toContain("Off");
    expect(sweepRequest("off", "", "", "", "")).toEqual({
      body: { mode: "off", keep_kobo: 0 },
    });
    expect(sweepRequest("weekly", "friday", "17:00", "", "20,000")).toEqual({
      body: {
        mode: "weekly",
        day: "friday",
        time: "17:00",
        keep_kobo: 2000000,
      },
    });
    expect(sweepRequest("weekly", "friday", "", "", "").problem).toContain(
      "time",
    );
    expect(sweepRequest("above", "", "", "500", "").problem).toContain(
      "at least ₦1,000",
    );
    expect(sweepRequest("above", "", "", "200000", "")).toEqual({
      body: { mode: "above", above_kobo: 20000000, keep_kobo: 0 },
    });
    expect(
      sweepRequest("weekly", "friday", "17:00", "", "abc").problem,
    ).toContain("keep");
  });
});
