import { describe, expect, it } from "vitest";
import { countLine, nairaKobo, nairaText, orderLine, priceLine, priceListLine, productLine, rowLine, serviceLine, stockInLine } from "./toolforms";

describe("what the tools say as diary lines", () => {
  it("words each change", () => {
    expect(priceLine({ name: "Rice, 50kg bag", from: 7_600_000, to: 7_800_000 })).toBe("Price of Rice, 50kg bag: ₦76,000 → ₦78,000");
    expect(countLine("Rice", 4, 6, "bag")).toBe("Stock of Rice counted: 4 bags → 6 bags");
    expect(countLine("Rice", null, 1, "bag")).toBe("Stock of Rice counted: no count → 1 bag");
    expect(stockInLine("Rice, 25kg bag", 20, 4, "bag")).toBe("Stock in: Rice, 25kg bag +20 bags (now 24 bags)");
    expect(productLine("Garri", 3_200_000, "bag")).toBe("New product: Garri, ₦32,000 a bag");
    expect(serviceLine("Rice milling", 30, 250_000)).toBe("New service: Rice milling, 30 min, ₦2,500");
    expect(serviceLine("Braids", 120, null)).toBe("New service: Braids, 120 min, price on the day");
    expect(orderLine("accept", "AD-1", "Rivers Foods")).toBe("Accepted order AD-1 from Rivers Foods");
    expect(rowLine("Customers")).toBe("Added a row to Customers");
  });
  it("says a price list's first change and how many more", () => {
    expect(priceListLine("Wholesale", [])).toBe("Price list Wholesale saved");
    expect(
      priceListLine("Wholesale", [
        { name: "Rice", from: 4_000_000, to: 4_100_000 },
        { name: "Beans", from: 1, to: 2 },
      ]),
    ).toBe("Price list Wholesale: Rice ₦40,000 → ₦41,000, and 1 more");
  });
  it("reads and writes naira", () => {
    expect(nairaKobo("78,000")).toBe(7_800_000);
    expect(nairaKobo("₦1500.5")).toBe(150_050);
    expect(nairaKobo("abc")).toBeNaN();
    expect(nairaKobo("")).toBeNaN();
    expect(nairaText(7_800_000)).toBe("78,000");
    expect(nairaText(150_050)).toBe("1,500.50");
  });
});
