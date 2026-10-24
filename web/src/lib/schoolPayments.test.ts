import { describe, expect, it } from "vitest";
import {
  amountKobo,
  decisionLine,
  emptyPayment,
  howWords,
  isApprovalNeeded,
  kindWords,
  paymentProblem,
  paymentRequest,
  postedLine,
  postingStatus,
  receiptHow,
  receiptPDF,
  receiptQuery,
  receiptStatus,
  requestDetails,
  requestTitle,
  type Posting,
  type Receipt,
} from "./schoolPayments";

const posting: Posting = {
  id: "p1",
  kind: "cash_posting",
  status: "posted",
  student_id: "s1",
  admission_number: "GCA/001",
  name: "E•••",
  class: "JSS 1 A",
  amount_kobo: 400000000,
  details: { how: "cash", date: "2026-10-23", note: "Term fees" },
  requested_by: "u1",
  requested_email: "bursar@school.test",
  requested_at: "2026-10-23T09:00:00Z",
  result: { parts: [{ invoice_id: "i1", number: "INV-0007", amount_kobo: 300000000, receipt_number: "RCT-2026T1-0003" }], credit_kobo: 100000000 },
};

describe("the payment form", () => {
  it("reads amounts in naira", () => {
    expect(amountKobo("25,000")).toBe(2500000);
    expect(amountKobo("₦ 1,500.50")).toBe(150050);
    expect(amountKobo("12.345")).toBeNaN();
    expect(amountKobo("abc")).toBeNaN();
  });
  it("says what is missing", () => {
    expect(paymentProblem(emptyPayment())).toMatch(/which student/);
    expect(paymentProblem({ ...emptyPayment("GCA/001"), amount: "0.50" })).toMatch(/at least ₦1/);
    expect(paymentProblem({ ...emptyPayment("GCA/001"), amount: "500", date: "23/10/2026" })).toMatch(/2026-10-23/);
    expect(paymentProblem({ ...emptyPayment("GCA/001"), amount: "500", note: "x".repeat(201) })).toMatch(/200 characters/);
    expect(paymentProblem({ ...emptyPayment("GCA/001"), amount: "500" })).toBeNull();
  });
  it("builds the request", () => {
    expect(paymentRequest({ student: " GCA/001 ", amount: "5,000", how: "transfer", date: "2026-10-23", note: " paid at the bank " })).toEqual({
      student: "GCA/001",
      amount_kobo: 500000,
      how: "transfer",
      date: "2026-10-23",
      note: "paid at the bank",
    });
    expect(paymentRequest({ student: "GCA/001", amount: "500", how: "cash", date: "", note: "" })).toEqual({ student: "GCA/001", amount_kobo: 50000, how: "cash" });
  });
});

describe("words", () => {
  it("names kinds, hows and states", () => {
    expect(kindWords("cash_posting")).toBe("Cash posting");
    expect(kindWords("term_invoice")).toBe("Term invoice with a discount");
    expect(howWords("pos")).toBe("POS");
    expect(postingStatus("pending")).toEqual({ label: "Waiting for a second person", tone: "pending" });
    expect(postingStatus("declined").label).toBe("Declined");
  });
  it("says what a posting did", () => {
    expect(postedLine(posting)).toBe("₦4,000,000 posted: ₦3,000,000 towards INV-0007 (receipt RCT-2026T1-0003); ₦1,000,000 kept as the family's credit.");
    expect(postedLine({ ...posting, result: { parts: [] } })).toMatch(/family's credit/);
    expect(postedLine({ ...posting, result: { parts: [{ invoice_id: "a", number: "INV-1", amount_kobo: 100 }, { invoice_id: "b", number: "INV-2", amount_kobo: 200 }] } })).toMatch(/2 invoices paid: INV-1 ₦1, INV-2 ₦2/);
  });
  it("describes a request for the second person", () => {
    expect(requestTitle(posting)).toBe("Cash posting of ₦4,000,000 for GCA/001 (JSS 1 A)");
    expect(requestDetails(posting)).toEqual(["Paid cash on 2026-10-23.", "Note: Term fees", "Asked by bursar@school.test."]);
    const disc: Posting = { ...posting, kind: "discount", details: { kind: "staff_child", percent: 50, reason: "Teacher's son" } };
    expect(requestDetails(disc)[0]).toBe("A staff child discount, 50% of the school's own fees: Teacher's son");
    const am: Posting = { ...posting, kind: "discount_amendment", admission_number: undefined, invoice_number: "INV-0009", details: { reason: "Hardship" } };
    expect(requestTitle(am)).toBe("Discount on an invoice of ₦4,000,000 on INV-0009");
    const held: Posting = { ...posting, kind: "term_invoice", details: { send: true } };
    expect(requestDetails(held)[0]).toMatch(/sent on approval/);
  });
  it("says how it was decided", () => {
    expect(decisionLine({ ...posting, decided_email: "owner@school.test" })).toBe("Approved by owner@school.test.");
    expect(decisionLine({ ...posting, status: "declined", decided_by: "u2", decision_reason: "No cash" })).toBe("Declined by u2: No cash");
    expect(decisionLine({ ...posting, status: "pending" })).toBe("");
  });
  it("recognises a request recorded for approval", () => {
    expect(isApprovalNeeded({ code: "needs_approval", error: "x", request: posting })).toBe(true);
    expect(isApprovalNeeded({ posting })).toBe(false);
    expect(isApprovalNeeded(null)).toBe(false);
  });
});

describe("receipts", () => {
  const r: Receipt = {
    id: "r1",
    number: "RCT-2026T1-0003",
    term_id: "t",
    term: "First term 2026/2027",
    student_id: "s1",
    admission_number: "GCA/001",
    name: "E•••",
    class_id: "c",
    class: "JSS 1 A",
    invoice_id: "i1",
    invoice_number: "INV-0007",
    part_id: "p",
    amount_kobo: 300000000,
    balance_after_kobo: 0,
    how: "manual",
    paid_at: "2026-10-23T09:00:00Z",
    issued_by: "u1",
    issued_at: "2026-10-23T09:00:00Z",
    voided: false,
  };
  it("words their state, how and links", () => {
    expect(receiptStatus(r)).toEqual({ label: "Valid", tone: "ok" });
    expect(receiptStatus({ ...r, voided: true }).label).toBe("Voided");
    expect(receiptHow("exact")).toBe("Paid into the student's account");
    expect(receiptHow("manual")).toBe("Received by the school");
    expect(receiptPDF(r)).toBe("/v1/schools/receipts/r1/pdf");
    expect(receiptQuery(" rct-2026t1-0003 ")).toBe("RCT-2026T1-0003");
  });
});
