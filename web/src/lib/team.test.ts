import { describe, expect, it } from "vitest";
import { emptyStaff, inviteSub, personSub, roleWords, staffProblem } from "./team";

describe("staff", () => {
  it("says roles as people do", () => {
    expect(roleWords("shift_manager")).toBe("Shift manager");
    expect(roleWords("cashier")).toBe("Cashier");
    expect(roleWords("payroll_approver")).toBe("Payroll approver");
  });
  it("checks the form", () => {
    expect(staffProblem(emptyStaff())).toBe("Give their name.");
    expect(staffProblem({ name: "Tunde", phone: "080", role: "cashier" })).toBe("Give their mobile number, like 0803 123 4567.");
    expect(staffProblem({ name: "Tunde", phone: "0803 123 4567", role: "" })).toBe("Choose what they do.");
    expect(staffProblem({ name: "Tunde", phone: "0803 123 4567", role: "cashier" })).toBeNull();
  });
  it("lines up a person and an invitation", () => {
    expect(personSub({ user_id: "u", name: "Tunde", roles: ["shift_manager"], phone: "•••9402", chat: true })).toBe("Shift manager · •••9402");
    expect(personSub({ user_id: "u", name: "Ada", roles: ["owner"], email: "ada@x.test", chat: false })).toBe("Owner · ada@x.test");
    expect(inviteSub({ id: "i", phone: "+2348031239403", roles: ["cashier"], inviter: "Ada", expires_at: "2026-11-16T10:00:00Z", created_at: "2026-11-09T10:00:00Z" })).toBe(
      "Cashier · +2348031239403 · not sent yet",
    );
  });
});
