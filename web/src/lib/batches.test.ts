import { describe, expect, it } from "vitest";
import {
  MAX_ROWS,
  addRows,
  againIds,
  batchCounts,
  batchName,
  batchTag,
  confirmWords,
  draftTotal,
  footWords,
  heldOnto,
  listTag,
  listWords,
  peopleWords,
  pollAfter,
  preparedIds,
  progressWords,
  resultsBy,
  rowBody,
  rowFromLine,
  rowFromRider,
  rowFromSaved,
  rowFromStaff,
  rowProblem,
  waitingIds,
  type PaymentBatch,
} from "./batches";
import type { Payment } from "./payments";

const pay = (id: string, status: Payment["status"], amount = 1_000_000) => ({ id, status, amount_kobo: amount }) as Payment;

const batch = (payments: Payment[], over: Partial<PaymentBatch> = {}): PaymentBatch => ({
  id: "b1",
  title: "",
  kind: "list",
  status: "sent",
  created_by: "user:ada",
  created_at: "2026-11-21T10:00:00Z",
  confirmed_at: null,
  payments,
  total_kobo: 0,
  count: payments.length,
  paid_kobo: 0,
  waiting: 0,
  failed: 0,
  ...over,
});

describe("filling the list", () => {
  it("makes rows of saved accounts, riders and pasted lines", () => {
    const s = rowFromSaved({ id: "s1", name: "bayo", title: "Bayo Rice", hint: "GTBank ••6789", check: { account_name: "BAYO ADEWALE" } });
    expect(s).toMatchObject({ kind: "saved", who: "BAYO ADEWALE", sub: "@bayo · GTBank ••6789", saved_id: "s1", amount: "" });
    const r = rowFromRider({ rider_id: "r1", rider: "Musa", net_kobo: 450_000, bank: "GTBank ••6789" });
    expect(r).toMatchObject({ kind: "rider", who: "Musa", rider_id: "r1", amount: "4,500", for: "a delivery" });
    const l = rowFromLine({ line: 1, text: "Chidi 0123456789 GTBank 45k", account_number: "0123456789", bank_name: "GTBank", amount_kobo: 4_500_000, name: "Chidi" });
    expect(l).toMatchObject({ kind: "account", who: "Chidi", number: "0123456789", bank: "GTBank", amount: "45,000" });
    const bad = rowFromLine({ line: 2, text: "Tunde 45k", problem: "No account number of 10 digits on this line." });
    expect(bad).toMatchObject({ kind: "account", who: "Line 2", sub: "As pasted: Tunde 45k", problem: "No account number of 10 digits on this line." });
    const saved = rowFromLine({ line: 3, text: "Bayo 0123456789 GTB 10k", account_number: "0123456789", bank_name: "GTBank", amount_kobo: 1_000_000, name: "Bayo", payee: "bayo" });
    expect(saved).toMatchObject({ kind: "saved", payee: "bayo", sub: "@bayo · GTBank", amount: "10,000" });
  });

  it("makes a row of a staff member with an account linked, for wages", () => {
    const acct = { saved_id: "s9", name: "musa", title: "Musa", bank_name: "Access Bank", last4: "6789", account_name: "MUSA BELLO" };
    const r = rowFromStaff({ user_id: "u1", name: "Musa Bello", pay_account: acct });
    expect(r).toMatchObject({ kind: "staff", who: "Musa Bello", sub: "@musa · Access Bank ••6789 · MUSA BELLO", staff_id: "u1", amount: "", for: "wages" });
    expect(rowProblem(r)).toBe("How much?");
    expect(rowBody({ ...r, amount: "20k" })).toEqual({ staff_id: "u1", amount_kobo: 2_000_000, purpose: "wages" });
    // The same person twice is added once; the saved account itself is another row.
    expect(addRows([r], [rowFromStaff({ user_id: "u1", name: "Musa Bello", pay_account: acct })]).rows).toHaveLength(1);
    expect(addRows([r], [rowFromSaved({ id: "s9", name: "musa", title: "", hint: "" })]).rows).toHaveLength(2);
  });

  it("leaves out the same person twice and stops at the limit", () => {
    const a = rowFromSaved({ id: "s1", name: "bayo", title: "", hint: "" });
    const b = rowFromSaved({ id: "s1", name: "bayo", title: "", hint: "" });
    expect(addRows([a], [b]).rows).toHaveLength(1);
    const many = Array.from({ length: MAX_ROWS + 2 }, (_, i) => rowFromRider({ rider_id: `r${i}`, rider: `R${i}`, net_kobo: 100, bank: "x" }));
    const out = addRows([], many);
    expect(out.rows).toHaveLength(MAX_ROWS);
    expect(out.left).toBe(2);
  });

  it("says what a row lacks", () => {
    const l = rowFromLine({ line: 1, text: "x", account_number: "123", bank_name: "" });
    expect(rowProblem(l)).toBe("Type the account number: 10 digits.");
    expect(rowProblem({ ...l, number: "0123456789" })).toBe("Which bank is it with?");
    expect(rowProblem({ ...l, number: "0123456789", bank: "GTBank" })).toBe("How much?");
    expect(rowProblem({ ...l, number: "0123456789", bank: "GTBank", amount: "lots" })).toBe("Type an amount, like 45,000 or 45k.");
    expect(rowProblem({ ...l, number: "0123456789", bank: "GTBank", amount: "45k" })).toBe("What is it for?");
    expect(rowProblem({ ...l, number: "0123456789", bank: "GTBank", amount: "45k", for: "wages" })).toBeNull();
  });

  it("sends each row as a payment's body", () => {
    expect(rowBody({ ...rowFromSaved({ id: "s1", name: "bayo", title: "", hint: "" }), amount: "45k", for: "stock bought" })).toEqual({ saved_id: "s1", amount_kobo: 4_500_000, purpose: "stock" });
    expect(rowBody(rowFromRider({ rider_id: "r1", rider: "Musa", net_kobo: 450_000, bank: "" }))).toEqual({ rider_id: "r1", amount_kobo: 450_000, purpose: "delivery" });
    expect(rowBody({ ...rowFromLine({ line: 1, text: "", account_number: "0123 456 789", bank_name: " GTBank " }), number: "0123 456 789", amount: "1,000", for: "generator fuel" })).toEqual({
      account_number: "0123456789",
      bank_name: "GTBank",
      amount_kobo: 100_000,
      purpose: "other",
      purpose_note: "generator fuel",
    });
    expect(rowBody({ ...rowFromLine({ line: 1, text: "", payee: "bayo" }), amount: "5k", for: "a bill" })).toEqual({ payee: "bayo", amount_kobo: 500_000, purpose: "bill" });
  });

  it("totals the foot", () => {
    const rows = [
      { ...rowFromRider({ rider_id: "a", rider: "A", net_kobo: 450_000, bank: "" }) },
      { ...rowFromRider({ rider_id: "b", rider: "B", net_kobo: 0, bank: "" }), amount: "nope" },
    ];
    expect(draftTotal(rows)).toBe(450_000);
    expect(footWords(rows.length, draftTotal(rows))).toBe("2 people · ₦4,500");
    expect(peopleWords(1)).toBe("1 person");
  });

  it("puts the held rows' problems back on the list", () => {
    const rows = [rowFromRider({ rider_id: "a", rider: "A", net_kobo: 1, bank: "" }), rowFromRider({ rider_id: "b", rider: "B", net_kobo: 1, bank: "" })];
    const back = heldOnto(rows, [{ row: 1, payee: "a rider", code: "no_bank", problem: "No bank account kept." }]);
    expect(back[0]?.problem).toBeUndefined();
    expect(back[1]?.problem).toBe("No bank account kept.");
  });
});

describe("a batch's words", () => {
  it("counts and words how the rows stand", () => {
    const b = batch([pay("1", "paid"), pay("2", "sending"), pay("3", "unknown"), pay("4", "waiting_ok"), pay("5", "failed"), pay("6", "cancelled")]);
    expect(batchCounts(b)).toEqual({ paid: 1, sending: 2, waiting: 1, failed: 1, prepared: 0, cancelled: 1 });
    expect(progressWords(b)).toBe("1 paid · 2 sending · 1 waiting for the owner's OK · 1 not paid");
    expect(progressWords(batch([]))).toBe("Nothing to pay");
  });

  it("tags a batch by its worst news first", () => {
    expect(batchTag(batch([pay("1", "prepared")], { status: "prepared" }))).toEqual({ label: "To confirm", tone: "warn" });
    expect(batchTag(batch([], { status: "preparing" })).label).toBe("Getting ready");
    expect(batchTag(batch([pay("1", "paid"), pay("2", "sending")])).label).toBe("Sending");
    expect(batchTag(batch([pay("1", "paid"), pay("2", "waiting_ok")])).label).toBe("Waiting for an OK");
    expect(batchTag(batch([pay("1", "paid"), pay("2", "failed")]))).toEqual({ label: "Some not paid", tone: "danger" });
    expect(batchTag(batch([pay("1", "failed")])).label).toBe("Not paid");
    expect(batchTag(batch([pay("1", "paid")]))).toEqual({ label: "All paid", tone: "ok" });
    expect(batchTag(batch([pay("1", "cancelled")], { status: "cancelled" })).label).toBe("Cancelled");
  });

  it("reads again soon while money moves, less while rows wait, then stops", () => {
    expect(pollAfter(batch([], { status: "preparing" }))).toBe(2000);
    expect(pollAfter(batch([pay("1", "sending")]))).toBe(3000);
    expect(pollAfter(batch([pay("1", "waiting_ok")]))).toBe(10000);
    expect(pollAfter(batch([pay("1", "paid"), pay("2", "failed")]))).toBeNull();
  });

  it("names a batch", () => {
    expect(batchName(batch([], { title: " Riders, week 45 " }))).toBe("Riders, week 45");
    expect(batchName(batch([], { kind: "riders" }))).toBe("Riders' pay");
    expect(batchName(batch([]))).toBe("A list of people");
  });

  it("words a batch in the list from its counts alone", () => {
    const b = (over: Partial<PaymentBatch>) => batch([], { total_kobo: 2_000_000, ...over });
    expect(listTag(b({ status: "prepared" })).label).toBe("To confirm");
    expect(listTag(b({ waiting: 1 })).label).toBe("Waiting for an OK");
    expect(listTag(b({ failed: 1, paid_kobo: 1_000_000 })).label).toBe("Some not paid");
    expect(listTag(b({ paid_kobo: 2_000_000 })).label).toBe("All paid");
    expect(listTag(b({})).label).toBe("Sending");
    expect(listWords(b({ paid_kobo: 1_000_000, failed: 1 }))).toBe("₦10,000 paid · 1 not paid");
    expect(listWords(b({ status: "prepared" }))).toBe("");
  });

  it("confirms the rows shown, by id, and asks again per row", () => {
    const b = batch([pay("1", "prepared", 4_500_000), pay("2", "prepared", 500_000), pay("3", "waiting_ok")], { status: "prepared" });
    expect(confirmWords(b)).toBe("Pay 2 people ₦50,000");
    expect(preparedIds(b)).toEqual(["1", "2"]);
    expect(waitingIds(b)).toEqual(["3"]);
    const results = [{ payment: "1" }, { payment: "2", code: "pay_again", problem: "Paid in the last day." }];
    expect(againIds(results)).toEqual(["2"]);
    expect(resultsBy(results).get("2")?.problem).toBe("Paid in the last day.");
    expect(resultsBy(results).has("1")).toBe(false);
  });
});
