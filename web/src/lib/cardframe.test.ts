import { describe, expect, it } from "vitest";
import { cardKicker, cardStatus, isDiary } from "./cardframe";

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
});
