import { describe, expect, it } from "vitest";
import { itemsWithPrice, listFor, listLabel, sharedWords, type PriceList } from "./pricelists";

const item = (item_id: string, price_kobo: number) => ({ item_id, name: item_id, price_kobo, min_quantity: 2, available: true });
const list = (id: string, items: PriceList["items"], links: string[]): PriceList => ({ id, name: id, items, links, updated_at: "" });

describe("a product's price list", () => {
  const trade = list("Trade", [item("pp", 4_500_000)], ["rivers"]);
  const vip = list("VIP", [], ["eko"]);
  const other = list("Other", [item("jr", 400_000)], []);
  it("prefers the list shared with the conversation's business, then the one holding it", () => {
    expect(listFor([other, vip, trade], "pp", "rivers")?.id).toBe("Trade");
    expect(listFor([other, vip, trade], "jr", "eko")?.id).toBe("VIP");
    expect(listFor([other, vip, trade], "jr")?.id).toBe("Other");
    expect(listFor([vip, trade], "zz")?.id).toBe("VIP");
    expect(listFor([], "zz")).toBeUndefined();
  });
  it("sets the product's price, keeping the others, or adds it", () => {
    expect(itemsWithPrice(trade, "pp", 4_800_000)).toEqual([{ item_id: "pp", price_kobo: 4_800_000, min_quantity: 2, available: true }]);
    expect(itemsWithPrice(trade, "jr", 450_000)).toEqual([
      { item_id: "pp", price_kobo: 4_500_000, min_quantity: 2, available: true },
      { item_id: "jr", price_kobo: 450_000, min_quantity: 1, available: true },
    ]);
  });
  it("names who it is shared with", () => {
    const names = new Map([["rivers", "Rivers Foods Ltd"], ["mama", "Mama Ngozi Stores"]]);
    expect(sharedWords(["rivers", "mama"], names)).toBe("Rivers Foods Ltd, Mama Ngozi Stores");
    expect(sharedWords([], names)).toBe("No one yet");
  });
});

describe("listLabel", () => {
  it("says price list once", () => {
    expect(listLabel("Trade")).toBe("Trade price list");
    expect(listLabel("Trade prices")).toBe("Trade prices");
    expect(listLabel("VIP price list")).toBe("VIP price list");
  });
});
