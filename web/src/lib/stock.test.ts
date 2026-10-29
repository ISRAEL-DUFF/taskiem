import { describe, expect, it } from "vitest";
import {
  addRequest,
  changeRequest,
  emptyItem,
  itemForm,
  itemProblem,
  itemSub,
  movePreview,
  moveProblem,
  moveRequest,
  movementSub,
  movementTitle,
  plural,
  quantity,
  stockLine,
  type Movement,
  type StockItem,
} from "./stock";

const indomie: StockItem = {
  id: "1",
  name: "Indomie",
  available: true,
  stock: 4,
  unit: "carton",
  cost_kobo: 850000,
  reorder_level: 10,
  target_level: 50,
  supplier: "Dangote Depot",
  low: true,
  suggest: 46,
};

describe("stock", () => {
  it("says quantities in their units", () => {
    expect(plural("bag")).toBe("bags");
    expect(plural("box")).toBe("boxes");
    expect(plural("battery")).toBe("batteries");
    expect(plural("glass")).toBe("glasses");
    expect(quantity(1, "carton")).toBe("1 carton");
    expect(quantity(48, "carton")).toBe("48 cartons");
    expect(quantity(3)).toBe("3");
    expect(stockLine(indomie)).toBe("4 cartons");
    expect(stockLine({ ...indomie, stock: null })).toBe("No count");
  });

  it("says an item's levels, cost, supplier and what to order", () => {
    expect(itemSub(indomie)).toBe(
      "reorder at 10 · up to 50 · cost ₦8,500 · Dangote Depot · order 46 cartons",
    );
    expect(
      itemSub({ id: "2", name: "Rice", available: true, stock: 3, waiting: 2 }),
    ).toBe("2 changes on their way");
    expect(itemSub({ id: "2", name: "Rice", available: true, stock: 3 })).toBe(
      "No levels yet",
    );
  });

  it("says a movement", () => {
    const m: Movement = {
      id: "m1",
      item_id: "1",
      item: "Indomie",
      reason: "received",
      status: "done",
      before: 0,
      after: 50,
      change: 50,
      by: "user:ada",
      reference: "PO-12",
      created_at: "2026-10-28T09:00:00Z",
      done_at: "2026-10-28T09:00:01Z",
    };
    expect(movementTitle(m)).toBe("Indomie: received +50");
    expect(movementSub(m)).toContain("0 → 50 · PO-12");
    expect(movementTitle({ ...m, reason: "sale", change: -2 })).toBe(
      "Indomie: sold -2",
    );
    expect(
      movementTitle({ ...m, reason: "count", set_to: 12, change: -38 }),
    ).toBe("Indomie: counted 12");
    expect(
      movementSub({ ...m, reason: "order", reference: "AK-7QX4KD", short: 1 }),
    ).toContain("1 more asked for than there was · order AK-7QX4KD");
    expect(
      movementSub({
        ...m,
        status: "pending",
        before: null,
        after: null,
        change: null,
        error: "cut off",
      }),
    ).toContain("not in the table yet");
  });

  it("checks the item form", () => {
    expect(itemProblem(emptyItem(), true)).toBe("Give the item a name.");
    expect(
      itemProblem({ ...emptyItem(), name: "Rice", quantity: "2.5" }, true),
    ).toContain("whole number");
    expect(
      itemProblem({ ...emptyItem(), name: "Rice", cost: "abc" }, true),
    ).toContain("cost price");
    expect(
      itemProblem(
        { ...emptyItem(), name: "Rice", reorder: "10", target: "5" },
        true,
      ),
    ).toContain("more than the reorder level");
    expect(
      itemProblem(
        {
          ...emptyItem(),
          name: "Rice",
          quantity: "20",
          unit: "bag",
          cost: "45,000",
          reorder: "5",
          target: "30",
        },
        true,
      ),
    ).toBeNull();
    expect(
      addRequest({
        ...emptyItem(),
        name: " Rice ",
        quantity: "20",
        unit: "bag",
        cost: "45,000",
      }),
    ).toEqual({
      name: "Rice",
      quantity: 20,
      unit: "bag",
      cost_kobo: 4500000,
      price_kobo: undefined,
      reorder_level: undefined,
      target_level: undefined,
      supplier: "",
    });
  });

  it("sends only what changed", () => {
    const f = itemForm(indomie);
    expect(changeRequest(indomie, f)).toEqual({});
    expect(
      changeRequest(indomie, { ...f, reorder: "12", supplier: "Ada Stores" }),
    ).toEqual({ reorder_level: 12, supplier: "Ada Stores" });
  });

  it("checks and previews a stock change", () => {
    expect(
      moveProblem({ kind: "received", amount: "", off: false, note: "" }),
    ).toBe("Enter how many, like 5.");
    expect(
      moveProblem({ kind: "adjustment", amount: "2", off: true, note: "" }),
    ).toBe("Say why, like spoilt or found.");
    expect(
      moveProblem({ kind: "count", amount: "0", off: false, note: "" }),
    ).toBeNull();
    expect(
      moveRequest(indomie, {
        kind: "adjustment",
        amount: "2",
        off: true,
        note: "spoilt",
      }),
    ).toEqual({
      item_id: "1",
      reason: "adjustment",
      note: "spoilt",
      change: -2,
    });
    expect(
      moveRequest(indomie, {
        kind: "count",
        amount: "7",
        off: false,
        note: "",
      }),
    ).toEqual({ item_id: "1", reason: "count", note: "", count: 7 });
    expect(
      movePreview(indomie, {
        kind: "adjustment",
        amount: "9",
        off: true,
        note: "x",
      }),
    ).toBe("4 → 0 cartons");
    expect(
      movePreview(indomie, {
        kind: "received",
        amount: "46",
        off: false,
        note: "",
      }),
    ).toBe("4 → 50 cartons");
  });
});
