import { describe, expect, it } from "vitest";
import { fixFor, needsToShow, stepsText, type ReadyNeed } from "./readiness";
import { roleLabel, roleName } from "../forms/RolePicker";

const need = (n: Partial<ReadyNeed>): ReadyNeed => ({
  kind: "connection",
  name: "main",
  connector: "paystack",
  service: "Paystack",
  problem: "missing",
  environment: "prod",
  steps: [{ id: "pay", name: "Pay the supplier" }],
  blocking: true,
  message: "Connect Paystack.",
  ...n,
});

describe("readiness", () => {
  it("offers the fix each need takes", () => {
    expect(fixFor(need({}))).toEqual({ kind: "connect", connector: "paystack", environment: "prod", name: "main", service: "Paystack" });
    // A step that names no connection gets the default name.
    expect(fixFor(need({ name: "" }))).toMatchObject({ kind: "connect", name: "main" });
    expect(fixFor(need({ problem: "ambiguous" }))).toEqual({ kind: "choose_connection", connector: "paystack", service: "Paystack" });
    expect(fixFor(need({ kind: "variable", name: "payout_note", blocking: false }))).toEqual({ kind: "set_variable", environment: "prod", name: "payout_note" });
    expect(fixFor(need({ kind: "secret", name: "api_key" }))).toEqual({ kind: "set_secret", environment: "prod", name: "api_key" });
    expect(fixFor(need({ kind: "table", name: "leave_requests" }))).toEqual({ kind: "make_table", name: "leave_requests" });
    expect(fixFor(need({ kind: "role", name: "credit_officer", problem: "nobody" }))).toEqual({ kind: "give_role", role: "credit_officer" });
  });

  it("shows production first, and dev's gaps only when asked", () => {
    const shown = needsToShow([
      need({ kind: "variable", name: "note", blocking: false, environment: "dev" }),
      need({ environment: "dev", blocking: false }),
      need({}),
    ]);
    expect(shown).toHaveLength(1);
    expect(shown[0]?.environment).toBe("prod");
    expect(needsToShow([need({ kind: "variable", name: "note", blocking: false, environment: "dev" })], true)).toHaveLength(1);
  });

  it("names the steps", () => {
    expect(stepsText(need({}))).toBe("Pay the supplier");
    expect(stepsText(need({ steps: [{ id: "a", name: "" }, { id: "b", name: "B" }, { id: "c", name: "C" }] }))).toBe("a and 2 more");
    expect(stepsText(need({ steps: [] }))).toBe("");
  });
});

describe("roles", () => {
  it("makes a role's name from words", () => {
    expect(roleName("Head of credit")).toBe("head_of_credit");
    expect(roleName("  Payroll  approver! ")).toBe("payroll_approver");
    expect(roleName("2nd signatory")).toBe("r_2nd_signatory");
  });
  it("labels roles in words, with who holds them", () => {
    expect(roleLabel({ name: "credit_officer", builtin: false, holders: 2 })).toBe("credit officer (2)");
    expect(roleLabel({ name: "credit_officer", builtin: false, holders: 0 })).toBe("credit officer (nobody yet)");
    expect(roleLabel({ name: "owner", builtin: true, holders: 1 })).toBe("Owner (the business's owners)");
  });
});
