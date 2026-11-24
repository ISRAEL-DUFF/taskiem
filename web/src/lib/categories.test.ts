import { describe, expect, it } from "vitest";
import { categoriesOf, categorySuggestions, groupSuggestions, suggest } from "./mentions";
import { TOOLS, categoryChips, categoryTag, filterRows } from "./tools";
import { orderSections, orderShown } from "../pages/ChatBuyer";

// Product categories (docs/spec/chat-categories-and-variants.md, Part A).
describe("categories", () => {
  const items = [
    { kind: "item" as const, id: "1", name: "Air Max 90", category: "Sneakers", words: "Nike, leather" },
    { kind: "item" as const, id: "2", name: "Chuck Taylor", category: "Sneakers" },
    { kind: "item" as const, id: "3", name: "Slides", category: "Sandals" },
    { kind: "item" as const, id: "4", name: "Sneaker cleaner" },
  ];
  it("counts the categories, the most used first", () => {
    expect(categoriesOf(items)).toEqual([
      { name: "Sneakers", count: 2 },
      { name: "Sandals", count: 1 },
    ]);
    expect(categoriesOf([{ category: "Bags" }, { category: " bags " }])).toEqual([{ name: "Bags", count: 2 }]);
  });
  it("finds by name first, then by category and words", () => {
    expect(suggest(items, "sneak").map((s) => s.id)).toEqual(["4", "1", "2"]);
    expect(suggest(items, "nike").map((s) => s.id)).toEqual(["1"]);
  });
  it("offers a category the words name as the picker's first rows", () => {
    expect(categorySuggestions(categoriesOf(items), "sne")).toEqual([
      { kind: "category", id: "Sneakers", name: "Sneakers", sub: "2 items" },
    ]);
    expect(categorySuggestions(categoriesOf(items), "")).toEqual([]);
    const g = groupSuggestions("sand", { items, businesses: [] }, true);
    expect(g.map((x) => x.label)).toEqual(["Categories", "Products", ""]);
    expect(g[0]?.items[0]).toMatchObject({ kind: "category", name: "Sandals", sub: "1 item" });
    // The business's own categories, when given, rather than the items'.
    const own = groupSuggestions("bag", { items, businesses: [], categories: [{ name: "Bags", count: 48 }] }, false);
    expect(own[0]?.items[0]).toMatchObject({ name: "Bags", sub: "48 items" });
  });
  it("gives the Products sheet a chip per category of two or more", () => {
    const products = TOOLS.find((t) => t.key === "products");
    if (!products) throw new Error("no products tool");
    const rows = products.rows({
      items: [
        { id: "1", name: "Air Max 90", available: true, stock: 3, category: "Sneakers", description: "Nike" },
        { id: "2", name: "Chuck Taylor", available: true, stock: 2, category: "Sneakers" },
        { id: "3", name: "Slides", available: true, stock: 1, category: "Sandals" },
      ],
    });
    expect(categoryChips(rows)).toEqual([{ label: "Sneakers", tag: "cat:sneakers" }]);
    expect(filterRows(rows, categoryTag("Sneakers"), "").map((r) => r.id)).toEqual(["1", "2"]);
    expect(filterRows(rows, undefined, "nike").map((r) => r.id)).toEqual(["1"]);
    expect(filterRows(rows, undefined, "sand").map((r) => r.id)).toEqual(["3"]);
  });
  it("puts a supplier's list under its categories, Other last", () => {
    const list = [
      { item_id: "a", name: "Rice", category: "Grains", price_kobo: 1, min_quantity: 1, available: true },
      { item_id: "b", name: "Oil", price_kobo: 1, min_quantity: 1, available: true },
      { item_id: "c", name: "Beans", category: "grains", price_kobo: 1, min_quantity: 1, available: true },
    ];
    expect(orderSections(list).map((s) => [s.label, s.items.map((i) => i.item_id)])).toEqual([
      ["Grains", ["a", "c"]],
      ["Other", ["b"]],
    ]);
    expect(orderSections(list.slice(1, 2))).toEqual([{ label: "", items: [list[1]] }]);
    expect(orderShown(list, categoryTag("Grains"), "").map((i) => i.item_id)).toEqual(["a", "c"]);
    expect(orderShown(list, undefined, "grain").map((i) => i.item_id)).toEqual(["a", "c"]);
    // A supplier's words for an item are searched too.
    expect(orderShown([...list, { item_id: "e", name: "Runner", description: "Nike", price_kobo: 1, min_quantity: 1, available: true }], undefined, "nike").map((i) => i.item_id)).toEqual(["e"]);
  });
});
