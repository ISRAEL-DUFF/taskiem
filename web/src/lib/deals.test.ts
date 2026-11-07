import { describe, expect, it } from "vitest";
import {
  breakWords,
  breaksProblem,
  dealKobo,
  dealLine,
  lapseWords,
  offWords,
  priceAt,
  stepWords,
} from "./deals";

const rice = {
  price_kobo: 4_100_000,
  unit: "bag",
  breaks: [
    { min_quantity: 10, price_kobo: 3_950_000 },
    { min_quantity: 50, price_kobo: 3_880_000 },
  ],
};

describe("deals", () => {
  it("prices a quantity at the last break reached", () => {
    expect(priceAt(rice, 5)).toBe(4_100_000);
    expect(priceAt(rice, 10)).toBe(3_950_000);
    expect(priceAt(rice, 49)).toBe(3_950_000);
    expect(priceAt(rice, 50)).toBe(3_880_000);
    expect(priceAt({ price_kobo: 100 }, 99)).toBe(100);
  });

  it("says the breaks as a buyer reads them", () => {
    expect(breakWords(rice)).toBe("from 10 bags ₦39,500 · from 50 ₦38,800");
    expect(breakWords({ unit: "kg" })).toBe("");
  });

  it("checks breaks go up in quantity and down in price", () => {
    expect(breaksProblem(5, 4_100_000, rice.breaks)).toBe("");
    expect(breaksProblem(10, 4_100_000, rice.breaks)).toMatch(/quantity/);
    expect(
      breaksProblem(1, 4_100_000, [{ min_quantity: 10, price_kobo: 4_200_000 }]),
    ).toMatch(/lower/);
    expect(
      breaksProblem(1, 100, Array.from({ length: 6 }, (_, i) => ({ min_quantity: i + 2, price_kobo: 90 - i }))),
    ).toMatch(/Up to 5/);
  });

  it("reads naira as people type them in a deal", () => {
    expect(dealKobo("760k")).toBe(76_000_000);
    expect(dealKobo("₦760,000")).toBe(76_000_000);
    expect(dealKobo("1.2m")).toBe(120_000_000);
    expect(dealKobo("N 5,000.50")).toBe(500_050);
    expect(dealKobo("lots")).toBeNaN();
    expect(dealKobo("")).toBeNaN();
  });

  it("says a deal against what it was", () => {
    expect(offWords(74_000_000, 79_000_000)).toBe("6% off");
    expect(offWords(80_000_000, 79_000_000)).toBe("");
    expect(dealLine(74_000_000, 79_000_000)).toBe(
      "₦740,000 · was ₦790,000 · 6% off",
    );
    expect(dealLine(74_000_000)).toBe("₦740,000");
  });

  it("tells the history and when it lapses", () => {
    const at = "2026-11-07T10:00:00Z";
    expect(stepWords({ by: "buyer", total_kobo: 74_000_000, at }, 0, "seller")).toBe(
      "They offered ₦740,000",
    );
    expect(
      stepWords({ by: "seller", who: "Tunde", total_kobo: 77_000_000, at }, 1, "seller"),
    ).toBe("Tunde countered ₦770,000");
    expect(stepWords({ by: "buyer", total_kobo: 76_000_000, at }, 2, "buyer")).toBe(
      "You countered ₦760,000",
    );
    const now = Date.parse(at);
    expect(lapseWords("2026-11-09T09:30:00Z", now)).toBe("Lapses in 47 hours");
    expect(lapseWords("2026-11-07T10:20:00Z", now)).toBe("Lapses in 20 minutes");
    expect(lapseWords("2026-11-07T09:00:00Z", now)).toBe("Lapsed");
    expect(lapseWords(undefined, now)).toBe("");
  });
});
