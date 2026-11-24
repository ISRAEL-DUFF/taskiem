import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import {
  NEW_STOCK,
  askUses,
  billLineOf,
  lineLabel,
  singular,
  usesProblem,
  usesQuestion,
  choiceProblem,
  choiceWords,
  fromLine,
  fromMemory,
  handLineRequest,
  handProblem,
  knownLabel,
  labelChoices,
  labelNameProblem,
  lineBody,
  lineMark,
  madeStockItem,
  nameKey,
  newStockRequest,
  sameChoice,
  suggestChoice,
  toAnswer,
  trackAskOf,
  trackAskWords,
  usesWords,
  type SpendLabel,
} from "./treat";

const label = (id: string, name: string, uses: number): SpendLabel => ({
  id,
  name,
  uses,
  created_by: "user:ada",
  created_at: "2026-11-01T09:00:00Z",
});
const labels = [label("l1", "Packaging", 3), label("l2", "Machines", 9)];

describe("the question on a line bought", () => {
  it("suggests from a stock item of the name, things tracked before, else record", () => {
    const stock = [{ id: "s1", name: "Ankara fabric" }];
    const sugg = [{ name: "Fabric" }];
    expect(suggestChoice(" ankara  Fabric", stock, [], sugg)).toEqual({
      treatment: "stock",
      stockItemId: "s1",
      label: "Fabric",
    });
    expect(
      suggestChoice("Singer sewing machine", stock, ["singer sewing machine"], [
        { name: "Machines", label_id: "l2" },
      ]),
    ).toEqual({ treatment: "track", label: "Machines" });
    expect(suggestChoice("Diesel", stock, [], [])).toEqual({
      treatment: "record",
      label: "",
    });
    // A retired stock item is not suggested.
    expect(
      suggestChoice("Ankara fabric", [{ id: "s1", name: "Ankara fabric", retired: true }], [], [])
        .treatment,
    ).toBe("record");
  });

  it("offers the business's labels by use, a matching one first, then new names", () => {
    expect(labelChoices(labels, [{ name: "Fabric" }])).toEqual([
      "Machines",
      "Packaging",
      "Fabric",
    ]);
    expect(
      labelChoices(labels, [{ name: "Packaging", label_id: "l1" }, { name: "Bags" }]),
    ).toEqual(["Packaging", "Machines", "Bags"]);
    expect(labelChoices(labels, [], 1)).toEqual(["Machines"]);
  });

  it("says what is missing before a line can go", () => {
    expect(choiceProblem({ treatment: "record", label: "" })).toBe("");
    expect(choiceProblem({ treatment: "stock", label: "" })).toBe(
      "Say which stock item it goes into.",
    );
    expect(
      choiceProblem({ treatment: "stock", stockItemId: NEW_STOCK, newName: "Thread", label: "" }),
    ).toBe("Say whether you sell it.");
    expect(
      choiceProblem({
        treatment: "stock",
        stockItemId: NEW_STOCK,
        newName: "Thread",
        sells: true,
        price: "abc",
        label: "",
      }),
    ).toBe("A selling price in naira, like 25,000.");
    expect(choiceProblem({ treatment: "track", label: "x".repeat(61) })).toBe(
      "A label is up to 60 characters.",
    );
  });

  it("words an answer in a line", () => {
    expect(choiceWords({ treatment: "track", label: "Machines" })).toBe(
      "Keep track of it · Machines",
    );
    expect(
      choiceWords({ treatment: "stock", stockItemId: "s1", uses: 6, label: "" }, () => "Malt"),
    ).toBe("Add to stock: Malt (6 each) · Not labelled");
    expect(choiceWords({ treatment: "record", label: " Diesel " })).toBe(
      "Just record it · Diesel",
    );
  });

  it("reads memory and order lines as answers", () => {
    expect(
      fromMemory({
        name: "Singer",
        treatment: "track",
        uses: 1,
        label: "Machines",
        label_id: "l2",
        updated_by: "user:ada",
        updated_at: "",
      }),
    ).toEqual({ treatment: "track", stockItemId: undefined, uses: undefined, label: "Machines" });
    expect(fromLine({ treatment: "stock", stock_item_id: "s1", uses: 1, label_id: "l1" }, labels)).toEqual({
      treatment: "stock",
      stockItemId: "s1",
      uses: undefined,
      label: "Packaging",
    });
    expect(fromLine({ treatment: "record", stock_item_id: "s1" }, labels).stockItemId).toBeUndefined();
  });

  it("builds the line's body for the API", () => {
    expect(lineBody({ treatment: "track", label: "Machines", trackMany: true }, { labelId: "l2" })).toEqual({
      treatment: "track",
      label_id: "l2",
      track_many: true,
    });
    expect(lineBody({ treatment: "stock", uses: 6, label: "", trackMany: true }, { stockItemId: "s1" })).toEqual({
      treatment: "stock",
      stock_item_id: "s1",
      uses: 6,
    });
    expect(lineBody({ treatment: "record", stockItemId: "s1", label: "" }, { stockItemId: "s1" })).toEqual({
      treatment: "record",
    });
    expect(knownLabel(labels, " machines")).toBe("l2");
    expect(knownLabel(labels, "Snacks")).toBeUndefined();
    expect(knownLabel(labels, "")).toBeUndefined();
  });

  it("makes a new stock item as a product or a material", () => {
    const c = { treatment: "stock" as const, stockItemId: NEW_STOCK, newName: " Thread ", label: "" };
    expect(newStockRequest({ ...c, sells: false }, "reel")).toEqual({
      name: "Thread",
      unit: "reel",
      quantity: 0,
      material: true,
    });
    expect(newStockRequest({ ...c, sells: true, price: "1,500" })).toEqual({
      name: "Thread",
      unit: "",
      quantity: 0,
      price_kobo: 150_000,
    });
    expect(madeStockItem({ id: "m1", material: true })).toBe("m1");
    expect(madeStockItem({ id: "p1", stock_item_id: "s9" })).toBe("s9");
  });

  it("knows when two answers differ", () => {
    expect(sameChoice({ treatment: "track", label: "Machines" }, { treatment: "track", label: "machines " })).toBe(true);
    expect(sameChoice({ treatment: "stock", stockItemId: "a", label: "" }, { treatment: "stock", stockItemId: "b", label: "" })).toBe(false);
  });
});

describe("many to keep track of", () => {
  it("reads 409 track_ask and 422 track_too_many", () => {
    const ask = new ApiError(409, "put 500 nylon bags…", [], {
      code: "track_ask",
      line: 1,
      item: "nylon bags",
      quantity: 500,
    });
    const a = trackAskOf(ask);
    expect(a).toEqual({ line: 1, item: "nylon bags", quantity: 500, tooMany: false });
    expect(a && trackAskWords(a)).toBe("Put 500 nylon bags on Things we have, one row each?");
    const many = trackAskOf(
      new ApiError(422, "", [], { code: "track_too_many", line: 0, item: "nylon bags", quantity: 300 }),
    );
    expect(many && trackAskWords(many)).toMatch(/^300 nylon bags are too many to keep track of one by one/);
    expect(trackAskOf(new ApiError(409, "x", [], { code: "line_locked" }))).toBeNull();
    expect(trackAskOf(new Error("x"))).toBeNull();
  });
});

describe("orders and bills", () => {
  it("marks where a line went", () => {
    expect(lineMark("stock")?.word).toBe("To stock");
    expect(lineMark("track")?.word).toBe("On Things we have");
    expect(lineMark("record")).toBeNull();
  });

  it("says what an order still asks", () => {
    const line = { item_id: "1", item: "Diesel", quantity: 1, cost_kobo: 0, received: 0 };
    const p = { status: "drafted" as const, seller_status: null, lines: [{ ...line, unconfirmed: true }, line] };
    expect(toAnswer(p)).toBe("Say how to treat 1 item");
    expect(toAnswer({ ...p, lines: [{ ...line, unconfirmed: true }, { ...line, unconfirmed: true }] })).toBe(
      "Say how to treat 2 items",
    );
    expect(toAnswer({ ...p, status: "cancelled" })).toBe("");
    expect(toAnswer({ ...p, seller_status: "proposed" as const })).toBe("");
  });

  it("finds a bill line's order line by their item", () => {
    const orders = [
      {
        id: "o1",
        lines: [{ item_id: "x", seller_item_id: "x", item: "Machine", quantity: 1, cost_kobo: 1, received: 1, treatment: "track" as const }],
      },
    ];
    expect(billLineOf({ purchase_id: "o1", seller_item_id: "x" }, orders)?.treatment).toBe("track");
    expect(billLineOf({ purchase_id: "o1" }, orders)).toBeUndefined();
    expect(billLineOf({}, orders)).toBeUndefined();
  });
});

describe("orders by hand and labels", () => {
  it("matches a name without case or extra spaces", () => {
    expect(nameKey("  Diesel   fuel ")).toBe("diesel fuel");
  });

  it("checks an order by hand", () => {
    expect(handProblem("", [{ item: "Diesel", quantity: "1", price: "" }])).toBe("Say which supplier it is to.");
    expect(handProblem("s1", [{ item: "", quantity: "1", price: "" }])).toBe("Add what you are ordering.");
    expect(handProblem("s1", [{ item: "Diesel", quantity: "0", price: "" }])).toBe(
      "How many Diesel is a whole number, like 2.",
    );
    expect(handProblem("s1", [{ item: "Diesel", quantity: "2", price: "x" }])).toBe(
      "The price of Diesel is in naira, like 15,000.",
    );
    expect(handProblem("s1", [{ item: "Diesel", quantity: "2", price: "15,000" }])).toBe("");
    expect(handLineRequest({ item: " Diesel ", quantity: "2", price: "15,000" })).toEqual({
      item: "Diesel",
      quantity: 2,
      cost_kobo: 1_500_000,
    });
    expect(handLineRequest({ item: "Diesel", quantity: "2", price: "" })).toEqual({ item: "Diesel", quantity: 2 });
  });

  it("words a label's uses and checks a new name", () => {
    expect(usesWords(0)).toBe("Not used yet");
    expect(usesWords(1)).toBe("Used once");
    expect(usesWords(12)).toBe("Used 12 times");
    expect(labelNameProblem(" ", labels)).toBe("Give the label a name.");
    expect(labelNameProblem("machines", labels, "l1")).toBe("Machines is already a label: merge them instead.");
    expect(labelNameProblem("Machines", labels, "l2")).toBe("");
  });
});

describe("a merged label on a line (H2 g-label-merge)", () => {
  // Snacks merged into Staff welfare: the business's labels leave it out.
  const kept = [label("l-w", "Staff welfare", 5)];
  it("names the label kept, as the server sends it", () => {
    expect(lineLabel({ label_id: "l-snacks", label: "Staff welfare" }, kept)).toBe("Staff welfare");
    expect(fromLine({ treatment: "record", label_id: "l-snacks", label: "Staff welfare" }, kept).label).toBe("Staff welfare");
  });
  it("falls back to the business's labels, and none for none", () => {
    expect(lineLabel({ label_id: "l-w" }, kept)).toBe("Staff welfare");
    expect(lineLabel({}, kept)).toBe("");
  });
});

describe("how many of a stock item one is (H2 g-uses-ask)", () => {
  // The same cases as engine/stock TestAskUses.
  it.each([
    ["bag", "g", 1, true],
    ["", "g", 1, true],
    ["", "yards", 1, true],
    ["", "bag", 1, false],
    ["tin", "carton", 1, true],
    ["bags", "kg", 0, true],
    ["yard", "yards", 1, false],
    ["Bags", "bag", 1, false],
    ["bag", "", 1, false],
    ["bag", "pcs", 1, false],
    ["carton", "piece", 1, false],
    ["bag", "g", 1000, false],
  ] as const)("%s into %s (uses %d) asks %s", (line, stock, uses, ask) => {
    expect(askUses(line, stock, uses)).toBe(ask);
  });
  it("says the question, and checks the number", () => {
    expect(usesQuestion("g", "bags", "Rice")).toBe("How many g is one bag?");
    expect(usesQuestion("g", "", "Rice")).toBe("How many g is one Rice?");
    expect(singular("boxes")).toBe("box");
    expect(usesProblem("50,000")).toBe("");
    expect(usesProblem("")).toBe("");
    expect(usesProblem("half")).toBe("A whole number, like 1000.");
    expect(usesProblem("0")).toBe("A whole number, like 1000.");
  });
  it("sends uses with a stock line", () => {
    expect(lineBody({ treatment: "stock", stockItemId: "si-rice", uses: 50_000, label: "" }, { stockItemId: "si-rice" })).toEqual({
      treatment: "stock",
      stock_item_id: "si-rice",
      uses: 50_000,
    });
  });
});
