import { describe, expect, it } from "vitest";
import { billHero, billsDue, billStatus, type Bill } from "./bills";

const bill: Bill = {
  id: "b1",
  link: "l1",
  supplier: "s1",
  supplier_name: "Dangote Stores",
  reference: "INV-0042",
  lines: [{ description: "Rice", quantity: 5, unit_kobo: 8500000 }],
  amount_kobo: 42500000,
  paid_kobo: 10000000,
  due_kobo: 32500000,
  status: "part_paid",
  allow_part_payments: true,
  disputed: false,
  declared_paid_kobo: 0,
  mirrored_at: "2026-10-31T09:00:00Z",
  created_at: "2026-10-31T09:00:00Z",
};

describe("bills", () => {
  it("say a bill's status in plain words", () => {
    expect(billStatus("open")).toEqual({ label: "To pay", tone: "warn" });
    expect(billStatus("part_paid").label).toBe("Part-paid");
    expect(billStatus("paid").tone).toBe("ok");
    expect(billStatus("disputed").label).toBe("Queried");
    expect(billStatus("void")).toEqual({ label: "Cancelled", tone: "muted" });
  });

  it("lead a bill with what is left to pay", () => {
    expect(billHero(bill)).toEqual({ label: "To pay", kobo: 32500000, sub: "of ₦425,000 · ₦100,000 paid", paid: 10000000 / 42500000 });
    expect(billHero({ ...bill, status: "paid", paid_kobo: 42500000, due_kobo: 0 })).toEqual({ label: "Paid in full", kobo: 42500000, sub: "Nothing left to pay", paid: 1 });
    expect(billHero({ ...bill, status: "void" }).sub).toBe("Dangote Stores cancelled this invoice");
  });

  it("add up what is left to pay", () => {
    expect(billsDue([bill, { ...bill, id: "b2", due_kobo: 500000 }, { ...bill, id: "b3", status: "void" }])).toBe(33000000);
    expect(billsDue(undefined)).toBe(0);
  });
});

describe("an invoice to a linked business", () => {
  it("names it by its link key, with no phone or email needed", async () => {
    const { draftRequest, formFromInvoice, invoiceProblem, newInvoiceForm } = await import("./invoices");
    const f = { ...newInvoiceForm(), linked: "t-ada", name: "Ada's Kitchen", amount: "25000", phone: "0803 000 0000" };
    expect(invoiceProblem(f)).toBeNull();
    const d = draftRequest(f);
    expect(d.customer).toEqual({ name: "Ada's Kitchen", key: "link:t-ada" });
    expect(invoiceProblem({ ...f, linked: undefined, phone: "" })).toMatch(/phone number or email/);
    const back = formFromInvoice({ customer: { name: "Ada's Kitchen", key: "link:t-ada" }, lines: [], connection: "", fee_paid_by: "business" } as never);
    expect(back.linked).toBe("t-ada");
  });
});
