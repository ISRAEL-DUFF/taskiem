import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import {
  areaName,
  areaOffOf,
  dearerEach,
  newerVersionOf,
  packageLabel,
  readPackages,
  startingRows,
} from "./sell";

describe("forms that sell", () => {
  it("reads packages typed in naira, with labels only when given", () => {
    const got = readPackages([
      { qty: "1", price: "25,000", label: "1 item + free delivery" },
      { qty: "2", price: "45000", label: "" },
      { qty: "3", price: "₦65,000.50", label: " " },
    ]);
    expect(got.problem).toBe("");
    expect(got.packages).toEqual([
      { qty: 1, price_kobo: 2500000, label: "1 item + free delivery" },
      { qty: 2, price_kobo: 4500000 },
      { qty: 3, price_kobo: 6500050 },
    ]);
  });

  it("says what is wrong, in plain words", () => {
    const row = (qty: string, price = "100", label = "") => ({
      qty,
      price,
      label,
    });
    expect(readPackages([]).problem).toMatch(/at least one/);
    expect(
      readPackages(Array.from({ length: 7 }, (_, i) => row(String(i + 1))))
        .problem,
    ).toMatch(/Up to 6/);
    expect(readPackages([row("0")]).problem).toMatch(/1 to 20/);
    expect(readPackages([row("21")]).problem).toMatch(/1 to 20/);
    expect(readPackages([row("2"), row("2")]).problem).toMatch(
      /two packages of 2/,
    );
    expect(readPackages([row("1", "")]).problem).toMatch(/a price/);
    expect(readPackages([row("1", "0.5")]).problem).toMatch(/from ₦1/);
    expect(readPackages([row("1", "100", "see www.x.ng")]).problem).toMatch(
      /no links/,
    );
  });

  it("draws a label from the quantity and price", () => {
    expect(packageLabel(1, 2500000)).toBe("1 item · ₦25,000");
    expect(packageLabel(2, 4500000)).toBe("2 items · ₦45,000");
  });

  it("warns of a package dearer per item, never refusing it", () => {
    expect(
      dearerEach({ qty: 2, price_kobo: 5200000 }, 2500000, "the cleaner"),
    ).toMatch(/₦26,000 an item/);
    expect(
      dearerEach({ qty: 2, price_kobo: 4500000 }, 2500000, "the cleaner"),
    ).toBe("");
    expect(
      dearerEach({ qty: 2, price_kobo: 4500000 }, null, "the cleaner"),
    ).toBe("");
  });

  it("starts from three packages, the bigger a little cheaper each", () => {
    expect(startingRows(2500000).map((r) => [r.qty, r.price])).toEqual([
      ["1", "25000"],
      ["2", "45000"],
      ["3", "64000"],
    ]);
    expect(startingRows(null)[0]?.price).toBe("");
  });

  it("reads the area a refusal names", () => {
    expect(
      areaOffOf(new ApiError(409, "off", [], { area_off: "storefront" })),
    ).toBe("storefront");
    expect(areaOffOf(new ApiError(409, "no"))).toBe("");
    expect(areaOffOf(new Error("x"))).toBe("");
    expect(areaName("storefront")).toBe("Storefront");
  });

  it("knows a refusal for a newer version to publish first", () => {
    expect(
      newerVersionOf(
        new ApiError(409, "newer", [], { code: "newer_version", newer_version: 3 }),
      ),
    ).toBe(3);
    expect(newerVersionOf(new ApiError(409, "off", [], { area_off: "storefront" }))).toBe(0);
    expect(newerVersionOf(new Error("x"))).toBe(0);
  });
});
