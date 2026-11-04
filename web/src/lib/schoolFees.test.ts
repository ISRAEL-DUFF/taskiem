import { describe, expect, it } from "vitest";
import { amendmentProblem, amendmentRequest, emptyAmendment, lineNote, removableLines, type CustomerInvoice } from "./invoices";
import {
  discountProblem,
  discountRequest,
  discountWords,
  emptyDiscount,
  emptyFeeRow,
  feeRows,
  feeRowsProblem,
  feeRowsRequest,
  feeRowsTotals,
  invoicedLine,
  needsApproval,
  planSummary,
  savedFeesLine,
  termTotals,
  type TermInvoiced,
  type TermPlan,
} from "./schoolFees";

const rows = [
  { name: "Tuition", amount: "100,000", kind: "required" as const, passThroughFor: "" },
  { name: "Transport", amount: "20000", kind: "optional" as const, passThroughFor: "" },
  { name: "WAEC fee", amount: "18,000", kind: "required" as const, passThroughFor: " WAEC " },
  emptyFeeRow(),
];

describe("fee structures", () => {
  it("checks the rows, leaving empty ones out", () => {
    expect(feeRowsProblem(rows)).toBeNull();
    expect(feeRowsProblem([...rows, { ...emptyFeeRow(), name: "tuition", amount: "5" }])).toMatch(/twice/);
    expect(feeRowsProblem([{ ...emptyFeeRow(), name: "Books" }])).toMatch(/amount/);
    expect(feeRowsProblem([{ ...emptyFeeRow(), amount: "500" }])).toMatch(/name/);
  });
  it("sends kobo, with who a line is collected for", () => {
    expect(feeRowsRequest(rows).items).toEqual([
      { name: "Tuition", amount_kobo: 10_000_000, kind: "required" },
      { name: "Transport", amount_kobo: 2_000_000, kind: "optional" },
      { name: "WAEC fee", amount_kobo: 1_800_000, kind: "required", pass_through_for: "WAEC" },
    ]);
  });
  it("totals what every student pays", () => {
    expect(feeRowsTotals(rows)).toEqual({ required: 11_800_000, passThrough: 1_800_000, optional: 2_000_000 });
  });
  it("reads items back as rows", () => {
    expect(feeRows([{ id: "1", term_id: "t", class_id: "c", name: "Tuition", amount_kobo: 10_000_050, kind: "required", position: 0 }])[0]?.amount).toBe("100000.50");
  });
  it("says what a save did to drafts", () => {
    const fees = { class_id: "c", class: "JSS 1 A", items: [], required_kobo: 11_800_000, pass_through_kobo: 0 };
    expect(savedFeesLine({ fees, drafts_updated: 0 })).toBe("Saved JSS 1 A's fees: ₦118,000 for every student.");
    expect(savedFeesLine({ fees, drafts_updated: 2 })).toMatch(/2 draft invoices follow the change/);
  });
});

describe("discounts", () => {
  it("is a percent or an amount, with a reason", () => {
    expect(discountProblem({ ...emptyDiscount(), value: "10", reason: "Second child" })).toBeNull();
    expect(discountProblem({ ...emptyDiscount(), value: "110", reason: "x" })).toMatch(/1 to 100/);
    expect(discountProblem({ ...emptyDiscount(), value: "10" })).toMatch(/why/);
    expect(discountRequest({ kind: "hardship", by: "amount", value: "5,000", reason: " Hard year " })).toEqual({ kind: "hardship", amount_kobo: 500_000, reason: "Hard year" });
    expect(discountWords({ kind: "sibling", percent: 10 })).toBe("Sibling, 10% of the school's own fees");
  });
  it("words a refusal that needs a second person", () => {
    expect(needsApproval(409, { code: "needs_approval" })).toMatch(/second person/);
    expect(needsApproval(202, { code: "needs_approval", request: {} })).toMatch(/recorded/);
    expect(needsApproval(409, { code: "other" })).toBeNull();
    expect(needsApproval(201, { discount: {} })).toBeNull();
  });
});

const plan: TermPlan = {
  term: { id: "t", session_id: "s", session: "2026/2027", name: "First term", starts_on: "", ends_on: "", fees_due_on: "", state: "current", started_at: null, enrolled: 3 },
  enrolled: 3,
  to_invoice: 2,
  total_kobo: 25_000_000,
  pass_through_kobo: 3_600_000,
  classes: [],
  already_invoiced: 0,
  no_contact: [{ id: "x", admission_number: "GCA/3", name: "N•••", class: "JSS 1" }],
  no_fees: [],
  nothing_to_pay: [],
  needs_approval: [],
  hash: "h",
};

describe("invoicing a term", () => {
  it("summarises the plan", () => {
    expect(planSummary(plan)).toEqual([
      "2 students to invoice, ₦250,000 in all.",
      "₦36,000 of it is collected for others (such as exam bodies), not the school's own.",
      "1 left out: no guardian to send it to (add one on the Families page).",
    ]);
    expect(planSummary({ ...plan, to_invoice: 0, already_invoiced: 2, pass_through_kobo: 0, no_contact: [] })[0]).toMatch(/Every student/);
  });
  it("says what was done", () => {
    const r: TermInvoiced = { ...plan, created: 2, send: true, sent: 2, failed: 0 };
    expect(invoicedLine(r)).toBe("2 invoices made; 2 sent.");
    expect(invoicedLine({ ...r, send: false })).toMatch(/drafts/);
    expect(invoicedLine({ ...r, created: 0, send: false })).toMatch(/Nobody new/);
    expect(invoicedLine({ ...r, created: 0 })).toBe("Nobody new to invoice; 2 drafts sent.");
  });
  it("totals the term's invoices, leaving void ones out", () => {
    expect(
      termTotals([
        { total_kobo: 100, paid_kobo: 40, due_kobo: 60, status: "part_paid" },
        { total_kobo: 50, paid_kobo: 0, due_kobo: 0, status: "void" },
      ]),
    ).toEqual({ total: 100, paid: 40, due: 60 });
  });
});

describe("amendments", () => {
  const x: Pick<CustomerInvoice, "lines" | "amendments"> = {
    lines: [
      { description: "Tuition", quantity: 1, unit_kobo: 10_000_000 },
      { description: "WAEC fee", quantity: 1, unit_kobo: 1_800_000, pass_through_for: "WAEC" },
      { description: "Sibling discount (10%)", quantity: 1, unit_kobo: -1_000_000, kind: "discount", reason: "Second child" },
      { description: "Taken out: WAEC fee", quantity: 1, unit_kobo: -1_800_000, kind: "removal", amendment: "a1", reason: "Not sitting" },
    ],
    amendments: [{ id: "a1", kind: "remove", line: { description: "", quantity: 1, unit_kobo: 0 }, amount_kobo: -1_800_000, removes: 1, reason: "Not sitting", by: "u", created_at: "" }],
  };
  it("offers only items not taken out", () => {
    expect(removableLines(x)).toEqual([{ index: 0, label: "Tuition (₦100,000)" }]);
  });
  it("needs a reason, and an amount or the line", () => {
    expect(amendmentProblem({ ...emptyAmendment(), description: "Books", amount: "5000" })).toMatch(/why/);
    expect(amendmentProblem({ ...emptyAmendment(), description: "Books", amount: "5000", reason: "Lost" })).toBeNull();
    expect(amendmentProblem({ ...emptyAmendment(), kind: "remove", reason: "x" })).toMatch(/Choose/);
    expect(amendmentRequest({ ...emptyAmendment(), kind: "remove", line: "0", reason: " x " })).toEqual({ kind: "remove", line: 0, reason: "x" });
    expect(amendmentRequest({ ...emptyAmendment(), kind: "discount", amount: "1,000", reason: "Goodwill", passThroughFor: "WAEC" })).toEqual({ kind: "discount", amount_kobo: 100_000, reason: "Goodwill" });
  });
  it("labels pass-through lines and reasons", () => {
    expect(lineNote({ description: "WAEC fee", quantity: 1, unit_kobo: 1_800_000, pass_through_for: "WAEC" })).toBe("Collected on behalf of WAEC.");
    expect(lineNote({ description: "Sibling discount (10%)", quantity: 1, unit_kobo: -1_000_000, kind: "discount", reason: "Second child" })).toBe("Second child");
    expect(lineNote({ description: "Books", quantity: 1, unit_kobo: 100, amendment: "a2", reason: "Lost" })).toBe("Added after the invoice was sent. Lost");
  });
});
