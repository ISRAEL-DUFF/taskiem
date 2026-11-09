import { describe, expect, it } from "vitest";
import { naira } from "./invoices";
import {
  entryAmount,
  entryName,
  entryPill,
  entrySub,
  entryTags,
  invoicePill,
  invoiceSub,
  invoiceTags,
  matchable,
  owing,
  towardsWords,
  type MoneyEntry,
} from "./chatmoney";

const inv = (o: Partial<Parameters<typeof invoiceSub>[0] & { kind?: "invoice" | "credit" }> = {}) => ({
  customer: { name: "B•••" },
  total_kobo: 4500000,
  due_kobo: 4500000,
  status: "sent" as const,
  ...o,
});

const entry = (o: Partial<MoneyEntry> = {}): MoneyEntry => ({
  id: "e1",
  kind: "cash",
  direction: "in",
  amount_kobo: 450000,
  status: "settled",
  when: "2026-11-09T10:00:00Z",
  to_match: false,
  ...o,
});

describe("invoices in the chat", () => {
  it("says what is owed", () => {
    expect(owing(inv())).toBe(true);
    expect(owing(inv({ status: "paid", due_kobo: 0 }))).toBe(false);
    expect(owing(inv({ status: "draft" }))).toBe(false);
    expect(invoiceSub(inv())).toBe(`B••• · ${naira(4500000)} · to pay`);
    expect(invoiceSub(inv({ status: "part_paid", due_kobo: 2500000 }))).toBe(`B••• · ${naira(4500000)} · ${naira(2500000)} to pay`);
    expect(invoiceSub(inv({ status: "paid", due_kobo: 0 }))).toBe(`B••• · ${naira(4500000)}`);
  });
  it("tags and pills", () => {
    expect(invoiceTags(inv({ status: "overdue" }))).toEqual(["owed", "overdue"]);
    expect(invoiceTags(inv({ status: "draft" }))).toEqual(["draft"]);
    expect(invoicePill(inv({ status: "paid" }))).toEqual({ text: "Paid", tone: "lime" });
    expect(invoicePill(inv({ status: "overdue" })).tone).toBe("warn");
  });
});

describe("the money log in the chat", () => {
  it("names an entry by its words, else its kind", () => {
    expect(entryName(entry({ description: "Cash sales" }))).toBe("Cash sales");
    expect(entryName(entry({ kind: "pos_sale" }))).toBe("Card sale");
    expect(entryName(entry({ kind: "expense", vendor: "Total" }))).toBe("Total");
  });
  it("signs the amount", () => {
    expect(entryAmount(entry())).toBe(`+${naira(450000)}`);
    expect(entryAmount(entry({ direction: "out" }))).toBe(`−${naira(450000)}`);
    expect(entryAmount(entry({ amount_kobo: undefined }))).toBe("No amount");
    expect(entrySub(entry({ description: "Cash sales" }))).toBe(`+${naira(450000)} · Cash · 9 Nov`);
    expect(entrySub(entry({ kind: "payment", channel: "gtbank/current" }))).toBe(`+${naira(450000)} · gtbank/current · 9 Nov`);
  });
  it("says what it counts towards and what waits", () => {
    expect(entryPill(entry({ to_match: true }))).toEqual({ text: "To match", tone: "warn" });
    expect(entryPill(entry({ towards: "invoice INV-0001" }))).toEqual({ text: "Counted", tone: "lime" });
    expect(entryPill(entry({ status: "pending" }))?.text).toBe("Pending");
    expect(entryPill(entry())).toBeUndefined();
    expect(entryTags(entry({ to_match: true }))).toEqual(["in", "match"]);
    expect(towardsWords(entry({ towards: "invoice INV-0001" }))).toBe("Counted towards invoice INV-0001.");
  });
  it("matches money in with something left", () => {
    expect(matchable(entry({ left_kobo: 450000 }))).toBe(true);
    expect(matchable(entry({ left_kobo: 0 }))).toBe(false);
    expect(matchable(entry({ direction: "out" }))).toBe(false);
    expect(matchable(entry({ left_kobo: 100, status: "pending" }))).toBe(false);
  });
});
