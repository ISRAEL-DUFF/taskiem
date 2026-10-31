import { describe, expect, it } from "vitest";
import { lineProblem, priceWords, type PriceItem } from "./pricelists";

const rice: PriceItem = {
  item_id: "r",
  name: "Rice",
  unit: "bag",
  price_kobo: 4500000,
  min_quantity: 5,
  available: true,
};

describe("price lists", () => {
  it("reads a price as a buyer does", () => {
    expect(priceWords(rice)).toBe("₦45,000 a bag · at least 5");
    expect(
      priceWords({
        ...rice,
        unit: undefined,
        min_quantity: 1,
        available: false,
      }),
    ).toBe("₦45,000 · not available now");
  });
  it("says what a line is short of", () => {
    expect(lineProblem(rice, "")).toBe("");
    expect(lineProblem(rice, "10")).toBe("");
    expect(lineProblem(rice, "2")).toBe("at least 5");
    expect(lineProblem(rice, "2.5")).toBe("a whole number");
    expect(lineProblem({ ...rice, available: false }, "10")).toBe(
      "not available now",
    );
  });
});
