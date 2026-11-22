import { describe, expect, it } from "vitest";
import {
  byLabel,
  detailsRequest,
  emptyThing,
  hadOf,
  priceProblem,
  profitWords,
  rowsOf,
  spendingLine,
  spendingName,
  spendingPath,
  statusRequest,
  statusSaid,
  statusSavable,
  thingLine,
  thingProblem,
  thingRequest,
  thingsOfLine,
  thingsPath,
  type Thing,
} from "./things";

const row = (p: Partial<Thing>): Thing => ({
  id: "t",
  name: "Generator",
  status: "have",
  status_at: "2026-11-01T10:00:00Z",
  created_by: "user:ada",
  created_at: "2026-11-01T10:00:00Z",
  ...p,
});

describe("Things we have", () => {
  it("asks for the list grouped by label, with those no longer had when shown", () => {
    expect(thingsPath(false)).toBe("/v1/things?group=label");
    expect(thingsPath(true)).toBe("/v1/things?all=true&group=label");
  });

  it("groups the rows under the server's groups, those no longer had after, Not labelled last", () => {
    const things = [
      row({ id: "a", label_id: "m", label: "Machines" }),
      row({ id: "b", label_id: "m", label: "Machines", status: "sold" }),
      row({ id: "c" }),
      row({ id: "d", label_id: "f", label: "Furniture", status: "broken" }),
      row({ id: "e", label_id: "m", label: "Machines" }),
    ];
    const groups = [
      { label_id: "m", label: "Machines", count: 2, total_kobo: 500 },
      { label_id: null, label: "", count: 1, total_kobo: 0 },
    ];
    const out = byLabel(things, groups);
    expect(out.map((g) => g.label)).toEqual(["Machines", "Furniture", "Not labelled"]);
    expect(out[0]?.rows.map((t) => t.id)).toEqual(["a", "e", "b"]);
    expect(out[1]?.count).toBe(0);
  });

  it("puts a merged label's rows in one section, matched by name as the server groups them", () => {
    // Equipment (lab-e) merged into Tools (lab-t): the rows keep their own
    // label_id, their label is the kept one's name; the server's one group
    // carries the first row's id.
    const things = [
      row({ id: "a", label_id: "lab-e", label: "Tools", cost_kobo: 300 }),
      row({ id: "b", label_id: "lab-t", label: "Tools", cost_kobo: 200 }),
      row({ id: "c", label_id: "lab-e", label: "tools", status: "sold", cost_kobo: 50 }),
    ];
    const groups = [{ label_id: "lab-e", label: "Tools", count: 2, total_kobo: 500 }];
    const out = byLabel(things, groups);
    expect(out.map((g) => g.label)).toEqual(["Tools"]);
    expect(out[0]?.rows.map((t) => t.id)).toEqual(["a", "b", "c"]);
    expect(out[0]).toMatchObject({ count: 2, total_kobo: 500 });
  });

  it("an order line's sections count and total its own rows still had", () => {
    const things = [
      row({ id: "a", label_id: "m", label: "Machines", cost_kobo: 400, purchase_line_id: "l1" }),
      row({ id: "b", label_id: "m", label: "Machines", cost_kobo: 400, purchase_line_id: "l1", status: "sold" }),
      row({ id: "c", label_id: "m", label: "Machines", cost_kobo: 900, purchase_line_id: "l2" }),
    ];
    const rows = rowsOf(things, "l1");
    const out = byLabel(rows, undefined);
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ label: "Machines", count: 1, total_kobo: 400 });
    expect(hadOf(rows)).toEqual({ count: 1, total_kobo: 400 });
    expect(thingsPath(false, "l 1")).toBe("/v1/things?line=l%201&all=true&group=label");
  });

  it("saves a price later on a row sold without one, for someone who manages money", () => {
    const soldBare = { status: "sold" as const };
    expect(statusSavable(soldBare, "sold", "", true)).toBe(false);
    expect(statusSavable(soldBare, "sold", "150,000", true)).toBe(true);
    expect(statusSavable(soldBare, "sold", "150,000", false)).toBe(false);
    expect(statusSavable({ status: "sold", sale_entry_id: "e1" }, "sold", "150,000", true)).toBe(false);
    expect(statusSavable({ status: "have" }, "have", "", true)).toBe(false);
    expect(statusSavable({ status: "have" }, "broken", "", false)).toBe(true);
  });

  it("links an order line kept track of to its rows once something came", () => {
    expect(thingsOfLine({ treatment: "track", id: "l1", received: 2 })).toBe("/money/things?line=l1");
    expect(thingsOfLine({ treatment: "track", id: "l1", received: 0 })).toBe("");
    expect(thingsOfLine({ treatment: "stock", id: "l1", received: 2 })).toBe("");
    const things = [row({ id: "a", purchase_line_id: "l1" }), row({ id: "b" })];
    expect(rowsOf(things, "l1").map((t) => t.id)).toEqual(["a"]);
    expect(rowsOf(things, null)).toHaveLength(2);
  });

  it("says a row's line plainly", () => {
    const d = (s: string) => s;
    expect(thingLine(row({ supplier: "Singer Ltd", received_at: "2026-11-02T08:00:00Z" }), d)).toBe(
      "From Singer Ltd · came 2026-11-02",
    );
    expect(thingLine(row({ status: "given", received_at: "2026-11-01T23:30:00Z" }), d)).toBe(
      "Came 2026-11-02 · Given away",
    );
  });
});

describe("Add something we have", () => {
  const today = "2026-11-22";
  it("needs a name, an amount and a day not after today", () => {
    expect(thingProblem(emptyThing(), today)).toBe("Say what it is.");
    expect(thingProblem({ ...emptyThing(), name: "Desk", cost: "abc" }, today)).toMatch(/amount/);
    expect(thingProblem({ ...emptyThing(), name: "Desk", came: "2026-11-23" }, today)).toMatch(/after today/);
    expect(thingProblem({ ...emptyThing(), name: "Desk", cost: "25,000" }, today)).toBe("");
  });

  it("sends only what was said", () => {
    expect(thingRequest({ ...emptyThing(), name: "  Office   desk " })).toEqual({ name: "Office desk" });
    expect(
      thingRequest(
        { name: "Generator", label: "Machines", supplierId: "s1", cost: "₦450,000", came: "2026-10-01", serial: " G-1 ", warranty: "2027-10-01", note: "In the shop" },
        "lab-1",
      ),
    ).toEqual({
      name: "Generator",
      label_id: "lab-1",
      supplier_id: "s1",
      cost_kobo: 45_000_000,
      received_at: "2026-10-01T12:00:00+01:00",
      serial: "G-1",
      warranty_until: "2027-10-01",
      note: "In the shop",
    });
  });
});

describe("what happened to it", () => {
  it("is a status, and a sale with a price is money in", () => {
    expect(statusRequest("broken")).toEqual({ status: "broken" });
    expect(statusRequest("sold")).toEqual({ status: "sold" });
    expect(statusRequest("sold", "150,000", "transfer")).toEqual({ status: "sold", sale_price_kobo: 15_000_000, sale_paid_how: "transfer" });
    expect(statusRequest("gone", "150,000")).toEqual({ status: "gone" });
  });

  it("refuses a price from someone who does not manage money, saying why", () => {
    expect(priceProblem("", false)).toBe("");
    expect(priceProblem("150,000", true)).toBe("");
    expect(priceProblem("0", true)).toMatch(/more than/);
    expect(priceProblem("150,000", false)).toMatch(/manages money/);
  });

  it("says the change back", () => {
    expect(statusSaid("Generator", "sold", 15_000_000)).toMatch(/^Generator sold for .*150,000: money in$/);
    expect(statusSaid("Generator", "given")).toBe("Generator: given away");
    expect(statusSaid("Generator", "have")).toBe("Generator is had again");
  });

  it("changes only the details that changed; an empty warranty clears it", () => {
    const t = { serial: "G-1", warranty_until: "2027-10-01", note: "" };
    expect(detailsRequest(t, { serial: "G-1", warranty: "2027-10-01", note: "" })).toEqual({});
    expect(detailsRequest(t, { serial: "G-2 ", warranty: "", note: "Fixed" })).toEqual({ serial: "G-2", warranty_until: "", note: "Fixed" });
  });
});

describe("spending by label", () => {
  it("asks for the period", () => {
    expect(spendingPath({ from: "2026-11-01", to: "2026-11-22" })).toBe("/v1/money/spending?from=2026-11-01&to=2026-11-22");
  });

  it("says each label's parts and names Not labelled", () => {
    const l = { label_id: "m", name: "Machines", kobo: 500, bought_to_sell_kobo: 0, bought_to_keep_kobo: 30_000_000, running_costs_kobo: 1_500_000, entries: 2 };
    expect(spendingLine(l)).toMatch(/^.*15,000 running costs · .*300,000 bought to keep$/);
    expect(spendingName(l)).toBe("Machines");
    expect(spendingName({ ...l, label_id: null, name: "" })).toBe("Not labelled");
  });

  it("says a loss as a loss", () => {
    expect(profitWords(1_000_000)).toMatch(/10,000 profit$/);
    expect(profitWords(-300)).toMatch(/3 loss$/);
  });
});
