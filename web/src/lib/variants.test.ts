import { describe, expect, it } from "vitest";
import { allCells, findSize, keyOf, label, parseValues, pickable, sizesLine } from "./variants";
import type { StockItem } from "./stock";

const cs = [
  { name: "Size", values: ["41", "42"] },
  { name: "Colour", values: ["Black", "White"] },
];
const item = (id: string, size: string, colour: string, stock: number, extra: Partial<StockItem> = {}): StockItem => ({
  id,
  name: `Air Max 90 – ${size}, ${colour}`,
  available: true,
  stock,
  variant_of: "1",
  variant: { Size: size, Colour: colour },
  ...extra,
});
const items: StockItem[] = [
  { id: "1", name: "Air Max 90", available: true, stock: 0, choices: cs },
  item("2", "41", "Black", 3),
  item("3", "42", "Black", 0, { low: true }),
  item("4", "42", "White", 2, { low: true }),
  item("5", "41", "White", 9, { available: false }),
];

describe("sizes and colours", () => {
  it("says a variant in its choices' order, and keys it whatever the case", () => {
    expect(label({ Colour: "Black", Size: "42" }, cs)).toBe("42, Black");
    expect(keyOf({ Size: "42", Colour: "Black" })).toBe(keyOf({ colour: "black", size: "42" }));
  });
  it("makes every combination, the first choice slowest", () => {
    expect(allCells(cs).map((v) => label(v, cs))).toEqual(["41, Black", "41, White", "42, Black", "42, White"]);
    expect(allCells([])).toEqual([]);
  });
  it("counts a product's sizes on sale, its low ones and its stock", () => {
    expect(sizesLine(items, "1")).toEqual({ count: 3, low: 2, stock: 5 });
  });
  it("offers only what is on sale with some left, given what is picked", () => {
    expect([...pickable(items, "1", "Size", {})].sort()).toEqual(["41", "42"]);
    expect([...pickable(items, "1", "Colour", { Size: "42" })]).toEqual(["White"]);
    expect([...pickable(items, "1", "Colour", { Size: "41" })]).toEqual(["Black"]); // white 41 is off sale
    expect(findSize(items, "1", { Size: "42", Colour: "White" })?.id).toBe("4");
    expect(findSize(items, "1", { Size: "41", Colour: "White" })).toBeUndefined();
  });
  it("reads values typed on one line or several", () => {
    expect(parseValues("40, 41,41\n 42 ,, ")).toEqual(["40", "41", "42"]);
  });
});
