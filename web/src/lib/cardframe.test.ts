import { describe, expect, it } from "vitest";
import { DELIVERY_STEPS, ORDER_STEPS, cardKicker, cardStatus, isDiary, stepsFor } from "./cardframe";

describe("the card frame", () => {
  it("places an order on its four steps", () => {
    expect(cardStatus({ doc: "order", event: "ordered", reference: "P-1" })).toEqual({ word: "New", tone: "lime", step: 0 });
    expect(cardStatus({ doc: "purchase", event: "accept", reference: "P-1" }).step).toBe(1);
    expect(cardStatus({ doc: "order", event: "dispatched", reference: "P-1" }).step).toBe(2);
    expect(cardStatus({ doc: "order", event: "delivered", reference: "P-1" }).step).toBe(3);
    expect(cardStatus({ doc: "order", event: "decline", reference: "P-1" })).toEqual({ word: "Declined", tone: "danger" });
  });
  it("words an invoice, and anything new", () => {
    expect(cardStatus({ doc: "bill", event: "queried", reference: "INV-1" }).tone).toBe("warn");
    expect(cardStatus({ doc: "invoice", event: "some_new", reference: "INV-1" })).toEqual({ word: "some new", tone: "plain" });
    expect(cardKicker({ doc: "bill", event: "bill", reference: "INV-1" })).toBe("Invoice · INV-1");
  });
  it("tells our own changes as diary lines, new documents as cards", () => {
    expect(isDiary({ doc: "order", event: "accept", reference: "P-1" }, true)).toBe(true);
    expect(isDiary({ doc: "order", event: "accept", reference: "P-1" }, false)).toBe(false);
    expect(isDiary({ doc: "order", event: "ordered", reference: "P-1" }, true)).toBe(false);
    expect(isDiary({ doc: "invoice", event: "bill", reference: "INV-1" }, true)).toBe(false);
  });
  it("a deal no one answered in time has lapsed", () => {
    const now = Date.parse("2026-11-07T10:00:00Z");
    const deal = (until: string) => ({ steps: [], until });
    expect(
      cardStatus({ doc: "order", event: "offered", reference: "P-1", deal: deal("2026-11-07T09:00:00Z") }, now),
    ).toEqual({ word: "Lapsed", tone: "plain" });
    expect(
      cardStatus({ doc: "order", event: "offered", reference: "P-1", deal: deal("2026-11-08T09:00:00Z") }, now).word,
    ).toBe("Offer");
    expect(
      cardStatus({ doc: "purchase", event: "change", reference: "P-1", was_kobo: 9, deal: deal("2026-11-07T09:00:00Z") }, now).word,
    ).toBe("Lapsed");
    expect(
      cardStatus({ doc: "order", event: "accept", reference: "P-1", deal: deal("2026-11-07T09:00:00Z") }, now).word,
    ).toBe("Accepted");
  });
  it("places a delivery on its five steps, on either side (chat-riders phase 1)", () => {
    expect(cardStatus({ doc: "delivery", event: "offered", reference: "AK-1" })).toEqual({ word: "Offered", tone: "warn", step: 0 });
    expect(cardStatus({ doc: "dispatch", event: "accepted", reference: "AK-1" }).step).toBe(1);
    expect(cardStatus({ doc: "delivery", event: "picked_up", reference: "AK-1" }).step).toBe(2);
    expect(cardStatus({ doc: "delivery", event: "arrived", reference: "AK-1" })).toEqual({ word: "At the door", tone: "lime", step: 3 });
    expect(cardStatus({ doc: "dispatch", event: "delivered", reference: "AK-1" }).step).toBe(4);
    expect(cardStatus({ doc: "delivery", event: "cant", reference: "AK-1" })).toEqual({ word: "Passed", tone: "danger" });
    expect(cardStatus({ doc: "dispatch", event: "released", reference: "AK-1" }).word).toBe("Taken back");
    // Phase 4: the business's confirm and the fee paid stay on the last step.
    expect(cardStatus({ doc: "dispatch", event: "confirmed", reference: "AK-1" })).toEqual({ word: "Confirmed", tone: "lime", step: 4 });
    expect(cardStatus({ doc: "delivery", event: "paid", reference: "AK-1" })).toEqual({ word: "Fee paid", tone: "lime", step: 4 });
    expect(cardKicker({ doc: "delivery", event: "offered", reference: "AK-1" })).toBe("Delivery · AK-1");
    expect(stepsFor({ doc: "delivery" })).toBe(DELIVERY_STEPS);
    expect(stepsFor({ doc: "order" })).toBe(ORDER_STEPS);
    // The offer is a card on both sides; the rider's own steps are lines on theirs.
    expect(isDiary({ doc: "dispatch", event: "offered", reference: "AK-1" }, true)).toBe(false);
    expect(isDiary({ doc: "delivery", event: "accepted", reference: "AK-1" }, true)).toBe(true);
  });
});
