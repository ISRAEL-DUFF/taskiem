import { describe, expect, it } from "vitest";
import {
  addRequest,
  changeRequest,
  emptyItem,
  itemProblem,
  countedByLine,
  rowCount,
  rowCounted,
  rowEdit,
  rowForm,
  rowSub,
  soldAsLine,
  stockList,
  worthLine,
  type StockItemRow,
  movePreview,
  moveProblem,
  moveRequest,
  movementSub,
  movementTitle,
  plural,
  quantity,
  stockLine,
  byHowLine,
  countDiff,
  countFromCSV,
  countProblem,
  countRequest,
  shrinkLine,
  emptySale,
  saleProblem,
  saleRequest,
  saleSub,
  saleTitle,
  saleTotal,
  unsoldSale,
  type Movement,
  type Sale,
  type SalesDay,
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

const fabric: StockItemRow = {
  id: "0192e2e0-0000-7000-8000-00000000000a",
  name: "Ankara fabric",
  unit: "yard",
  category: "",
  count: 4,
  cost_kobo: 150000,
  low_level: 10,
  target_level: 40,
  supplier_id: null,
  supplier: "Balogun Market",
  group_of: "",
  group_name: "",
  low: true,
  suggest: 36,
  value_kobo: 600000,
  retired: false,
  sold_as: [],
};

const malt = (uses: number, name = "Malt, pack of 6") => ({
  product_id: uses === 1 ? "7" : "8",
  name,
  uses,
  price_kobo: uses * 40000,
  variant: null,
  available: true,
});

const bottles: StockItemRow = {
  ...fabric,
  id: "0192e2e0-0000-7000-8000-00000000000b",
  name: "Malt bottle",
  unit: "bottle",
  count: 20,
  cost_kobo: 25000,
  low_level: null,
  target_level: null,
  supplier: "",
  low: false,
  suggest: null,
  value_kobo: 500000,
  sold_as: [malt(6)],
};

describe("stock items", () => {
  it("says what a stock item is sold as, in plain words", () => {
    expect(soldAsLine(fabric)).toBe("Not sold");
    expect(soldAsLine(bottles)).toBe(
      "Sold as Malt, pack of 6 (uses 6 bottles)",
    );
    expect(
      soldAsLine({ ...bottles, sold_as: [malt(6), malt(1, "Malt bottle")] }),
    ).toBe("Sold as Malt, pack of 6 (uses 6 bottles) and Malt bottle");
    expect(soldAsLine({ ...bottles, unit: "", sold_as: [malt(6)] })).toBe(
      "Sold as Malt, pack of 6 (uses 6)",
    );
    expect(rowCount(fabric)).toBe("4 yards");
    expect(rowSub(fabric)).toBe(
      "reorder at 10 · up to 40 · cost ₦1,500 · Balogun Market · order 36 yards",
    );
    expect(rowSub(bottles)).toBe("cost ₦250");
    expect(rowSub({ ...bottles, cost_kobo: null })).toBe("No levels yet");
  });

  it("says what the list is worth", () => {
    const v = { keeps_stock: true, value_kobo: 1100000, low: 1, items: [fabric, bottles] };
    expect(worthLine(v, "all")).toBe("2 counted · worth ₦11,000 at cost · 1 low");
    expect(worthLine({ ...v, items: [fabric], low: 0, value_kobo: 600000 }, "not_sold")).toBe(
      "1 not sold · worth ₦6,000 at cost",
    );
  });

  it("groups the stock items of a product's sizes and colours under it", () => {
    const size = (id: string, name: string, count: number, low = false): StockItemRow => ({
      ...bottles,
      id,
      name,
      count,
      low,
      group_of: "p1",
      group_name: "Sneakers",
      sold_as: [{ ...malt(1, name), product_id: `${id}-row` }],
    });
    const list = stockList([
      fabric,
      size("a", "Sneakers (42, Black)", 3, true),
      size("b", "Sneakers (43, Black)", 5),
      bottles,
    ]);
    expect(list.map((x) => (x.kind === "group" ? `group ${x.name}` : x.row.name))).toEqual([
      "Ankara fabric",
      "group Sneakers",
      "Sneakers (42, Black)",
      "Sneakers (43, Black)",
      "Malt bottle",
    ]);
    expect(list[1]).toEqual({ kind: "group", id: "p1", name: "Sneakers", count: 8, sizes: 2, low: 1 });
    expect(list[2]).toMatchObject({ kind: "item", group: "Sneakers" });
  });

  it("changes a material by its own id, a product's stock item through its product", () => {
    expect(rowEdit(fabric)).toEqual({ id: fabric.id, name: true, price: false });
    // Sold as a pack of 6: its name and price are the pack's, not here.
    expect(rowEdit(bottles)).toEqual({ id: "8", name: false, price: false });
    // One for one: the product's name and price too.
    const one = { ...bottles, sold_as: [malt(1, "Malt bottle")] };
    expect(rowEdit(one)).toEqual({ id: "7", name: true, price: true });
    expect(rowForm(one).price).toBe("400");
    expect(rowForm(bottles).price).toBe("");
    expect(rowEdit({ ...one, group_of: "p1" })).toEqual({ id: "7", name: false, price: false });
    // Stock in, counts and history name the stock item itself.
    expect(rowCounted(bottles)).toEqual({ id: bottles.id, stock: 20, unit: "bottle" });
  });

  it("says what a product is counted by", () => {
    expect(countedByLine({ stock_item_id: bottles.id, uses: 6 }, [fabric, bottles])).toBe(
      "Malt bottle · one sale uses 6 bottles · 20 bottles now",
    );
    expect(countedByLine({ stock_item_id: bottles.id, uses: 1 }, [bottles])).toBe(
      "Malt bottle · 20 bottles now",
    );
    expect(countedByLine({}, [])).toBe("Not counted: selling it changes no count");
    expect(countedByLine({ choices: [{ name: "Size", values: ["42"] }] }, [])).toContain(
      "sizes and colours",
    );
  });
});

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

  it("says a movement", () => {
    const m: Movement = {
      id: "m1",
      item_id: "1",
      item: "Indomie",
      reason: "received",
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
        price: "50,000",
      }, true),
    ).toEqual({
      name: "Rice",
      quantity: 20,
      unit: "bag",
      cost_kobo: 4500000,
      price_kobo: 5000000,
      reorder_level: undefined,
      target_level: undefined,
      supplier: "",
    });
    // Not sold: a material, with no price whatever was typed.
    expect(
      addRequest({ ...emptyItem(), name: "Ankara fabric", quantity: "20", unit: "yard", price: "9" }, false),
    ).toEqual({
      name: "Ankara fabric",
      quantity: 20,
      unit: "yard",
      cost_kobo: undefined,
      material: true,
      reorder_level: undefined,
      target_level: undefined,
      supplier: "",
    });
  });

  it("sends only what changed", () => {
    const f = rowForm(bottles);
    expect(changeRequest(f, f)).toEqual({});
    expect(
      changeRequest(f, { ...f, reorder: "12", supplier: "Ada Stores" }),
    ).toEqual({ reorder_level: 12, supplier: "Ada Stores" });
    expect(changeRequest(f, { ...f, name: "Malt bottles" })).toEqual({
      name: "Malt bottles",
    });
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

describe("sales", () => {
  const items: StockItem[] = [
    { id: "1", name: "Indomie", available: true, stock: 4, unit: "carton" },
    {
      id: "2",
      name: "Rice",
      available: true,
      stock: 30,
      unit: "bag",
      price_kobo: 4500000,
    },
  ];
  it("reads a day's takings by how they were paid", () => {
    const d = {
      by_how: { cash: 9000000, transfer: 0, pos: 450000 },
    } as SalesDay;
    expect(byHowLine(d)).toBe("Cash ₦90,000 · Terminal ₦4,500");
    expect(byHowLine({ ...d, by_how: { cash: 100000, credit: 4500000 } })).toBe("Cash ₦1,000 · On credit ₦45,000");
  });
  it("titles a sale and says whether its money came", () => {
    const s: Sale = {
      id: "s",
      reference: "S-7QX4KD",
      lines: [
        {
          item_id: "2",
          item: "Rice",
          quantity: 2,
          unit: "bag",
          price_kobo: 4500000,
          line_kobo: 9000000,
        },
        {
          item_id: "1",
          item: "Indomie",
          quantity: 1,
          unit: "carton",
          price_kobo: 1000000,
          line_kobo: 1000000,
        },
      ],
      amount_kobo: 10000000,
      paid_how: "pos",
      status: "waiting",
      by: "user:ada",
      sold_at: new Date().toISOString(),
      short: ["Indomie"],
    };
    expect(saleTitle(s)).toBe("2 bags Rice, 1 carton Indomie");
    expect(saleSub(s)).toContain(
      "Terminal · waiting for the terminal's report · S-7QX4KD · more Indomie sold than counted",
    );
    expect(saleSub({ ...s, status: "matched" })).toContain("money came in");
  });
  it("prices a sale from its items, or needs the amount", () => {
    const f = {
      ...emptySale(),
      lines: [{ item_id: "2", quantity: "2", price: "" }],
    };
    expect(saleTotal(items, f)).toBe(9000000);
    expect(saleProblem(items, f)).toBeNull();
    const noPrice = {
      ...f,
      lines: [{ item_id: "1", quantity: "2", price: "" }],
    };
    expect(saleProblem(items, noPrice)).toBe(
      "Indomie has no price: give its price or the amount taken.",
    );
    expect(saleProblem(items, { ...noPrice, amount: "20,000" })).toBeNull();
    expect(
      saleProblem(items, {
        ...f,
        lines: [{ item_id: "", quantity: "1", price: "" }],
      }),
    ).toBe("Choose the item sold on each line.");
    expect(
      saleProblem(items, {
        ...f,
        lines: [{ item_id: "2", quantity: "1.5", price: "" }],
      }),
    ).toContain("whole number");
    expect(saleProblem(items, { ...f, amount: "0" })).toContain("is an amount");
    expect(
      saleRequest({ ...noPrice, amount: "20,000", how: "transfer" }),
    ).toEqual({
      lines: [{ item_id: "1", quantity: 2, price_kobo: undefined }],
      paid_how: "transfer",
      note: "",
      amount_kobo: 2000000,
    });
  });
  it("gives a terminal's sale its items for the amount it reported", () => {
    const u = { entry_id: "e1", amount_kobo: 450050, channel: "pos", at: "" };
    const f = unsoldSale(u);
    expect(f.how).toBe("pos");
    expect(f.amount).toBe("4500.50");
    expect(
      saleRequest(
        { ...f, lines: [{ item_id: "1", quantity: "1", price: "" }] },
        u,
      ),
    ).toEqual({
      lines: [{ item_id: "1", quantity: 1, price_kobo: undefined }],
      paid_how: "pos",
      note: "",
      amount_kobo: 450050,
      entry_id: "e1",
    });
  });
});

describe("counts", () => {
  const indomie: StockItem = {
    id: "1",
    name: "Indomie",
    available: true,
    stock: 40,
    unit: "carton",
  };
  const rice: StockItem = {
    id: "2",
    name: "Rice",
    available: true,
    stock: null,
    unit: "bag",
  };
  const items = [indomie, rice];
  it("says how a count differs", () => {
    expect(countDiff(indomie, "37")).toBe("40 → 37 cartons (3 fewer)");
    expect(countDiff(indomie, "40")).toBe("as Taskiem has it");
    expect(countDiff(indomie, "42")).toBe("40 → 42 cartons (2 more)");
    expect(countDiff(rice, "12")).toBe("12 bags, first count");
    expect(countDiff(indomie, "")).toBeNull();
  });
  it("checks and sends the items counted", () => {
    expect(countProblem({})).toContain("at least one");
    expect(countProblem({ "1": "2.5" })).toContain("whole number");
    expect(countProblem({ "1": "37", "2": "" })).toBeNull();
    expect(countRequest({ "1": "37", "2": "" }, " rats ")).toEqual({
      lines: [{ item_id: "1", count: 37 }],
      reason: "rats",
    });
  });
  it("reads a CSV of names and counts", () => {
    const out = countFromCSV(
      'Item,Count\nindomie,37\n"Rice";"1,200"\nGarri,4\n\n',
      items,
    );
    expect(out.sheet).toEqual({ "1": "37", "2": "1200" });
    expect(out.unknown).toEqual(["Garri"]);
  });
  it("says a month's shrinkage", () => {
    const base = { from: "", to: "", items: [] };
    expect(
      shrinkLine({ ...base, received: 0, sold: 0, lost: 0, lost_kobo: 0 }),
    ).toBe("Nothing moved yet this month.");
    expect(
      shrinkLine({
        ...base,
        received: 35,
        sold: 12,
        lost: 4,
        lost_kobo: 12000000,
      }),
    ).toBe("35 in, 12 sold, 4 lost (₦120,000 at cost)");
  });
});
