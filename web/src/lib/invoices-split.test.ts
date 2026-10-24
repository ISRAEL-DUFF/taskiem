import { describe, expect, it } from "vitest";
import {
  creditWords,
  groupSuggestions,
  invoiceCustomerPath,
  leftWords,
  payerPath,
  payerWords,
  sentLines,
  splitLine,
  splitProblem,
  splitRequest,
  splitRows,
  splitTotal,
  unmatchedPayments,
  type MoneySuggestion,
} from "./invoices";

// Splits, customers' credit and remembered payers (B1; docs/matching.md).
describe("splitting a payment", () => {
  const proposal = {
    parts: [
      { invoice: "INV-0001", number: "INV-0001", amount_kobo: 2000000, due_kobo: 2000000 },
      { invoice: "INV-0002", number: "INV-0002", amount_kobo: 3000000, due_kobo: 3000000 },
    ],
    credit_kobo: 1000000,
  };

  it("offers the server's split as editable rows", () => {
    const rows = splitRows(proposal);
    expect(rows).toEqual([
      { invoice: "INV-0001", due_kobo: 2000000, amount: "20000" },
      { invoice: "INV-0002", due_kobo: 3000000, amount: "30000" },
    ]);
    expect(splitTotal(rows)).toBe(5000000);
    expect(splitRows(undefined)).toEqual([]);
  });

  it("refuses parts that are not amounts, nothing, or more than was received", () => {
    const rows = splitRows(proposal);
    const first = { invoice: "INV-0001", due_kobo: 2000000, amount: "20000" };
    expect(splitProblem(rows, 6000000)).toBe("");
    expect(splitProblem([{ ...first, amount: "lots" }], 6000000)).toMatch(/as naira/);
    expect(splitProblem([{ ...first, amount: "" }], 6000000)).toMatch(/at least one/);
    expect(splitProblem([{ ...first, amount: "70,000" }], 6000000)).toBe("The parts add up to ₦70,000, more than the ₦60,000 received.");
  });

  it("sends the parts with an amount, and whether the rest is credit", () => {
    const rows = [...splitRows(proposal), { invoice: "INV-0003", due_kobo: 100, amount: "" }];
    rows[1] = { invoice: "INV-0002", due_kobo: 3000000, amount: "25,000.50" };
    expect(splitRequest(rows, true)).toEqual({
      parts: [
        { invoice: "INV-0001", amount_kobo: 2000000 },
        { invoice: "INV-0002", amount_kobo: 2500050 },
      ],
      credit_rest: true,
    });
  });

  it("says what a split did", () => {
    expect(splitLine({ parts: [{ target: "invoice:INV-0001", amount_kobo: 2000000 }, { target: "order:AK-1", amount_kobo: 300000 }], left_kobo: 0, credit_kobo: 100000 })).toBe(
      "Matched in parts: INV-0001 ₦20,000, order AK-1 ₦3,000. ₦1,000 is the customer's credit, counted towards their next invoice.",
    );
    expect(splitLine({ parts: [{ target: "invoice:INV-0001", amount_kobo: 2000000 }], left_kobo: 50000, credit_kobo: 0 })).toBe(
      "Matched in parts: INV-0001 ₦20,000. ₦500 is left to match.",
    );
  });

  it("keeps what is left and why money is held on the cards", () => {
    const s = { id: "s1", entry_id: "e1", amount_kobo: 6000000, left_kobo: 4000000, channel: "paystack/main", reference: "R", received_at: "", kind: "payment" } as MoneySuggestion;
    const [g] = groupSuggestions([s]);
    if (!g) throw new Error("no card");
    expect(g.left_kobo).toBe(4000000);
    expect(leftWords(g)).toBe("₦40,000 of it is left to match; the rest counts towards other invoices or orders.");
    expect(leftWords({ amount_kobo: 100, left_kobo: 100 })).toBe("");
    expect(leftWords({ amount_kobo: null, left_kobo: null })).toBe("");
    const [u] = unmatchedPayments([{ entry_id: "e2", amount_kobo: 100, channel: "c", reference: "r", received_at: "", kind: "account_credit", held_reason: "paid into another account number" }]);
    if (!u) throw new Error("no card");
    expect(u.held_reason).toBe("paid into another account number");
  });
});

describe("the customer's money", () => {
  it("words credit, payers and paths", () => {
    expect(creditWords(0)).toBe("None");
    expect(creditWords(150000)).toMatch(/^₦1,500: paid and not yet counted/);
    expect(payerWords({ id: "p", kind: "name", payer: "O•••", times: 1, last_at: "" })).toBe("O••• (name), paid once");
    expect(payerWords({ id: "p", kind: "account", payer: "•••6789", times: 3, last_at: "" })).toBe("•••6789 (paying account), paid 3 times");
    expect(invoiceCustomerPath("INV/1")).toBe("/v1/invoices/INV%2F1/customer");
    expect(payerPath("a b")).toBe("/v1/money/payers/a%20b");
    expect(sentLines({ queued: ["sms"], credit_kobo: 100000 })).toEqual(["Sent by SMS.", "₦1,000 of the customer's credit counts towards it."]);
  });
});
