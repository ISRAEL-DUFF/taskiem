import { afterEach, describe, expect, it, vi } from "vitest";
import { columnsFor, editorDoc, filterFits, liveQueue, newPart, recordTables, withUses, type PageDoc, type RecordView } from "./pages";

const views: RecordView[] = [
  {
    name: "orders",
    title: "Orders",
    on: true,
    may_read: true,
    area: "Orders",
    columns: [
      { name: "number", title: "Number", type: "text", personal: false },
      { name: "customer", title: "Customer", type: "text", personal: true },
      { name: "email", title: "Email", type: "email", personal: true },
      { name: "status", title: "Status", type: "text", personal: false },
      { name: "total", title: "Total", type: "integer", personal: false },
    ],
  },
  { name: "invoices", title: "Invoices", on: false, may_read: true, area: "Money and invoices", columns: [] },
];

describe("Taskiem's records on a page", () => {
  it("offers the views whose area is on, beside the tables", () => {
    const t = recordTables(views);
    expect(t.map((x) => [x.name, x.title])).toEqual([["records:orders", "Orders (Taskiem's)"]]);
  });

  it("never narrows, orders or adds up by a view's personal column", () => {
    const [orders] = recordTables(views);
    expect(columnsFor(orders, "pick").map((c) => c.name)).toEqual(["number", "status", "total", "created_at"]);
    expect(columnsFor(orders, "sum").map((c) => c.name)).toEqual(["total"]);
    expect(columnsFor(orders, "group").map((c) => c.name)).toEqual(["number", "status"]);
    // Shown masked, so offered to show.
    expect(columnsFor(orders, "any").map((c) => c.name)).toContain("customer");
    expect(filterFits({ id: "q", type: "search" }, orders)).toBe(true);
    expect(filterFits({ id: "q", type: "search", columns: ["customer"] }, orders)).toBe(false);
    expect(filterFits({ id: "m", type: "mine", column: "email" }, orders)).toBe(false);
  });

  it("saves a view as records and reads it back for the editor", () => {
    const [orders] = recordTables(views);
    const part = newPart("list", orders, []);
    expect(part.source?.table).toBe("records:orders");
    const doc: PageDoc = { page: "page/v1", name: "desk", title: "Desk", filters: [{ id: "q", type: "search" }], blocks: [part, newPart("count", orders, [part])] };
    const saved = withUses(doc, orders ? [orders] : []);
    expect(saved.blocks[0]?.source).toEqual({ records: "orders", order: [{ column: "created_at", desc: true }], uses: ["q"] });
    expect(saved.blocks[1]?.source).toEqual({ records: "orders" });
    expect(editorDoc(saved)?.blocks[0]?.source?.table).toBe("records:orders");
  });
});

describe("a page kept up to date", () => {
  afterEach(() => vi.useRealTimers());

  it("reads a burst of notices once, then waits two seconds before the next", async () => {
    vi.useFakeTimers();
    const reads: [string[], boolean][] = [];
    const q = liveQueue((ids, all) => void reads.push([ids.sort(), all]), 2000, () => Date.now());
    q.add({ page: "p", blocks: ["a"] });
    q.add({ page: "p", blocks: ["b", "a"] });
    await vi.advanceTimersByTimeAsync(0);
    expect(reads).toEqual([[["a", "b"], false]]);
    q.add({ page: "p", blocks: ["c"] });
    q.add({ page: "p", blocks: [], all: true });
    await vi.advanceTimersByTimeAsync(1000);
    expect(reads.length).toBe(1);
    await vi.advanceTimersByTimeAsync(1000);
    expect(reads).toEqual([
      [["a", "b"], false],
      [["c"], true],
    ]);
    q.stop();
  });
});
