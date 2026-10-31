import { describe, expect, it } from "vitest";
import { buyerSteps, linesTotal, sameName, sellerAsk, sellerLine, sellerSteps, tradeStatus, type TradeOrder } from "./tradeorders";

const order: TradeOrder = {
  id: "o1",
  order_reference: "P-ABC123",
  order_status: "received",
  link: "l1",
  buyer: "b1",
  buyer_name: "Ada's Kitchen",
  reference: "P-ABC123",
  lines: [{ item: "Rice", unit: "bag", quantity: 10, price_kobo: 4_000_000 }],
  total_kobo: 40_000_000,
  status: "received",
  invoice_at: "delivered",
  auto_accept: false,
  placed_by_seller: false,
  created_at: "2026-10-31T09:00:00Z",
  updated_at: "2026-10-31T09:00:00Z",
};

describe("orders from linked businesses", () => {
  it("says a status in words", () => {
    expect(tradeStatus("received")).toEqual({ label: "New", tone: "warn" });
    expect(tradeStatus("dispatched").label).toBe("On its way");
    expect(tradeStatus("cancelled").tone).toBe("muted");
  });
  it("offers the seller's steps its status allows", () => {
    const s = sellerSteps(order);
    expect([s.accept, s.change, s.decline, s.delivered, s.cancel]).toEqual([true, true, true, false, false]);
    const live = sellerSteps({ ...order, status: "ready", cancel_asked_at: "x" });
    expect([live.accept, live.dispatched, live.delivered, live.cancel, live.keep]).toEqual([false, true, true, true, true]);
    expect(sellerSteps({ ...order, status: "delivered", short_at: "x" }).agree).toBe(true);
    expect(sellerSteps({ ...order, status: "delivered", short_at: "x", short_agreed_at: "y" }).agree).toBe(false);
  });
  it("says what needs the seller", () => {
    expect(sellerAsk(order)).toBe("Accept it, change it, or decline it.");
    expect(sellerAsk({ ...order, status: "accepted", cancel_asked_at: "x" })).toContain("asks to cancel it");
    expect(sellerAsk({ ...order, status: "accepted" })).toBe("");
  });
  it("says the seller's side on the buyer's order", () => {
    const p = { supplier: "Dangote", link_id: "l1" };
    expect(sellerLine({ ...p, seller_status: null })).toBe("On its way to Dangote on Taskiem.");
    expect(sellerLine({ ...p, seller_status: "changed" })).toBe("Dangote asks to change it.");
    expect(sellerLine({ ...p, seller_status: "accepted", cancel_asked_at: "x" })).toBe("You asked Dangote to cancel it.");
    expect(sellerLine({ supplier: "Dangote", seller_status: "accepted" })).toBe("");
  });
  it("offers the buyer's steps", () => {
    const p = { status: "sent" as const, link_id: "l1", seller_status: "changed" as const, change: [] };
    expect(buyerSteps(p)).toEqual({ confirmProposed: false, acceptChange: true, cancel: true, answerDelivery: false });
    expect(buyerSteps({ ...p, status: "confirmed", seller_status: "delivered", change: undefined })).toEqual({
      confirmProposed: false,
      acceptChange: false,
      cancel: false,
      answerDelivery: true,
    });
    expect(buyerSteps({ ...p, status: "drafted", seller_status: "proposed" })).toMatchObject({ confirmProposed: true, cancel: true });
    expect(buyerSteps({ ...p, seller_status: "delivered", short_at: "x" }).answerDelivery).toBe(false);
    expect(buyerSteps({ ...p, link_id: undefined }).cancel).toBe(false);
  });
  it("finds the buyer's item of the same name", () => {
    expect(sameName([{ id: "1", name: "Rice " }, { id: "2", name: "Garri" }], "rice")).toBe("1");
    expect(sameName([{ id: "2", name: "Garri" }], "Rice")).toBe("");
    expect(sellerAsk({ ...order, status: "proposed" })).toContain("waiting for them to confirm it");
    expect(sellerSteps({ ...order, status: "proposed" }).cancel).toBe(true);
  });
  it("adds up lines", () => {
    expect(linesTotal([{ quantity: 2, price_kobo: 500 }, { quantity: 1, price_kobo: 100 }])).toBe(1100);
  });
});
