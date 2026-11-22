import { describe, expect, it } from "vitest";
import { choosable, countsWords, employeeSub, frequencyWords, lineState, payrollaLinkBody, peopleCount, periodLabel, runStatus, sortLines, suggestionWords, type PayrollLine } from "./payroll";

describe("payroll words", () => {
  it("says a run's status in plain words", () => {
    expect(runStatus("COMPUTED")).toEqual({ label: "Waiting for approval", tone: "warn" });
    expect(runStatus("PARTIALLY_COMPLETED").label).toBe("Partly paid");
    expect(runStatus("COMPLETED")).toEqual({ label: "Paid", tone: "ok" });
    expect(runStatus("DISBURSING").label).toBe("Paying");
    expect(runStatus("FAILED").tone).toBe("danger");
    expect(runStatus("SOMETHING_NEW").label).toBe("In Payrolla");
  });
  it("says whether a person's pay arrived", () => {
    expect(lineState("failed").label).toBe("Not paid");
    expect(lineState("returned").label).toBe("Returned");
    expect(lineState("paid").tone).toBe("ok");
  });
  it("names the period", () => {
    expect(periodLabel({ period_start: "2026-11-01", period_end: "2026-11-30" })).toBe("November 2026");
    expect(periodLabel({ period_start: "2026-11-16", period_end: "2026-12-15" })).toBe("16 Nov to 15 Dec 2026");
    expect(periodLabel({ period_start: "2026-12-16", period_end: "2027-01-15" })).toBe("16 Dec 2026 to 15 Jan 2027");
    expect(periodLabel({ period_start: "", period_end: "" })).toBe("");
  });
  it("counts people and says how often", () => {
    expect(peopleCount(1)).toBe("1 person");
    expect(peopleCount(12)).toBe("12 people");
    expect(frequencyWords("MONTHLY")).toBe("Paid monthly");
    expect(frequencyWords("ODD")).toBe("");
    expect(countsWords({ paid: 10, waiting: 0, failed: 1, returned: 1 })).toBe("10 paid · 1 not paid · 1 returned");
  });
  it("puts those not paid first", () => {
    const l = (name: string, state: PayrollLine["state"]): PayrollLine => ({ payslip_id: name, employee_id: name, name, net_kobo: 1, status: "", state, staff: null });
    expect(sortLines([l("Ada", "paid"), l("Bayo", "returned"), l("Chi", "failed"), l("Dami", "waiting")]).map((x) => x.name)).toEqual(["Chi", "Bayo", "Dami", "Ada"]);
  });
  it("suggests and chooses employees", () => {
    expect(suggestionWords({ employee_id: "e", name: "Musa Bello", by: "email" })).toBe("Looks like Musa Bello in Payrolla (same email)");
    expect(suggestionWords({ employee_id: "e", name: "Musa Bello", by: "phone" })).toBe("Looks like Musa Bello in Payrolla (same phone number)");
    const emps = [
      { id: "1", name: "Zed", status: "ACTIVE", linked_to: null },
      { id: "2", name: "Ade", status: "TERMINATED", linked_to: null, department: "Kitchen" },
      { id: "3", name: "Bola", status: "ACTIVE", linked_to: "u2" },
      { id: "4", name: "Kemi", status: "ACTIVE", linked_to: "u1" },
    ];
    expect(choosable(emps, "u1").map((e) => e.id)).toEqual(["4", "1", "2"]);
    expect(employeeSub(emps[1]!)).toBe("Kitchen · Has left");
    expect(payrollaLinkBody(null)).toEqual({});
    expect(payrollaLinkBody("e1")).toEqual({ employee_id: "e1" });
  });
});
