import { describe, expect, it } from "vitest";
import { customFieldKey, loadSavedSources, savedExpr, savedSources, SAVED_GROUP } from "./savedSources";
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
    { key: "payee", label: "Payee", type: "bank_account" },
    { key: "note", label: "Its own note", type: "text" },
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
    // A bank account's number is never read by a run.
    expect(out.filter((s) => s.label.startsWith("@supplier")).map((s) => s.expr)).toEqual([
      "=saved.supplier.title",
      "=saved.supplier.account_name",
      "=saved.supplier.bank_name",
      "=saved.supplier.bank_code",
      "=saved.supplier.note",
    ]);
  });

  it("gives a place, a person, a provider and a file their own fields", () => {
    const labels = (k: SavedThing["kind"]) =>
      savedSources([thing({ name: "x1", kind: k })])
        .map((s) => s.label.replace("@x1 · ", ""))
        .slice(1);
    expect(labels("place")).toEqual(["Address", "Street and number", "Area", "Landmark", "Phone", "Note"]);
    expect(labels("person")).toEqual(["Phone", "Email", "Address", "Birthday", "Note"]);
    expect(labels("provider_account")).toEqual(["Provider", "Number with them", "Name on the account", "What it is", "Note"]);
    expect(labels("file")).toEqual(["File name", "File type", "The file, for a message to send", "Note"]);
  });

  it("gives a made-up kind's fields, but for sensitive ones, files, bank accounts and those never shared", () => {
    const out = savedSources([thing({ name: "van", title: "Van", kind: "custom", kind_id: "k1" })], [car]);
    expect(out.map((s) => s.expr)).toEqual(["=saved.van.title", "=saved.van.plate", "=saved.van.field_note", "=saved.van.note"]);
    expect(out[1]?.label).toBe("@van · Plate");
    expect(out[2]?.label).toBe("@van · Its own note");
    // Its kind not read: its title and note only.
    expect(savedSources([thing({ name: "van", kind: "custom", kind_id: "k9" })], [car]).map((s) => s.expr)).toEqual(["=saved.van.title", "=saved.van.note"]);
  });

  it("reads a made-up field keyed as every thing's field as field_<key>", () => {
    expect(["name", "title", "kind", "note", "plate"].map(customFieldKey)).toEqual(["field_name", "field_title", "field_kind", "field_note", "plate"]);
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
    expect((await loadSavedSources(get)).map((s) => s.label)).toEqual(["@van · Title", "@van · Plate", "@van · Its own note", "@van · Note"]);
    expect(asked.sort()).toEqual(["/v1/saved", "/v1/saved-kinds"]);
    // The kinds fail: the things still show, with their titles.
    const noKinds = <T>(path: string): Promise<T> => (path === "/v1/saved" ? get<T>(path) : Promise.reject(new Error("no")));
    expect(await loadSavedSources(noKinds)).toHaveLength(2);
    expect(await loadSavedSources(() => Promise.reject(new Error("offline")))).toEqual([]);
  });
});

describe("a made-up kind's field named file", () => {
  it("is read as field_file: only a saved file is a message's file", () => {
    expect(customFieldKey("file")).toBe("field_file");
    expect(customFieldKey("colour")).toBe("colour");
  });
});
