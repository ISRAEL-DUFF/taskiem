import { describe, expect, it } from "vitest";
import { quantitySteps, shopItemOf, shortWords, termsOf } from "./businessshop";
import type { PriceItem } from "./pricelists";

const rice: PriceItem = {
  item_id: "rice",
  name: "Ofada rice",
  unit: "bag",
  category: "Grains",
  price_kobo: 4500000,
  min_quantity: 2,
  available: true,
  breaks: [
    { min_quantity: 10, price_kobo: 4000000 },
    { min_quantity: 50, price_kobo: 3880000 },
  ],
  photo: { "320": "x" },
};

const shoe: PriceItem = {
  item_id: "shoe",
  name: "Canvas sneaker",
  unit: "pair",
  price_kobo: 800000,
  min_quantity: 6,
  available: true,
  breaks: [{ min_quantity: 50, price_kobo: 700000 }],
  choices: [{ name: "Size", values: ["42", "43"] }],
  sizes: [
    { item_id: "s42", name: "Canvas sneaker – 42", variant: { Size: "42" }, available: true },
    { item_id: "s43", name: "Canvas sneaker – 43", variant: { Size: "43" }, price_kobo: 900000, available: false },
  ],
};

describe("a supplier's items in the shop", () => {
  it("draws an item: its photo through the link, its sizes at their own price where they have one", () => {
    expect(shopItemOf(rice, "L 1")).toEqual({
      id: "rice",
      name: "Ofada rice",
      description: undefined,
      category: "Grains",
      price_kobo: 4500000,
      photo: "/v1/link-chats/L%201/photo?item=rice&w=640",
      available: true,
      sizes: undefined,
    });
    expect(shopItemOf(shoe, "L1").sizes).toEqual([
      { id: "s42", variant: { Size: "42" }, price_kobo: undefined, available: true },
      { id: "s43", variant: { Size: "43" }, price_kobo: 900000, available: false },
    ]);
    // Sold in sizes with none on sale: nothing to order.
    expect(shopItemOf({ ...shoe, sizes: [] }, "L1").available).toBe(false);
  });

  it("says its quantity prices from its minimum up, and which one the bag reaches", () => {
    const r = quantitySteps(termsOf(rice), rice.price_kobo, 10);
    expect(r.steps).toEqual([
      { words: "2 to 9 bags", price: 4500000 },
      { words: "10 to 49 bags", price: 4000000 },
      { words: "50 bags and more", price: 3880000 },
    ]);
    expect(r.reached).toBe(1);
    expect(quantitySteps(termsOf(rice), rice.price_kobo, 0).reached).toBe(-1);
    // A break at or under the minimum is the first price.
    expect(quantitySteps({ unit: "", min: 10, breaks: [{ min_quantity: 5, price_kobo: 90 }], sized: false }, 100, 10).steps).toEqual([{ words: "10 and more", price: 90 }]);
  });

  it("stops a product's sizes under its minimum together", () => {
    expect(shortWords([shoe], { s42: 3 })).toBe("Canvas sneaker: at least 6 in all, its sizes together.");
    expect(shortWords([shoe], { s42: 6 })).toBeUndefined();
    expect(termsOf(shoe).sized).toBe(true);
  });
});
