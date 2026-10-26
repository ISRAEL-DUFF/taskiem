import { describe, expect, it } from "vitest";
import {
  creditWords,
  groupSuggestions,
  handProblem,
  handRequest,
  handRest,
  handRestWords,
  handRows,
  invoiceCustomerPath,
  leftWords,
  matchChoices,
  payerPath,
  payerWords,
  sentLines,
  splitHint,
  splitLine,
  splitProblem,
  splitRequest,
  splitRowLabel,
  splitRows,
  splitTotal,
  unmatchedPayments,
  type CustomerInvoice,
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

  it("offers an invoice given nothing with an empty amount, marks a sibling's, and says why it is offered", () => {
    const several = { parts: [{ number: "INV-0002", amount_kobo: 3000000, due_kobo: 3000000 }, { number: "INV-0001", amount_kobo: 0, due_kobo: 2000000 }, { number: "INV-0003", amount_kobo: 0, due_kobo: 100000, sibling: true }], credit_kobo: 0, over: false };
    const rows = splitRows(several);
    expect(rows.map((r) => r.amount)).toEqual(["30000", "", ""]);
    expect(rows.map(splitRowLabel)).toEqual(["INV-0002 (₦30,000 due)", "INV-0001 (₦20,000 due)", "INV-0003 (₦1,000 due, a sibling's)"]);
    expect(splitTotal(rows)).toBe(3000000);
    expect(splitHint(several, "INV-0002")).toMatch(/^INV-0002 is offered all of it\. If this payment is for several of the family's open invoices/);
    expect(splitHint({ ...proposal, over: true }, "INV-0002")).toBe(
      "The payment is more than is due on INV-0002. Offered across the same customer's open invoices, INV-0002 first then oldest first; change the amounts if you know better.",
    );
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

describe("splitting by hand", () => {
  const invoices = [
    { id: "i1", number: "INV-0001", status: "sent", due_kobo: 2500000, customer: { name: "Gozie" } },
    { id: "i2", number: "INV-0002", status: "overdue", due_kobo: 1000000, customer: { name: "Gozie" } },
  ] as CustomerInvoice[];
  const orders = [{ target_kind: "order" as const, target: "order:AK-1", number: "AK-1", due_kobo: 300000, customer: "B•••", placed_at: "" }];

  it("lists every invoice and order to pick from, none picked, and asks for a pick", () => {
    const rows = handRows(matchChoices(invoices, orders, []));
    expect(rows.map((r) => [r.value, r.picked, r.amount, r.due_kobo])).toEqual([
      ["i1", false, "", 2500000],
      ["i2", false, "", 1000000],
      ["order:AK-1", false, "", 300000],
    ]);
    expect(handProblem(rows, 4000000)).toBe("Tick the invoices or orders this payment is for.");
    expect(handRest(rows, 4000000)).toBe(4000000);
    expect(handRestWords(4000000)).toBe("₦40,000 left to match afterwards.");
    expect(handRestWords(0)).toBe("All of the payment counts towards these.");
  });

  it("wants each picked part as an amount above nothing, and no more than is left", () => {
    const [a, b, o] = handRows(matchChoices(invoices, orders, []));
    if (!a || !b || !o) throw new Error("rows");
    expect(handProblem([{ ...a, picked: true }, b, o], 4000000)).toBe("Give INV-0001 its part as naira, like 20,000.");
    expect(handProblem([a, b, { ...o, picked: true, amount: "lots" }], 4000000)).toBe("Give order AK-1 its part as naira, like 20,000.");
    expect(handProblem([{ ...a, picked: true, amount: "0" }, b, o], 4000000)).toBe("Give INV-0001 its part as naira, like 20,000.");
    const rows = [{ ...a, picked: true, amount: "25,000" }, { ...b, picked: true, amount: "10000" }, { ...o, picked: false, amount: "99999" }];
    expect(handProblem(rows, 3000000)).toBe("The parts add up to ₦35,000, more than the ₦30,000 left of this payment.");
    expect(handProblem(rows, 4000000)).toBe("");
    expect(handRest(rows, 4000000)).toBe(500000);
    expect(handRequest(rows)).toEqual({ parts: [{ invoice: "i1", amount_kobo: 2500000 }, { invoice: "i2", amount_kobo: 1000000 }] });
    expect(handRequest([a, b, { ...o, picked: true, amount: "3000" }])).toEqual({ parts: [{ order: "AK-1", amount_kobo: 300000 }] });
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
