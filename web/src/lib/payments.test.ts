import { describe, expect, it } from "vitest";
import {
  PAY_FIELDS,
  limitProblem,
  limitWords,
  needsOkWords,
  outcomeWords,
  payAmountKobo,
  payBody,
  payButtonWords,
  payFieldProblem,
  payWarnings,
  payeeName,
  paymentStatus,
  paymentSub,
  purposeOf,
  purposeWords,
  type Payment,
} from "./payments";

const base: Payment = {
  id: "p1",
  saved_id: "s1",
  personal: false,
  payee: "Bayo Rice Mills",
  bank_name: "GTBank",
  account: "••6789",
  account_name: "BAYO ADEWALE",
  amount_kobo: 4_500_000,
  purpose: "delivery",
  purpose_note: "",
  note: "",
  first_payment: false,
  changed_account: false,
  needs_ok: "",
  status: "prepared",
  reason: null,
  fee_kobo: null,
  started_from: "chat",
  prepared_by: "user:1",
  confirmed_by: null,
  created_at: "2026-11-21T10:00:00Z",
  confirmed_at: null,
  finished_at: null,
  again: false,
};

describe("amounts as people type them", () => {
  it("reads naira, thousands and millions", () => {
    expect(payAmountKobo("45000")).toBe(4_500_000);
    expect(payAmountKobo("₦45,000.50")).toBe(4_500_050);
    expect(payAmountKobo("45k")).toBe(4_500_000);
    expect(payAmountKobo("45K")).toBe(4_500_000);
    expect(payAmountKobo("1.5m")).toBe(150_000_000);
    expect(payAmountKobo("2.5k")).toBe(250_000);
  });
  it("says empty and wrong apart", () => {
    expect(payAmountKobo("")).toBeUndefined();
    expect(payAmountKobo("  ")).toBeUndefined();
    expect(payAmountKobo("forty")).toBeNaN();
    expect(payAmountKobo("45.123")).toBeNaN();
    expect(payAmountKobo("4k5")).toBeNaN();
  });
});

describe("what a payment is for", () => {
  it("maps the form's choices to the purposes", () => {
    expect(purposeOf("a bill")).toBe("bill");
    expect(purposeOf("wages")).toBe("wages");
    expect(purposeOf("a delivery")).toBe("delivery");
    expect(purposeOf("A refund")).toBe("refund");
    expect(purposeOf("stock bought")).toBe("stock");
    expect(purposeOf("stock")).toBe("stock");
    expect(purposeOf("other")).toBe("other");
    expect(purposeOf("generator fuel")).toBeUndefined();
    expect(purposeOf("")).toBeUndefined();
  });
  it("says it in words", () => {
    expect(purposeWords(base)).toBe("A delivery");
    expect(purposeWords({ purpose: "other", purpose_note: "generator fuel" })).toBe("Other: generator fuel");
    expect(purposeWords({ purpose: "stock", purpose_note: "" })).toBe("Stock bought");
  });
});

describe("the body of a payment to prepare", () => {
  it("pays a saved account by its @name", () => {
    expect(payBody({ who: "@bayo", amount: "45k", for: "a delivery", note: " rice " }, "chat")).toEqual({
      from: "chat",
      payee: "@bayo",
      amount_kobo: 4_500_000,
      purpose: "delivery",
      note: "rice",
    });
  });
  it("pays an account number at a bank", () => {
    expect(payBody({ number: "0123 456 789", bank: "GTBank", amount: "45,000", for: "stock bought" }, "tools")).toEqual({
      from: "tools",
      account_number: "0123456789",
      bank_name: "GTBank",
      amount_kobo: 4_500_000,
      purpose: "stock",
    });
  });
  it("pays a rider by their id, to the account kept for their pay", () => {
    expect(payBody({ rider: "r1", amount: "2,500", for: "a delivery" }, "tools")).toEqual({
      from: "tools",
      rider_id: "r1",
      amount_kobo: 250_000,
      purpose: "delivery",
    });
  });
  it("pays a staff member by their id, to the account linked to them", () => {
    expect(payBody({ staff: "u1", amount: "20,000", for: "wages" }, "tools")).toEqual({
      from: "tools",
      staff_id: "u1",
      amount_kobo: 2_000_000,
      purpose: "wages",
    });
  });
  it("keeps the word for other, and a typed reason that is not a choice", () => {
    expect(payBody({ who: "@bayo", amount: "1000", for: "other", for_note: "fuel" }, "chat")).toMatchObject({ purpose: "other", purpose_note: "fuel" });
    expect(payBody({ who: "@bayo", amount: "1000", for: "generator fuel" }, "chat")).toMatchObject({ purpose: "other", purpose_note: "generator fuel" });
  });
});

describe("field problems", () => {
  it("checks amounts, account numbers and payees", () => {
    expect(payFieldProblem("money", "45k")).toBeNull();
    expect(payFieldProblem("money", "0.5")).toBe("Pay at least ₦1.");
    expect(payFieldProblem("money", "lots")).toMatch(/Type an amount/);
    expect(payFieldProblem("account", "0123456789")).toBeNull();
    expect(payFieldProblem("account", "012345")).toBe("An account number is 10 digits.");
    expect(payFieldProblem("payee", "@bayo")).toBeNull();
    expect(payFieldProblem("payee", "@@")).toMatch(/saved accounts/);
    expect(payFieldProblem("text", "anything")).toBeNull();
    expect(payFieldProblem("money", "")).toBeNull();
  });
  it("says the amount on the button once it is there", () => {
    expect(payButtonWords({ amount: "45k" })).toBe("Check and pay ₦45,000");
    expect(payButtonWords({})).toBe("Check and pay");
  });
});

describe("what the person confirming sees", () => {
  it("puts a changed account first, in red", () => {
    const w = payWarnings({ ...base, changed_account: true, first_payment: true, again: true });
    expect(w.map((x) => x.key)).toEqual(["changed", "again", "first"]);
    expect(w[0]?.tone).toBe("danger");
    expect(w[0]?.words).toContain("Not the account you paid Bayo Rice Mills before (GTBank ••6789)");
    expect(w[1]?.words).toBe("You paid Bayo Rice Mills ₦45,000 in the last day. Pay again only if you mean to.");
  });
  it("says when the bank's name is not known yet", () => {
    expect(payWarnings({ ...base, account_name: null }).map((x) => x.key)).toEqual(["unchecked"]);
    expect(payWarnings(base)).toEqual([]);
  });
  it("names the payee as the bank does", () => {
    expect(payeeName(base)).toBe("BAYO ADEWALE");
    expect(payeeName({ ...base, account_name: null })).toBe("Bayo Rice Mills");
    expect(paymentSub(base)).toBe("GTBank ••6789 · A delivery");
  });
});

describe("statuses and outcomes", () => {
  it("has words and a tone for each status", () => {
    expect(paymentStatus("paid")).toEqual({ label: "Paid", tone: "ok" });
    expect(paymentStatus("waiting_ok")).toEqual({ label: "Waiting for the owner's OK", tone: "warn" });
    expect(paymentStatus("failed").tone).toBe("danger");
    expect(paymentStatus("cancelled").tone).toBe("muted");
    expect(paymentStatus("something").tone).toBe("muted");
  });
  it("says why a payment waits", () => {
    expect(needsOkWords("limit")).toBe("It waits for the owner's OK in Approvals: it is over the payer's limit.");
    expect(needsOkWords("limit,changed")).toBe(
      "It waits for the owner's OK in Approvals: it is over the payer's limit, and the account changed since it was last paid.",
    );
    expect(needsOkWords("")).toBe("It waits for the owner's OK in Approvals.");
  });
  it("says how it ended", () => {
    expect(outcomeWords({ ...base, status: "paid", fee_kobo: 2500 })).toBe("Received by the bank. The fee was ₦25.");
    expect(outcomeWords({ ...base, status: "failed", reason: "account closed" })).toBe("Not paid: account closed. Any money taken is returned to your balance.");
    expect(outcomeWords({ ...base, status: "cancelled" })).toBe("Cancelled before it was sent. Nothing was paid.");
  });
});

describe("limits", () => {
  it("checks what the owner types", () => {
    expect(limitProblem("50k", "200k")).toBeNull();
    expect(limitProblem("0", "0")).toBeNull();
    expect(limitProblem("300k", "200k")).toBe("One payment can't be more than a day's total.");
    expect(limitProblem("", "200k")).toMatch(/Type both/);
    expect(limitProblem("lots", "200k")).toMatch(/in naira/);
  });
  it("says a role's limit in words", () => {
    expect(limitWords(undefined)).toBe("Every payment waits for the owner's OK");
    expect(limitWords({ one_kobo: 5_000_000, day_kobo: 20_000_000 })).toBe("Up to ₦50,000 a payment, ₦200,000 a day");
  });
});

describe("the fields of the two ways in", () => {
  it("match the chat's catalogue", () => {
    expect(PAY_FIELDS.person.map((f) => `${f.key}:${f.kind}`)).toEqual(["who:payee", "amount:money", "for:choice", "note:text"]);
    expect(PAY_FIELDS.account.map((f) => `${f.key}:${f.kind}`)).toEqual(["number:account", "bank:bank", "amount:money", "for:choice"]);
  });
});
