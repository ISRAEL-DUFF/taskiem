import { describe, expect, it } from "vitest";
import { TOOLS, availabilityWord, filterRows, toolsFor } from "./tools";

const tool = (k: string) => {
  const t = TOOLS.find((x) => x.key === k);
  if (!t) throw new Error(k);
  return t;
};

describe("the chat's tools", () => {
  const stock = {
    items: [
      {
        id: "1",
        name: "Rice, 50kg bag",
        available: true,
        stock: 42,
        unit: "bag",
        price_kobo: 7_800_000,
      },
      {
        id: "2",
        name: "Rice, 25kg bag",
        available: true,
        stock: 4,
        low: true,
        price_kobo: 4_100_000,
      },
      { id: "3", name: "Old beans", available: false, stock: null },
    ],
  };
  it("lists products with their price and stock, and counts the low ones", () => {
    const rows = tool("products").rows(stock);
    expect(rows[0]).toMatchObject({
      name: "Rice, 50kg bag",
      sub: "₦78,000 a bag",
      pill: { text: "42 in stock", tone: "plain" },
    });
    expect(rows[1]?.pill).toEqual({ text: "4 left", tone: "warn" });
    expect(rows[2]?.sub).toBe("No price yet");
    expect(tool("products").line(rows)).toBe("3 items");
    expect(tool("stock").badge?.(tool("stock").rows(stock))).toEqual({
      text: "1 low",
      tone: "warn",
    });
    // The stock tool lists every item, those with no count too (their first goods in).
    expect(
      tool("stock")
        .rows(stock)
        .map((r) => r.id),
    ).toEqual(["2", "1", "3"]);
    expect(tool("stock").rows(stock)[2]?.pill).toEqual({
      text: "No count",
      tone: "plain",
    });
  });
  it("filters by chip and by search", () => {
    const rows = tool("products").rows(stock);
    expect(filterRows(rows, "low", "").map((r) => r.id)).toEqual(["2"]);
    expect(filterRows(rows, "hidden", "").map((r) => r.id)).toEqual(["3"]);
    expect(filterRows(rows, undefined, "50kg").map((r) => r.id)).toEqual(["1"]);
  });
  it("offers only the tools a person may open, matching what follows /", () => {
    const can = (p: string) => p === "table.read";
    expect(toolsFor(can).map((t) => t.key)).toEqual([
      "products",
      "stock",
      "tables",
      "purchases",
    ]);
    expect(toolsFor(() => true, "pri").map((t) => t.key)).toEqual(["prices"]);
    // Staff see their role's tools; the handover is a shift manager's.
    expect(toolsFor(() => true, "", ["cashier"]).map((t) => t.key)).toEqual([
      "sell",
      "stock",
      "orders",
    ]);
    expect(
      toolsFor(() => true, "", ["shift_manager"]).map((t) => t.key),
    ).toEqual(["sell", "handover", "stock", "orders", "bills"]);
    expect(toolsFor(() => true, "", ["owner"]).map((t) => t.key)).not.toContain(
      "handover",
    );
    expect(
      toolsFor(() => true, "", ["cashier", "admin"]).length,
    ).toBeGreaterThan(5);
    expect(toolsFor(() => true, "lists").map((t) => t.key)).toEqual(["prices"]);
    // Availability is a delivery business's alone (docs/spec/chat-riders.md).
    expect(toolsFor(() => true).map((t) => t.key)).not.toContain(
      "availability",
    );
    expect(
      toolsFor(() => true, "", undefined, true).map((t) => t.key),
    ).toContain("availability");
    expect(
      toolsFor(() => true, "ava", undefined, true).map((t) => t.key),
    ).toEqual(["availability"]);
  });
  it("words a rider's availability and shows it on the tile", () => {
    const avail = tool("availability");
    // A time today (not "now plus an hour", which after 23:00 is tomorrow
    // and would be worded with its date).
    const soon = new Date();
    soon.setHours(12, 30, 0, 0);
    const hhmm = "12:30";
    expect(availabilityWord({ state: "free", businesses: 1 })).toBe("Free");
    expect(availabilityWord({ state: "off", businesses: 1 })).toBe("Off today");
    expect(
      availabilityWord({
        state: "busy",
        until: soon.toISOString(),
        businesses: 1,
      }),
    ).toBe(`Busy till ${hhmm}`);
    expect(availabilityWord({ state: "busy", open: 2, businesses: 1 })).toBe(
      "Busy, 2 deliveries",
    );
    expect(availabilityWord({ state: "busy", open: 1, businesses: 1 })).toBe(
      "Busy, 1 delivery",
    );
    const rows = avail.rows({ state: "off", businesses: 2 });
    expect(avail.line(rows)).toBe("Off today");
    expect(avail.badge?.(rows)).toEqual({ text: "Off", tone: "warn" });
    expect(
      avail.badge?.(avail.rows({ state: "free", businesses: 2 })),
    ).toBeUndefined();
    expect(avail.line(avail.rows(undefined))).toBe("Free");
  });
  it("words orders, new ones first in the badge", () => {
    const rows = tool("orders").rows({
      orders: [
        {
          id: "o1",
          order_reference: "AD-1",
          buyer_name: "Ada",
          total_kobo: 82_000_000,
          status: "received",
        },
      ],
    });
    expect(rows[0]).toMatchObject({
      name: "AD-1",
      sub: "Ada · ₦820,000",
      pill: { text: "New", tone: "lime" },
    });
    expect(tool("orders").badge?.(rows)).toEqual({
      text: "1 new",
      tone: "lime",
    });
  });
});
