import { describe, expect, it } from "vitest";
import { parsePayslipCard, paymentCounts, paymentTone, payslipFile, payslipPath, payslipReady, type RunPayment } from "./payslip";
import { actionPath, actionWords } from "./payrollrun";

const pay = (p: Partial<RunPayment>): RunPayment => ({
  status: "SCHEDULED",
  words: "Pays on 28 Nov",
  tone: "info",
  total_kobo: 0,
  paid: 0,
  failed: 0,
  returned: 0,
  pending: 0,
  short_by_kobo: 0,
  may_pay_now: false,
  may_retry: false,
  may_payslips: false,
  cannot_pay: false,
  ...p,
});

describe("after locking", () => {
  it("names the payment actions in plain words, on their own routes", () => {
    expect(actionWords("pay_now")).toBe("Pay now");
    expect(actionWords("retry_failed")).toBe("Pay the failed ones again");
    expect(actionPath("pay_now")).toBe("pay-now");
    expect(actionPath("retry_failed")).toBe("retry-failed");
    expect(actionPath("lock")).toBe("lock");
  });

  it("says who was paid and who was not", () => {
    expect(paymentCounts(pay({ paid: 46, failed: 2 }))).toBe("Paid 46 of 48 people, 2 not paid");
    expect(paymentCounts(pay({ paid: 1, returned: 1 }))).toBe("Paid 1 of 2 people, 1 returned by the bank");
    expect(paymentCounts(pay({ pending: 3 }))).toBe("");
  });

  it("shows a payment's state in its tone", () => {
    expect(paymentTone({ tone: "warn" })).toBe("warn");
    expect(paymentTone({ tone: "bad" })).toBe("danger");
    expect(paymentTone({ tone: "" })).toBe("muted");
  });

  it("reads a payslip for the payer side or one's own", () => {
    expect(payslipPath("p 1", false)).toBe("/v1/payroll/payslips/p%201");
    expect(payslipPath("p1", true, "/pdf")).toBe("/v1/payroll/me/payslips/p1/pdf");
    expect(payslipFile("November 2026")).toBe("payslip-november-2026.pdf");
    expect(payslipReady("November")).toBe("Your payslip for November is ready");
  });

  it("reads the chat's payslip card, and nothing else", () => {
    expect(parsePayslipCard('{"payslip_id":"p1","period":"November 2026"}')).toEqual({ payslip_id: "p1", period: "November 2026" });
    expect(parsePayslipCard('{"run_id":"r1"}')).toBeNull();
    expect(parsePayslipCard("hello")).toBeNull();
  });
});
