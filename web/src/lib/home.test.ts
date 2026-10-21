import { describe, expect, it } from "vitest";
import { moneyToday, ordersToday, runWords, startedByWords, triggerWords } from "./home";

const now = new Date(2026, 9, 21, 12, 0);
const at = (h: number, day = 21) => new Date(2026, 9, day, h).toISOString();

describe("moneyToday", () => {
  it("adds up what was paid today, not refunds or yesterday", () => {
    const orders = [
      { paid_at: at(9), total_kobo: 850_000, status: "paid" as const },
      { paid_at: at(10), total_kobo: 1_000_000, status: "completed" as const },
      { paid_at: at(11), total_kobo: 500_000, status: "refunded" as const },
      { paid_at: at(20, 20), total_kobo: 700_000, status: "paid" as const },
      { paid_at: null, total_kobo: 300_000, status: "received" as const },
    ];
    expect(moneyToday(orders, now)).toEqual({ kobo: 1_850_000, count: 2 });
  });
});

describe("ordersToday", () => {
  it("counts today's orders and every one waiting for the owner", () => {
    const orders = [
      { created_at: at(9), status: "received" as const },
      { created_at: at(10), status: "completed" as const },
      { created_at: at(9, 20), status: "refund_due" as const },
    ];
    expect(ordersToday(orders, now)).toEqual({ count: 2, waiting: 2 });
  });
});

describe("triggerWords", () => {
  it("says what starts a workflow", () => {
    expect(triggerWords({ type: "schedule", config: { cron: "0 8 * * *" } })).toBe("Every day at 08:00");
    expect(triggerWords({ type: "storefront" })).toBe("An order comes in");
    expect(triggerWords({ type: "connector_event", config: { connector: "paystack@1" } })).toBe("Paystack tells it something happened");
    expect(triggerWords({ type: "manual" })).toBe("When someone starts it");
    expect(triggerWords(null)).toBe("Not set up yet");
  });
  it("falls back for a schedule it cannot read", () => {
    expect(triggerWords({ type: "schedule", config: { cron: "*/7 3-5 * * *" } })).toBe("On a schedule");
  });
});

describe("runWords", () => {
  it("ends a sentence about a run", () => {
    expect(runWords("completed")).toBe("finished");
    expect(runWords("needs_reconciliation")).toBe("needs checking");
  });
});

describe("startedByWords", () => {
  const names = new Map([["u1", "Ada Obi"]]);
  it("names the member, else says what started it", () => {
    expect(startedByWords("u1", "manual", names)).toBe("Ada Obi");
    expect(startedByWords("k9", "manual", names)).toBe("an app or API key");
    expect(startedByWords(null, "schedule", names)).toBe("its schedule");
    expect(startedByWords(null, "storefront", names)).toBe("an order");
    expect(startedByWords(null, undefined, names)).toBe("Taskiem");
  });
});
