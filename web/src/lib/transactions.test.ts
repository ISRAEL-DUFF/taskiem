import { describe, expect, it } from "vitest";
import { naira } from "./invoices";
import {
  byDay,
  countPath,
  countTitle,
  narrowed,
  otherLine,
  pendingLine,
  totalsHero,
  transactionsLink,
  txAmount,
  txEntryPath,
  txFilter,
  txHero,
  txParams,
  txPath,
  txPeriodWords,
  txProblem,
  txStatus,
  txSub,
  shownReference,
  txTitle,
  whereWords,
  type Movement,
  type MovementDetail,
  type MovementTotals,
} from "./transactions";

// 25 Nov 2026, 10:00 in Lagos.
const NOW = new Date("2026-11-25T09:00:00Z");

const m = (x: Partial<Movement> = {}): Movement => ({
  id: "e1",
  movement: "e1",
  kind: "payment",
  direction: "in",
  amount_kobo: 2500000,
  status: "settled",
  channel: "paystack/main",
  reference: "P-1",
  when: "2026-11-25T08:00:00Z",
  to_match: false,
  ...x,
});

const totals = (x: Partial<MovementTotals> = {}): MovementTotals => ({
  count: 3,
  in_settled_kobo: 2500000,
  in_pending_kobo: 0,
  out_settled_kobo: 400000,
  out_pending_kobo: 0,
  fees_kobo: 37500,
  other_currencies: 0,
  ...x,
});

describe("the filters in the address", () => {
  it("is this month and nothing else unless it says", () => {
    const f = txFilter(new URLSearchParams());
    expect(f).toEqual({ period: "month", from: "", to: "", direction: "", channel: "", matched: "", q: "" });
    expect(txParams(f)).toEqual({});
    expect(narrowed(f)).toBe(false);
  });
  it("reads and writes back what it knows, and drops what it does not", () => {
    const p = new URLSearchParams("period=custom&from=2026-10-01&to=2026-10-31&direction=out&channel=cash&matched=no&q=diesel");
    const f = txFilter(p);
    expect(txParams(f)).toEqual(Object.fromEntries(p));
    expect(narrowed(f)).toBe(true);
    expect(txFilter(new URLSearchParams("period=forever&direction=up&matched=maybe"))).toMatchObject({ period: "month", direction: "", matched: "" });
    // Dates are kept only for chosen dates.
    expect(txParams({ ...f, period: "week" })).not.toHaveProperty("from");
  });
  it("asks the server for the period, the filters and the next page", () => {
    const f = txFilter(new URLSearchParams("direction=in&channel=paystack/main&matched=yes&q= 4,500 "));
    const q = new URLSearchParams(txPath(f, "c1", NOW).split("?")[1]);
    expect(Object.fromEntries(q)).toEqual({
      movements: "true",
      from: "2026-11-01",
      to: "2026-11-25",
      direction: "in",
      channel: "paystack/main",
      matched: "yes",
      q: "4,500",
      cursor: "c1",
    });
    expect(txPath(txFilter(new URLSearchParams("period=all")), undefined, NOW)).toBe("/v1/money/entries?movements=true");
    expect(txEntryPath("e/1")).toBe("/v1/money/entries/e%2F1");
    expect(txEntryPath("e1", true)).toBe("/v1/money/entries/e1?reveal=true");
  });
  it("says the period, and what is wrong with chosen dates", () => {
    expect(txPeriodWords(txFilter(new URLSearchParams()), NOW)).toBe("This month");
    expect(txPeriodWords(txFilter(new URLSearchParams("period=all")), NOW)).toBe("Everything");
    expect(txPeriodWords(txFilter(new URLSearchParams("period=custom&from=2026-10-01&to=2026-10-31")), NOW)).toBe("1 Oct 2026 to 31 Oct 2026");
    expect(txProblem(txFilter(new URLSearchParams("period=custom&from=2026-10-31&to=2026-10-01")), NOW)).toBe("The last day is on or after the first.");
    expect(txProblem(txFilter(new URLSearchParams("period=week")), NOW)).toBeNull();
  });
  it("takes Money's period to Transactions", () => {
    expect(transactionsLink("month", { from: "2026-11-01", to: "2026-11-25" })).toBe("/money/transactions");
    expect(transactionsLink("today", { from: "2026-11-25", to: "2026-11-25" })).toBe("/money/transactions?period=today");
    expect(transactionsLink("yesterday", { from: "2026-11-24", to: "2026-11-24" })).toBe("/money/transactions?period=custom&from=2026-11-24&to=2026-11-24");
  });
});

describe("a row's words", () => {
  it("names it by what was typed, else its kind", () => {
    expect(txTitle(m())).toBe("Payment");
    expect(txTitle(m({ kind: "expense", vendor: "Total" }))).toBe("Total");
    expect(txTitle(m({ kind: "cash", description: "Cash from Bisi" }))).toBe("Cash from Bisi");
  });
  it("signs the amount by which way it went", () => {
    expect(txAmount(m())).toBe(`+${naira(2500000)}`);
    expect(txAmount(m({ direction: "out", amount_kobo: 400000 }))).toBe(`−${naira(400000)}`);
    expect(txAmount(m({ amount_kobo: undefined }))).toBe("No amount");
    expect(txAmount(m({ currency: "USD", amount_kobo: 5000 }))).toBe("+USD 50.00");
  });
  it("says where, the reference and what it counts towards", () => {
    expect(txSub(m({ towards: "invoice INV-0104" }))).toBe("Paystack (main) · P-1 · invoice INV-0104");
    expect(txSub(m({ kind: "cash", description: "Cash from Bisi", channel: "cash", reference: "Cash from Bisi" }))).toBe("Cash");
    // Taskiem's own references never show; a payments account's do.
    expect(txSub(m({ kind: "cash", description: "Sales", channel: "till-1", reference: "entered:87a17496-5fdf-419f-af00-6b819d42a402" }))).not.toContain("entered:");
    for (const r of ["entered:1", "sale:k", "posting:p", "chat:n", "tnt:x", "purchase:y", "thing-sale:z", "withdraw:w", "delivery-fee:d"]) expect(shownReference(r)).toBe("");
    for (const r of ["P-1", "T1234567890", "TRF-0099"]) expect(shownReference(r)).toBe(r);
    expect(shownReference(undefined)).toBe("");
    expect(whereWords("iswallet/taskiem")).toBe("Taskiem wallet");
    expect(whereWords(undefined)).toBe("");
  });
  it("gives each status its word and tone", () => {
    expect(txStatus(m({ status: "failed" }))).toEqual({ label: "Failed", tone: "danger" });
    expect(txStatus(m({ status: "reversed" }))).toEqual({ label: "Reversed", tone: "muted" });
    expect(txStatus(m({ status: "pending" }))).toEqual({ label: "Pending", tone: "info" });
    expect(txStatus(m({ to_match: true }))).toEqual({ label: "To match", tone: "warn" });
    expect(txStatus(m({ towards: "invoice INV-1" }))).toEqual({ label: "Matched", tone: "ok" });
    expect(txStatus(m())).toEqual({ label: "Not matched", tone: "muted" });
    expect(txStatus(m({ direction: "out" }))).toEqual({ label: "Paid", tone: "ok" });
  });
  it("groups rows by their Lagos day", () => {
    const g = byDay([m({ when: "2026-11-25T08:00:00Z" }), m({ when: "2026-11-24T23:30:00Z" }), m({ when: "2026-11-24T20:00:00Z" }), m({ when: "2026-11-01T10:00:00Z" })], NOW);
    expect(g.map((x) => [x.day, x.rows.length])).toEqual([
      ["Today", 2], // 00:30 on the 25th in Lagos
      ["Yesterday", 1],
      ["1 Nov 2026", 1],
    ]);
  });
});

describe("the totals", () => {
  it("leads with money in, or money out when only that is shown", () => {
    const f = txFilter(new URLSearchParams());
    expect(totalsHero(totals(), f)).toEqual({ label: "Money in", amount: naira(2500000), sub: `${naira(400000)} out · ${naira(37500)} in fees · 3 transactions` });
    expect(totalsHero(totals({ count: 1, fees_kobo: 0 }), { ...f, direction: "out" })).toEqual({ label: "Money out", amount: naira(400000), sub: "1 transaction" });
  });
  it("says what is pending and what is in another currency", () => {
    expect(pendingLine(totals())).toBe("");
    expect(pendingLine(totals({ in_pending_kobo: 100000, out_pending_kobo: 5000 }))).toBe(`${naira(100000)} in and ${naira(5000)} out still pending`);
    expect(otherLine(totals({ other_currencies: 2 }))).toBe("2 in other currencies are not in these sums.");
    expect(otherLine(totals())).toBe("");
  });
});

describe("one transaction", () => {
  const d: MovementDetail = { ...m({ fee_kobo: 37500 }), recorded_at: "2026-11-25T08:00:01Z", source: "event", history: [], counts: [] };
  it("leads with its amount and status", () => {
    expect(txHero(d)).toEqual({ label: "Money in", amount: `+${naira(2500000)}`, tag: "Not matched", sub: "Payment · Paystack (main)" });
  });
  it("links what it paid for", () => {
    expect(countPath({ what: "invoice", id: "i", number: "INV-0104", amount_kobo: 1 })).toBe("/invoices/INV-0104");
    expect(countPath({ what: "order", id: "o", number: "AK-7QX4KD", amount_kobo: 1 })).toBe("/storefront/orders/AK-7QX4KD");
    expect(countPath({ what: "sale", id: "s", number: "S-7QX4KD", amount_kobo: 1 })).toBeNull();
    expect(countTitle({ what: "sale", id: "s", number: "S-7QX4KD", amount_kobo: 1 })).toBe("Sale S-7QX4KD");
  });
});
