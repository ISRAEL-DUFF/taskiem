import { describe, expect, it } from "vitest";
import { bagOf, flatItems, orderLine, priceLine, purchaseState, quoteText, reorderQty, sizesTaken, stepQty } from "./buyer";
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
    expect(orderLine(items[1] as (typeof items)[number])).toBe("₦41,000 · min 5");
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

describe("a product ordered by its sizes and colours", () => {
  const trainer: PriceItem = {
    item_id: "am",
    name: "Air Max 90",
    unit: "pair",
    price_kobo: 4_000_000,
    min_quantity: 6,
    available: true,
    breaks: [{ min_quantity: 50, price_kobo: 3_600_000 }],
    sizes: [
      { item_id: "s42", name: "Air Max 90 – 42", variant: { Size: "42" }, available: true },
      { item_id: "s46", name: "Air Max 90 – 46", variant: { Size: "46" }, price_kobo: 4_400_000, available: true },
      { item_id: "s47", name: "Air Max 90 – 47", variant: { Size: "47" }, available: false },
    ],
  };
  it("is its sizes: the product's price and breaks, a size's own price without", () => {
    const flat = flatItems([trainer]);
    expect(flat.map((x) => x.item_id)).toEqual(["s42", "s46", "s47"]);
    expect(flat[0]).toMatchObject({ price_kobo: 4_000_000, variant_of: "am", unit: "pair", min_quantity: 1 });
    expect(flat[1]).toMatchObject({ price_kobo: 4_400_000, breaks: undefined });
    expect(flat[2]?.available).toBe(false);
  });
  it("prices its sizes at the product's quantity together", () => {
    const bag = bagOf([trainer], { s42: 40, s46: 10 });
    expect(bag.lines).toHaveLength(2);
    expect(bag.total).toBe(40 * 3_600_000 + 10 * 4_400_000);
    expect(bag.count).toBe("1 item in your order");
    expect(bagOf([trainer], { s42: 30 }).total).toBe(30 * 4_000_000);
  });
  it("says what is taken", () => {
    expect(sizesTaken(trainer, { s42: 30, s46: 6 })).toBe("36 pairs in 2 sizes and colours");
    expect(sizesTaken(trainer, {})).toBe("");
  });
});
