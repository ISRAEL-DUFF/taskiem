import { describe, expect, it } from "vitest";
import {
  KEPT_MESSAGE,
  KEPT_OWN_NOTE,
  cellText,
  cellValue,
  columnErrors,
  csvCell,
  exampleStep,
  importError,
  isKept,
  keptNotice,
  matchHeaders,
  nameError,
  newColumn,
  parseCSV,
  personalByDefault,
  tableActions,
  tableControls,
  tableFromCSV,
  toCSV,
  type Column,
  type Table,
} from "./tables";

const cols: Column[] = [
  { name: "name", title: "Name", type: "text", required: true, personal: false },
  { name: "phone", title: "", type: "phone", required: false, personal: true },
  { name: "balance_kobo", title: "", type: "integer", required: false, personal: false },
];

describe("data tables", () => {
  it("checks names", () => {
    expect(nameError("debtors")).toBeNull();
    expect(nameError("balance_kobo")).toBeNull();
    for (const bad of ["Debtors", "1a", "a-b", "id", "created_at", "xb_a", "pg_x", "", "a".repeat(64)]) expect(nameError(bad), bad).not.toBeNull();
  });

  it("checks columns together", () => {
    expect(columnErrors(cols)).toEqual([]);
    expect(columnErrors([])).toEqual([{ index: -1, message: "A table needs at least one column." }]);
    expect(columnErrors([...cols, { ...newColumn(), name: "name" }]).map((e) => e.index)).toEqual([3]);
    expect(columnErrors(cols, 2)[0]?.message).toContain("at most 2");
    expect(columnErrors([{ ...newColumn(), name: "Bad" }])[0]?.message).toContain("lower-case");
    expect(personalByDefault("phone")).toBe(true);
    expect(personalByDefault("text")).toBe(false);
  });

  it("turns typed cells into values", () => {
    expect(cellValue("text", "")).toBe("");
    expect(cellValue("integer", "")).toBeNull();
    expect(cellValue("integer", " 12 ")).toBe(12);
    expect(cellValue("number", "12.5")).toBe(12.5);
    expect(cellValue("integer", "lots")).toBe("lots");
    expect(cellValue("boolean", "Yes")).toBe(true);
    expect(cellValue("boolean", "0")).toBe(false);
    expect(cellValue("json", '{"a":1}')).toEqual({ a: 1 });
    expect(cellValue("json", "plain")).toBe("plain");
    expect(cellValue("phone", "0801 234 5678")).toBe("0801 234 5678");
    expect(cellText(null)).toBe("");
    expect(cellText(true)).toBe("true");
    expect(cellText({ a: 1 })).toBe('{"a":1}');
  });

  it("parses CSV with quotes, doubled quotes and newlines", () => {
    const { header, rows } = parseCSV('﻿Name,phone,balance_kobo\r\n"Ada, Mrs",2348012345678,500\n"Say ""hi""\nthere",,0\n\n,,\n');
    expect(header).toEqual(["Name", "phone", "balance_kobo"]); // as written: matched loosely later
    expect(rows).toEqual([
      ["Ada, Mrs", "2348012345678", "500"],
      ['Say "hi"\nthere', "", "0"],
    ]);
    expect(parseCSV("")).toEqual({ header: [], rows: [] });
    expect(parseCSV("a,b")).toEqual({ header: ["a", "b"], rows: [] });
  });

  it("serialises CSV", () => {
    expect(csvCell("plain")).toBe("plain");
    expect(csvCell('a "b", c')).toBe('"a ""b"", c"');
    expect(csvCell(null)).toBe("");
    const text = toCSV(["id", "name"], [{ id: 1, name: "Ada, Mrs" }, { id: 2, name: null }]);
    expect(text).toBe('id,name\r\n1,"Ada, Mrs"\r\n2,\r\n');
    expect(parseCSV(text).rows).toEqual([
      ["1", "Ada, Mrs"],
      ["2", ""],
    ]);
  });

  it("checks an import's header", () => {
    expect(importError(["name", "phone"], cols)).toBeNull();
    expect(importError(["id", "name", "created_at"], cols)).toBeNull();
    expect(importError([], cols)).toContain("header");
    expect(importError(["name", "colour"], cols)).toContain("colour");
    expect(importError(["id"], cols)).toContain("none");
  });

  it("matches headers loosely, and asks about the rest (N5)", () => {
    const withTitles = cols.map((c) => (c.name === "balance_kobo" ? { ...c, title: "Balance owed" } : c));
    expect(matchHeaders(["Name", "PHONE", "Balance Owed", "Notes"], withTitles)).toEqual({
      matched: { Name: "name", PHONE: "phone", "Balance Owed": "balance_kobo" },
      unmatched: ["Notes"],
    });
    expect(importError(["Name", "Notes"], withTitles)).toContain("“Notes”");
    expect(importError(["Name", "Notes"], withTitles, { Notes: "" })).toBeNull();
  });

  it("makes a table from a file's header and values", () => {
    const t = tableFromCSV("My customers", ["Name", "Phone number", "Amount owed", "Joined"], [
      ["Ada", "0803 123 4567", "5,000", "2026-01-05"],
      ["Bola", "+2348031234567", "300", "2026-02-01"],
    ]);
    expect(t.name).toBe("my_customers");
    expect(t.columns).toEqual([
      { name: "name", title: "Name", type: "text" },
      { name: "phone_number", title: "Phone number", type: "phone" },
      { name: "amount_owed", title: "Amount owed", type: "number" },
      { name: "joined", title: "Joined", type: "date" },
    ]);
  });

  it("writes the example step", () => {
    const t: Table = { id: "t", name: "debtors", title: "", description: "", columns: cols, postgres_table: "public.debtors", connection: "data_tables", created_by: "", created_at: "", updated_at: "" };
    const step = exampleStep(t);
    expect(step).toContain('"connection": "data_tables"');
    expect(step).toContain('"table": "debtors"');
    expect(step).toContain('"column": "balance_kobo"');
    expect(JSON.parse(step)).toHaveProperty("action", "read_rows");
  });

  it("shows a kept table read-only, with why", () => {
    const ordinary = { kept_by: null };
    const kept = { kept_by: "money_log" };
    expect(isKept(ordinary)).toBe(false);
    expect(isKept({})).toBe(false);
    expect(isKept(kept)).toBe(true);
    // An editor gets every control on an ordinary table, none that change a kept one.
    expect(tableControls(ordinary, true)).toEqual({ addRow: true, editRows: true, importCSV: true, columns: true, deleteTable: true, export: true, search: true });
    expect(tableControls(kept, true)).toEqual({ addRow: false, editRows: false, importCSV: false, columns: false, deleteTable: false, export: true, search: true });
    expect(tableControls(ordinary, false).addRow).toBe(false);
    // The sentence, and where it can still be edited when the project is the business's own.
    expect(keptNotice(ordinary, { own: true })).toBeNull();
    expect(keptNotice(kept, { own: false })).toEqual([KEPT_MESSAGE]);
    expect(keptNotice(kept, null)).toEqual([KEPT_MESSAGE]);
    expect(keptNotice(kept, { own: true })).toEqual([KEPT_MESSAGE, KEPT_OWN_NOTE]);
    expect(KEPT_MESSAGE).toContain("Corrections are new rows.");
    expect(KEPT_OWN_NOTE).toContain("outside Taskiem");
    // Workflows only read it.
    expect(tableActions(kept).map((a) => a.action)).toEqual(["read_rows", "row_changed (trigger)"]);
    expect(tableActions(ordinary).map((a) => a.action)).toContain("insert_rows");
  });
});
