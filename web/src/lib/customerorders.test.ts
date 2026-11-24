import { describe, expect, it } from "vitest";
import {
  lastConfirmed,
  lineKobo,
  lineNote,
  lineWords,
  orderPill,
  payChoices,
  payNow,
  workingOn,
  type OrderDraft,
} from "./customerorders";

const draft = (o: Partial<OrderDraft>): OrderDraft => ({
  id: "d",
  chat_id: "c",
  status: "open",
  lines: [],
  delivery_kobo: 0,
  items_kobo: 0,
  total_kobo: 0,
  questions: 0,
  ready: false,
  version: 1,
  by_reader: true,
  created_at: "2026-11-09T10:00:00Z",
  updated_at: "2026-11-09T10:00:00Z",
  ...o,
});

describe("customer orders", () => {
  it("finds the one being worked on, and the last confirmed", () => {
    const list = [
      draft({ id: "a", status: "confirmed" }),
      draft({ id: "b", status: "dropped" }),
      draft({ id: "c", status: "confirmed" }),
      draft({ id: "d", status: "asked" }),
    ];
    expect(workingOn(list)?.id).toBe("d");
    expect(lastConfirmed(list)?.id).toBe("c");
    expect(workingOn([])).toBeUndefined();
  });

  it("says where an order stands", () => {
    expect(orderPill(draft({ questions: 2 }))).toEqual({
      word: "2 questions",
      tone: "warn",
    });
    expect(orderPill(draft({ ready: true })).word).toBe("Ready to confirm");
    expect(orderPill(draft({ status: "asked" })).word).toBe("Waiting for them");
    expect(
      orderPill(
        draft({ status: "confirmed", order_status: "awaiting_payment" }),
      ).word,
    ).toBe("Waiting for payment");
    expect(
      orderPill(draft({ status: "confirmed", order_status: "on_the_way" }))
        .word,
    ).toBe("On the way");
    expect(
      orderPill(draft({ status: "confirmed", order_status: "expired" })),
    ).toEqual({ word: "Not paid in time", tone: "danger" });
  });

  it("offers the ways to pay this order can take", () => {
    expect(payChoices({ fulfilment: "delivery" }, true)).toEqual([
      "transfer",
      "on_delivery",
      "later",
    ]);
    expect(payChoices({ fulfilment: "pickup" }, false)).toEqual(["later"]);
    // A choice no longer offered falls back to the first.
    expect(payNow({ fulfilment: "pickup", pay: "on_delivery" }, true)).toBe(
      "transfer",
    );
    expect(payNow({ fulfilment: "delivery", pay: "on_delivery" }, true)).toBe(
      "on_delivery",
    );
  });

  it("words a line", () => {
    expect(
      lineWords({ item_id: "1", name: "Jollof rice plate", quantity: 2 }),
    ).toBe("2 × Jollof rice plate");
    expect(lineWords({ said: "1 shawarma" })).toBe("“1 shawarma”");
    expect(lineKobo({ item_id: "1", quantity: 2, price_kobo: 450000 })).toBe(
      900000,
    );
    expect(lineKobo({ item_id: "1", quantity: 2, no_price: true })).toBe(
      undefined,
    );
    expect(lineNote({ said: "1 shawarma" })).toMatch(/Not in your catalogue/);
    expect(lineNote({ item_id: "1", out: true, left: 3 })).toBe("Only 3 left.");
    expect(lineNote({ item_id: "1", out: true, left: 0 })).toBe(
      "Out of stock.",
    );
  });
});
