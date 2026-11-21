import { describe, expect, it } from "vitest";
import { loadSavedSources, savedExpr, savedSources, SAVED_GROUP } from "./savedSources";
import { segmentsOf, valueOf } from "./fieldtext";
import { rulesExpr, rulesOf } from "./plaincanvas";
import type { MadeKind, SavedThing } from "./saved";

const thing = (x: Partial<SavedThing>): SavedThing => ({
  id: x.name ?? "1",
  owner: "business",
  name: "price-list",
  title: "Price list",
  kind: "text",
  hint: "",
  details: {},
  check: { state: "none", account_name: null, via: null, at: null, problem: null },
  to_change: true,
  created_at: "2026-11-20T10:00:00Z",
  updated_at: "2026-11-20T10:00:00Z",
  ...x,
});

const car: MadeKind = {
  id: "k1",
  name: "Car",
  count: 1,
  fields: [
    { key: "plate", label: "Plate", type: "text" },
    { key: "vin", label: "VIN", type: "text", sensitive: true },
    { key: "papers", label: "Papers", type: "file" },
    { key: "secret_code", label: "Secret code", type: "text", never_share: true },
  ],
};

describe("saved things as a step's values", () => {
  it("reads a name with _ for -, or in brackets when it starts with a number", () => {
    expect(savedExpr("price-list", "body")).toBe("=saved.price_list.body");
    expect(savedExpr("bayo", "account_number")).toBe("=saved.bayo.account_number");
    expect(savedExpr("2go", "phone")).toBe('=saved["2go"].phone');
  });

  it("lists each thing's fields as a run reads them, under Saved", () => {
    const out = savedSources([thing({}), thing({ name: "supplier", title: "Supplier", kind: "bank_account" })]);
    expect(out.every((s) => s.group === SAVED_GROUP)).toBe(true);
    expect(out.slice(0, 3)).toEqual([
      { label: "@price-list · Title", expr: "=saved.price_list.title", group: "Saved" },
      { label: "@price-list · Text", expr: "=saved.price_list.body", group: "Saved" },
      { label: "@price-list · Note", expr: "=saved.price_list.note", group: "Saved" },
    ]);
    expect(out.map((s) => s.label)).toContain("@supplier · Account number");
    expect(out.find((s) => s.label === "@supplier · Account number")?.expr).toBe("=saved.supplier.account_number");
  });

  it("gives a place, a person, a provider and a file their own fields", () => {
    const labels = (k: SavedThing["kind"]) =>
      savedSources([thing({ name: "x1", kind: k })])
        .map((s) => s.label.replace("@x1 · ", ""))
        .slice(1);
    expect(labels("place")).toEqual(["Address", "Street and number", "Area", "Landmark", "Phone", "Note"]);
    expect(labels("person")).toEqual(["Phone", "Email", "Address", "Birthday", "Note"]);
    expect(labels("provider_account")).toEqual(["Provider", "Number with them", "Name on the account", "What it is", "Note"]);
    expect(labels("file")).toEqual(["File name", "File type", "Note"]);
  });

  it("gives a made-up kind's fields, but for files and those never shared", () => {
    const out = savedSources([thing({ name: "van", title: "Van", kind: "custom", kind_id: "k1" })], [car]);
    expect(out.map((s) => s.expr)).toEqual(["=saved.van.title", "=saved.van.plate", "=saved.van.vin"]);
    expect(out[1]?.label).toBe("@van · Plate");
    // Its kind not read: its title only.
    expect(savedSources([thing({ name: "van", kind: "custom", kind_id: "k9" })], [car])).toHaveLength(1);
  });

  it("never offers a person's own", () => {
    expect(savedSources([thing({ owner: "mine" })])).toEqual([]);
  });

  it("is a field a message box and a rule can hold, either form", () => {
    for (const expr of ["=saved.price_list.body", '=saved["2go"].phone']) {
      const segs = segmentsOf(`='Prices: ' + string(${expr.slice(1)})`, () => "x");
      expect(segs).toEqual([{ text: "Prices: " }, { expr, label: "x" }]);
      expect(valueOf(segs ?? [])).toBe(`='Prices: ' + string(${expr.slice(1)})`);
      const rule = rulesExpr({ rules: [{ left: expr, op: "contains", right: "rice" }], all: true });
      expect(rulesOf(rule)?.rules[0]?.left).toBe(expr);
    }
  });

  it("reads the business's list and kinds, and none when the list fails", async () => {
    const asked: string[] = [];
    const get = <T>(path: string): Promise<T> => {
      asked.push(path);
      if (path === "/v1/saved") return Promise.resolve({ saved: [thing({ name: "van", kind: "custom", kind_id: "k1" })], limit: 50 } as T);
      return Promise.resolve({ kinds: [car], starters: [], limit: 10, to_change: true } as T);
    };
    expect((await loadSavedSources(get)).map((s) => s.label)).toEqual(["@van · Title", "@van · Plate", "@van · VIN"]);
    expect(asked.sort()).toEqual(["/v1/saved", "/v1/saved-kinds"]);
    // The kinds fail: the things still show, with their titles.
    const noKinds = <T>(path: string): Promise<T> => (path === "/v1/saved" ? get<T>(path) : Promise.reject(new Error("no")));
    expect(await loadSavedSources(noKinds)).toHaveLength(1);
    expect(await loadSavedSources(() => Promise.reject(new Error("offline")))).toEqual([]);
  });
});
