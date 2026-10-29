import { describe, expect, it } from "vitest";
import {
  limitInput,
  limitRequest,
  payProblem,
  purchaseActions,
  purchaseItems,
  purchaseSub,
  receiveForm,
  receiveProblem,
  receiveRequest,
  supplierProblem,
  supplierSub,
  type Purchase,
  type Supplier,
} from "./purchases";

const po: Purchase = {
  id: "p1",
  reference: "P-K2M9PA",
  supplier_id: "s1",
  supplier: "Dangote Depot",
  lines: [
    {
      item_id: "1",
      item: "Indomie",
      unit: "carton",
      quantity: 46,
      cost_kobo: 850000,
      received: 46,
    },
    {
      item_id: "2",
      item: "Rice",
      unit: "bag",
      quantity: 17,
      cost_kobo: 4000000,
      received: 10,
    },
  ],
  total_kobo: 107100000,
  status: "part_received",
  drafted_by: "user:ada",
  approved_by: "user:ada",
  sent_at: new Date().toISOString(),
  confirmed_at: new Date().toISOString(),
  confirmed_how: "supplier",
  counter: null,
  counter_at: null,
  received_at: null,
  cancelled_at: null,
  received_kobo: 79100000,
  paid_kobo: 0,
  owed_kobo: 79100000,
  created_at: new Date().toISOString(),
};

describe("suppliers", () => {
  it("says where orders reach a supplier and what is owed", () => {
    const s: Supplier = {
      id: "s1",
      name: "Dangote Depot",
      phone: "+2348031234567",
      reached: "sms",
      owed_kobo: 57100000,
      open: 2,
      invited_at: null,
      created_at: "",
      retired_at: null,
    };
    expect(supplierSub(s)).toBe(
      "+2348031234567 · Gets orders by text message until they tap the link · 2 open orders · you owe ₦571,000",
    );
    expect(
      supplierSub({ ...s, reached: "whatsapp", open: 0, owed_kobo: 0 }),
    ).toBe("+2348031234567 · Gets orders on WhatsApp");
  });
  it("checks a supplier's name and phone", () => {
    expect(supplierProblem({ name: "", phone: "", items: [] }, true)).toBe(
      "Give the supplier a name.",
    );
    expect(
      supplierProblem({ name: "Dangote", phone: "12", items: [] }, true),
    ).toContain("mobile number");
    expect(
      supplierProblem(
        { name: "Dangote", phone: "0803 123 4567", items: [] },
        true,
      ),
    ).toBeNull();
    expect(
      supplierProblem({ name: "Dangote", phone: "", items: [] }, false),
    ).toBeNull();
  });
  it("reads the approval limit", () => {
    expect(limitInput({ purchase_limit_kobo: null })).toBe("");
    expect(limitInput({ purchase_limit_kobo: 50000000 })).toBe("500000");
    expect(limitRequest("500,000")).toEqual({ purchase_limit_kobo: 50000000 });
    expect(limitRequest(" ")).toEqual({ purchase_limit_kobo: null });
  });
});

describe("purchase orders", () => {
  it("reads an order in a row", () => {
    expect(purchaseItems(po)).toBe("46 cartons Indomie, 17 bags Rice");
    expect(purchaseSub(po)).toContain(
      "the supplier confirmed · 1 item still to come · owed ₦791,000",
    );
  });
  it("offers what fits the status", () => {
    expect(purchaseActions(po)).toEqual({
      send: false,
      confirm: false,
      receive: true,
      pay: true,
      cancel: false,
    });
    expect(purchaseActions({ ...po, status: "drafted" })).toEqual({
      send: true,
      confirm: false,
      receive: false,
      pay: false,
      cancel: true,
    });
    expect(purchaseActions({ ...po, status: "sent" }).confirm).toBe(true);
  });
  it("fills a receipt with what is still to come", () => {
    const f = receiveForm(po);
    expect(f).toEqual({ "1": "0", "2": "7" });
    expect(receiveProblem(f)).toBeNull();
    expect(receiveRequest(f)).toEqual({
      lines: [{ item_id: "2", quantity: 7 }],
    });
    expect(receiveProblem({ "1": "0", "2": "0" })).toContain("at least one");
    expect(receiveProblem({ "2": "1.5" })).toContain("whole number");
  });
  it("checks a payment", () => {
    expect(payProblem("")).toContain("amount");
    expect(payProblem("0")).toContain("amount");
    expect(payProblem("425,000")).toBeNull();
  });
});
