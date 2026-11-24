import { describe, expect, it } from "vitest";
import { batchMoney } from "./batches";
import {
  balanceAfter,
  beneficiaryName,
  diaryState,
  feeWords,
  nairaTyped,
  outcomeWords,
  payBody,
  paymentStatus,
  type Payment,
} from "./payments";

// Paying one person, finished (build-plan-gaps H7): the fee before
// paying, the balance after, the diary line's state, a beneficiary's name
// and the batch's foot.

const x = (over: Partial<Payment> = {}): Payment => ({
  id: "p1",
  saved_id: null,
  personal: false,
  payee: "Chidi Okeke",
  bank_name: "GTBank",
  account: "••6789",
  account_name: "CHIDI OKEKE",
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
  started_from: "tools",
  prepared_by: "user:ada",
  confirmed_by: null,
  created_at: "2026-11-24T10:00:00Z",
  confirmed_at: null,
  finished_at: null,
  again: false,
  ...over,
});

describe("the fee and the balance before paying", () => {
  it("says the fee quoted, or that it shows after, or the wallet's route", () => {
    expect(feeWords(x({ fee_quote_kobo: 2500 }))).toBe("₦45,000 + ₦25 fee");
    expect(feeWords(x({ fee_quote_kobo: null }))).toBe("The fee shows after paying");
    expect(feeWords(x({ route: "wallet", fee_quote_kobo: 0 }))).toBe("Taskiem wallet · arrives at once, no fee");
  });
  it("works out the balance after, with the fee", () => {
    expect(balanceAfter(x({ balance_kobo: 10_000_000, fee_quote_kobo: 2500 }))).toBe(5_497_500);
    expect(balanceAfter(x({ balance_kobo: 10_000_000 }))).toBe(5_500_000);
    expect(balanceAfter(x({ balance_kobo: null }))).toBeNull();
  });
  it("sends the chat its diary line goes to", () => {
    expect(payBody({ who: "@chidi", amount: "45k", for: "a delivery" }, "chat", "taskiem")).toMatchObject({ where: "taskiem", payee: "@chidi", amount_kobo: 4_500_000 });
    expect(payBody({ who: "@chidi", amount: "45k" }, "tools")).not.toHaveProperty("where");
  });
});

describe("after paying", () => {
  it("turns the diary line to received, or failed with Try again", () => {
    expect(diaryState(x({ status: "paid" }))).toEqual({ words: "Received by the bank", tone: "ok", again: false });
    expect(diaryState(x({ status: "failed" }))).toMatchObject({ words: "Failed: refunded to your balance", again: true });
    expect(diaryState(x({ status: "reversed" })).again).toBe(true);
    expect(diaryState(x({ status: "sending" })).words).toBe("Sent, waiting for the bank");
  });
  it("says a payment the bank sent back", () => {
    expect(paymentStatus("reversed").label).toBe("Came back");
    expect(outcomeWords(x({ status: "reversed", reason: "Account closed" }))).toContain("sent it back: Account closed");
  });
  it("names a beneficiary from the bank's name", () => {
    expect(beneficiaryName("CHIDI OKEKE")).toBe("chidi");
    expect(beneficiaryName("  Ọlá  Bello")).toBe("ọlá");
    expect(beneficiaryName(null)).toBe("");
  });
});

describe("the batch's foot", () => {
  const rows = [x({ id: "a", amount_kobo: 1_000_000 }), x({ id: "b", amount_kobo: 2_000_000 })];
  it("says the fees and the balance after", () => {
    expect(batchMoney({ payments: rows, fees_kobo: 5000, fees_unknown: 0, balance_kobo: 10_000_000, short_kobo: 0 })).toEqual({
      fees: "₦50",
      after: "₦69,950",
      short: null,
    });
  });
  it("says by how much it is short and which rows fit", () => {
    const m = batchMoney({ payments: rows, fees_kobo: 5000, balance_kobo: 2_500_000, short_kobo: 505_000, fit: ["a"] });
    expect(m.after).toBeNull();
    expect(m.short).toBe("Your balance is ₦25,000: ₦5,050 short for all 2 people. The first one fits: take some out, or add money first.");
  });
  it("says when the fees show after paying", () => {
    expect(batchMoney({ payments: rows, fees_kobo: 0, fees_unknown: 2, balance_kobo: null }).fees).toBe("The fees show after paying");
  });
});

describe("Pay it back", () => {
  it("fills in what is owed exactly, its kobo kept", () => {
    expect(nairaTyped(500_000)).toBe("5000");
    expect(nairaTyped(500_050)).toBe("5000.50");
    expect(nairaTyped(500_005)).toBe("5000.05");
    expect(nairaTyped(99)).toBe("0.99");
  });
});
