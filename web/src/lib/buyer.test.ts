import { describe, expect, it } from "vitest";
import { bagOf, priceLine, purchaseState, quoteText, reorderQty, stepQty } from "./buyer";
import type { PriceItem } from "./pricelists";

const item = (id: string, name: string, price: number, min = 1, available = true): PriceItem => ({
  item_id: id,
  name,
  unit: "bag",
  price_kobo: price,
  min_quantity: min,
  available,
});

describe("the buyer's tools", () => {
  it("steps quantities to the minimum and back off", () => {
    expect(stepQty(0, 5, 1)).toBe(5);
    expect(stepQty(5, 5, 1)).toBe(6);
    expect(stepQty(5, 5, -1)).toBe(0);
    expect(stepQty(7, 5, -1)).toBe(6);
    expect(stepQty(0, 0, 1)).toBe(1);
  });
  it("totals the bag", () => {
    const items = [item("a", "Rice, 50kg bag", 7_800_000, 2), item("b", "Rice, 25kg bag", 4_100_000, 5)];
    expect(bagOf(items, {})).toMatchObject({ total: 0, count: "Nothing yet" });
    const b = bagOf(items, { b: 20 });
    expect(b.total).toBe(82_000_000);
    expect(b.count).toBe("1 item in your order");
    expect(quoteText(b.lines)).toBe("Could you quote me for:\n• 20 × Rice, 25kg bag");
    expect(priceLine(items[1] as (typeof items)[number])).toBe("₦41,000 a bag · min 5");
  });
  it("says where an order is, and when a quote waits", () => {
    expect(purchaseState({ status: "sent", seller_status: "proposed" })).toMatchObject({ quote: true, step: 0 });
    expect(purchaseState({ status: "confirmed", seller_status: "dispatched" })).toMatchObject({ word: "On the way", step: 2 });
    expect(purchaseState({ status: "received", seller_status: "delivered" })).toMatchObject({ word: "Received", step: 3 });
    expect(purchaseState({ status: "sent", seller_status: "declined" }).tone).toBe("danger");
  });
  it("reorders what is still on the price list, at least its minimum", () => {
    const items = [item("s1", "Rice, 50kg bag", 1, 2), item("s2", "Beans", 1, 1, false)];
    const links = [{ link: "l", item_id: "own1", seller_item_id: "s1", seller_item: "Rice, 50kg bag" }];
    const last = {
      lines: [
        { item_id: "own1", item: "Rice", quantity: 1, cost_kobo: 1, received: 0 },
        { item_id: "own2", item: "Beans", quantity: 3, cost_kobo: 1, received: 0 },
      ],
    };
    expect(reorderQty(last, links, items)).toEqual({ s1: 2 });
  });
});
