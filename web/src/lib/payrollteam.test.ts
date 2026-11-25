import { describe, expect, it } from "vitest";
import {
  addBody,
  addProblem,
  bankLine,
  changeBody,
  changeFormOf,
  emptyAdd,
  fromWords,
  isAccountNumber,
  memberSub,
  payOf,
  splitName,
  today,
  viaWords,
  type TeamMember,
} from "./payrollteam";

const zainab: TeamMember = {
  id: "e1",
  name: "Zainab Bello",
  first_name: "Zainab",
  last_name: "Bello",
  phone: "+2348011112201",
  department: "Operations",
  status: "active",
  bank: { bank: "Guaranty Trust Bank", bank_code: "058", account_name: "ZAINAB A. BELLO", last4: "6789" },
  pay: { gross_kobo: 45000000, basic_kobo: 0, housing_kobo: 0, transport_kobo: 0, pensionable: true, from: "2026-01-01" },
  waiting_change: null,
  staff: null,
};

describe("staff and pay (P4b-2)", () => {
  it("says who asked for a change and how", () => {
    expect(viaWords({ via: "in_taskiem", asked_by: "Ngozi" })).toBe("Asked by Ngozi in Taskiem");
    expect(viaWords({ via: "in_taskiem" })).toBe("Asked in Taskiem");
    expect(viaWords({ via: "by_employee" })).toBe("Asked by the employee");
    expect(viaWords({ via: "in_payrolla" })).toBe("Asked in Payrolla");
  });

  it("shows an account masked, and a person's line", () => {
    expect(bankLine(zainab.bank)).toBe("Guaranty Trust Bank ••6789");
    expect(bankLine(null)).toBe("No account yet");
    expect(memberSub(zainab)).toBe("Operations · Guaranty Trust Bank ••6789");
    const waiting = { ...zainab, status: "left" as const, waiting_change: { id: "c", employee_id: "e1", bank: "Access Bank", account_name: "Z", last4: "2222", via: "in_taskiem" as const, mine: false, requested_at: "" } };
    expect(memberSub(waiting)).toBe("Operations · Guaranty Trust Bank ••6789 · Has left · Account change waiting");
  });

  it("checks the Add someone form and builds its body", () => {
    const f = { ...emptyAdd(), first: "Zainab", last: "Bello", phone: "0801 111 2201", gross: "450,000", basic: "300,000", bankCode: "058", account: "012 345 6789" };
    expect(addProblem({ ...f, phone: "" })).toBe("Give a phone number or an email, for their payslip.");
    expect(addProblem({ ...f, gross: "" })).toBe("Give their monthly pay.");
    expect(addProblem({ ...f, basic: "500,000" })).toMatch(/can't add up/);
    expect(addProblem({ ...f, account: "123" })).toMatch(/10-digit/);
    expect(addProblem(f)).toBe("");
    const body = addBody({ ...f, staffUser: "u1" }, "k1");
    expect(body).toMatchObject({
      key: "k1",
      first_name: "Zainab",
      staff_user: "u1",
      bank: { bank_code: "058", account_number: "0123456789" },
      pay: { gross_kobo: 45000000, basic_kobo: 30000000, pensionable: true },
    });
    expect(body).not.toHaveProperty("email");
    expect(payOf({ gross: "1,000", basic: "x", housing: "", transport: "" })).toMatch(/amounts/);
  });

  it("sends only what changed, pay from a date, never a phone", () => {
    const f = changeFormOf(zainab, new Date(2026, 10, 24));
    expect(f.from).toBe("2026-11-24");
    expect(changeBody(zainab, f, "k")).toBeNull();
    expect(changeBody(zainab, { ...f, department: "Sales" }, "k")).toEqual({ key: "k", department: "Sales" });
    expect(changeBody(zainab, { ...f, gross: "500,000", from: "2027-01-01" }, "k")).toEqual({ key: "k", pay: { gross_kobo: 50000000, from: "2027-01-01" } });
    expect(changeBody(zainab, { ...f, gross: "abc" }, "k")).toBe("Give their monthly pay.");
    expect(JSON.stringify(changeBody(zainab, { ...f, first: "Zee" }, "k"))).not.toMatch(/phone|job/);
    expect(fromWords("2027-01-01")).toBe("from 1 January 2027");
  });

  it("splits a name and reads an account number", () => {
    expect(splitName("Zainab Amina Bello")).toEqual({ first: "Zainab Amina", last: "Bello" });
    expect(splitName("Musa")).toEqual({ first: "Musa", last: "" });
    expect(isAccountNumber("0123-456-789")).toBe(true);
    expect(isAccountNumber("01234")).toBe(false);
    expect(today(new Date(2026, 0, 5))).toBe("2026-01-05");
  });
});
