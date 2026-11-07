import { describe, expect, it } from "vitest";
import { TOOLS, filterRows, toolsFor } from "./tools";

const tool = (k: string) => {
  const t = TOOLS.find((x) => x.key === k);
  if (!t) throw new Error(k);
  return t;
};

describe("the chat's tools", () => {
  const stock = {
    items: [
      { id: "1", name: "Rice, 50kg bag", available: true, stock: 42, unit: "bag", price_kobo: 7_800_000 },
      { id: "2", name: "Rice, 25kg bag", available: true, stock: 4, low: true, price_kobo: 4_100_000 },
      { id: "3", name: "Old beans", available: false, stock: null },
    ],
  };
  it("lists products with their price and stock, and counts the low ones", () => {
    const rows = tool("products").rows(stock);
    expect(rows[0]).toMatchObject({ name: "Rice, 50kg bag", sub: "₦78,000 a bag", pill: { text: "42 in stock", tone: "plain" } });
    expect(rows[1]?.pill).toEqual({ text: "4 left", tone: "warn" });
    expect(rows[2]?.sub).toBe("No price yet");
    expect(tool("products").line(rows)).toBe("3 items");
    expect(tool("stock").badge?.(tool("stock").rows(stock))).toEqual({ text: "1 low", tone: "warn" });
    // The stock tool lists every item, those with no count too (their first goods in).
    expect(tool("stock").rows(stock).map((r) => r.id)).toEqual(["2", "1", "3"]);
    expect(tool("stock").rows(stock)[2]?.pill).toEqual({ text: "No count", tone: "plain" });
  });
  it("filters by chip and by search", () => {
    const rows = tool("products").rows(stock);
    expect(filterRows(rows, "low", "").map((r) => r.id)).toEqual(["2"]);
    expect(filterRows(rows, "hidden", "").map((r) => r.id)).toEqual(["3"]);
    expect(filterRows(rows, undefined, "50kg").map((r) => r.id)).toEqual(["1"]);
  });
  it("offers only the tools a person may open, matching what follows /", () => {
    const can = (p: string) => p === "table.read";
    expect(toolsFor(can).map((t) => t.key)).toEqual(["products", "stock", "tables"]);
    expect(toolsFor(() => true, "pri").map((t) => t.key)).toEqual(["prices"]);
    expect(toolsFor(() => true, "lists").map((t) => t.key)).toEqual(["prices"]);
  });
  it("words orders, new ones first in the badge", () => {
    const rows = tool("orders").rows({
      orders: [{ id: "o1", order_reference: "AD-1", buyer_name: "Ada", total_kobo: 82_000_000, status: "received" }],
    });
    expect(rows[0]).toMatchObject({ name: "AD-1 · Ada", sub: "₦820,000", pill: { text: "New", tone: "lime" } });
    expect(tool("orders").badge?.(rows)).toEqual({ text: "1 new", tone: "lime" });
  });
});
