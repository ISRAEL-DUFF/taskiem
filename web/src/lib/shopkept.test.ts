import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ShopItem } from "./personchat";
import { categories, forgetShop, keepTo, fromPrice, pricesDiffer, recentItems, RECENT_KEPT, savedItems, shownItems, soldOut, toggleSaved, viewedItem } from "./shopkept";

const store = new Map<string, string>();
beforeEach(() => {
  store.clear();
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
    removeItem: (k: string) => void store.delete(k),
  });
  forgetShop();
});

const jollof: ShopItem = { id: "i1", name: "Jollof rice & chicken", category: "Rice", available: true, price_kobo: 450000, sizes: [
    { id: "s", variant: { size: "Small" }, price_kobo: 300000, available: true },
    { id: "b", variant: { size: "Big" }, price_kobo: 600000, available: false },
  ] };
const egusi: ShopItem = { id: "i2", name: "Egusi soup", category: "Soups", description: "With pounded yam", available: true, price_kobo: 600000 };
const chapman: ShopItem = { id: "i3", name: "Chapman", category: "Drinks", available: false, price_kobo: 150000 };
const zobo: ShopItem = { id: "i4", name: "Zobo", category: " Drinks ", available: true, price_kobo: 80000 };
const items = [jollof, egusi, chapman, zobo];

describe("the shop kept in the browser", () => {
  it("saves with the heart, by business, newest first, and unsaves", () => {
    expect(toggleSaved("b1", "i2")).toEqual(["i2"]);
    toggleSaved("b1", "i4");
    toggleSaved("b2", "i9");
    expect(savedItems("b1")).toEqual(["i4", "i2"]);
    expect(JSON.parse(store.get("taskiem-shop-saved") ?? "")).toEqual({ b1: ["i4", "i2"], b2: ["i9"] });
    expect(toggleSaved("b1", "i4")).toEqual(["i2"]);
  });

  it("keeps what was looked at lately, newest first, once each, at most twelve", () => {
    viewedItem("b1", "i1");
    viewedItem("b1", "i2");
    viewedItem("b1", "i1");
    expect(recentItems("b1")).toEqual(["i1", "i2"]);
    for (let n = 0; n < 20; n++) viewedItem("b1", `x${n}`);
    expect(recentItems("b1")).toHaveLength(RECENT_KEPT);
    expect(recentItems("b1")[0]).toBe("x19");
  });

  it("forgets both on signing out", () => {
    toggleSaved("b1", "i2");
    viewedItem("b1", "i2");
    forgetShop();
    expect(savedItems("b1")).toEqual([]);
    expect(recentItems("b1")).toEqual([]);
  });

  it("holds while the page is open where storage cannot be written", () => {
    vi.stubGlobal("localStorage", {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("blocked");
      },
      removeItem: () => undefined,
    });
    toggleSaved("b1", "i2");
    expect(savedItems("b1")).toEqual(["i2"]);
  });
});

describe("the shop's items", () => {
  it("prices from the cheapest size; says when sizes differ", () => {
    expect(fromPrice(jollof)).toBe(300000);
    expect(pricesDiffer(jollof)).toBe(true);
    expect(fromPrice(egusi)).toBe(600000);
    expect(pricesDiffer(egusi)).toBe(false);
  });

  it("is sold out when it, or every size, is gone", () => {
    expect(soldOut(jollof)).toBe(false);
    expect(soldOut(chapman)).toBe(true);
    expect(soldOut({ ...jollof, sizes: (jollof.sizes ?? []).map((z) => ({ ...z, available: false })) })).toBe(true);
  });

  it("names its categories once, in order; filters by category, saved and words", () => {
    expect(categories(items)).toEqual(["Rice", "Soups", "Drinks"]);
    expect(shownItems(items, "cat:Drinks", "", []).map((i) => i.id)).toEqual(["i3", "i4"]);
    expect(shownItems(items, "saved", "", ["i2"]).map((i) => i.id)).toEqual(["i2"]);
    expect(shownItems(items, "all", "pounded", []).map((i) => i.id)).toEqual(["i2"]);
    expect(shownItems(items, "all", "pizza", [])).toEqual([]);
  });
});

describe("a bag kept while the page was open", () => {
  it("drops what is gone or sold out, cuts to how few are left, takes the prices now", () => {
    const shop = { items: [jollof, egusi, chapman, { ...zobo, few_left: 2 }] };
    const { lines, changed } = keepTo(
      [
        { item_id: "s", name: "Jollof rice & chicken (Small)", price_kobo: 250000, quantity: 1 },
        { item_id: "b", name: "Jollof rice & chicken (Big)", price_kobo: 600000, quantity: 1 },
        { item_id: "i3", name: "Chapman", price_kobo: 150000, quantity: 1 },
        { item_id: "gone", name: "Old thing", price_kobo: 100, quantity: 1 },
        { item_id: "i4", name: "Zobo", price_kobo: 80000, quantity: 5 },
        { item_id: "i2", name: "Egusi soup", price_kobo: 600000, quantity: 1 },
      ],
      shop,
    );
    expect(changed).toBe(true);
    expect(lines).toEqual([
      { item_id: "s", name: "Jollof rice & chicken (Small)", price_kobo: 300000, quantity: 1 },
      { item_id: "i4", name: "Zobo", price_kobo: 80000, quantity: 2 },
      { item_id: "i2", name: "Egusi soup", price_kobo: 600000, quantity: 1 },
    ]);
  });

  it("says nothing changed when all is still for sale", () => {
    expect(keepTo([{ item_id: "i2", name: "Egusi soup", price_kobo: 600000, quantity: 1 }], { items: [egusi] }).changed).toBe(false);
  });
});
