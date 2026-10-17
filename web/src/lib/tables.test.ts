import { describe, expect, it } from "vitest";
import { cellText, cellValue, columnErrors, csvCell, exampleStep, importError, nameError, newColumn, parseCSV, personalByDefault, toCSV, type Column, type Table } from "./tables";

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
    expect(header).toEqual(["name", "phone", "balance_kobo"]);
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

  it("writes the example step", () => {
    const t: Table = { id: "t", name: "debtors", title: "", description: "", columns: cols, postgres_table: "public.debtors", connection: "data_tables", created_by: "", created_at: "", updated_at: "" };
    const step = exampleStep(t);
    expect(step).toContain('"connection": "data_tables"');
    expect(step).toContain('"table": "debtors"');
    expect(step).toContain('"column": "balance_kobo"');
    expect(JSON.parse(step)).toHaveProperty("action", "read_rows");
  });
});
