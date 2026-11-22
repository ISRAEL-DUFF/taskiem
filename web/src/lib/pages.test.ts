import { describe, expect, it } from "vitest";
import { columnOf, filterFits, filtersParam, freeId, grouped, layout, move, newPart, partWords, publishWords, rowsWord, toneClass, whoWords, withUses, type DrawnPart, type PageDoc } from "./pages";
import type { Table } from "./tables";

const tickets: Table = {
  id: "t1",
  name: "tickets",
  title: "Tickets",
  description: "",
  postgres_table: "public.tickets",
  connection: "data_tables",
  created_by: "",
  created_at: "",
  updated_at: "",
  columns: [
    { name: "title", title: "Title", type: "text", required: true, personal: false },
    { name: "status", title: "Status", type: "text", required: false, personal: false },
    { name: "email", title: "Email", type: "email", required: false, personal: true },
    { name: "amount_kobo", title: "Amount", type: "integer", required: false, personal: false },
    { name: "opened", title: "Opened", type: "date", required: false, personal: false },
  ],
};

describe("pages", () => {
  it("draws figures side by side, the main one apart", () => {
    const parts = [
      { id: "a", type: "figure", lead: true },
      { id: "b", type: "figure" },
      { id: "c", type: "figure" },
      { id: "d", type: "list" },
      { id: "e", type: "figure" },
    ] as DrawnPart[];
    expect(layout(parts).map((p) => (Array.isArray(p) ? p.map((x) => x.id).join("+") : p.id))).toEqual(["a", "b+c", "d", "e"]);
  });

  it("puts rows under their groups, in the server's order, leaving out empty ones", () => {
    const part = {
      id: "l",
      type: "list",
      groups: [
        { key: "open", label: "Open" },
        { key: "doing", label: "Doing" },
        { key: "", label: "Other" },
      ],
      rows: [
        { id: "1", group: "" },
        { id: "2", group: "open" },
      ],
    } as DrawnPart;
    expect(grouped(part).map((g) => `${g.label}:${g.rows.map((r) => r.id).join()}`)).toEqual(["Open:2", "Other:1"]);
  });

  it("says who may open a page and what publishing shows", () => {
    expect(whoWords([])).toBe("Everyone in the business");
    expect(whoWords(["support", "shift_manager"])).toBe("Owners and support and shift manager");
    expect(publishWords("Tickets", ["support"], ["Tickets: Title, Status"])).toEqual(["Owners and support will be able to open Tickets.", "It shows Tickets: Title, Status."]);
    expect(rowsWord(1)).toBe("1 row");
    expect(rowsWord(1200)).toBe("1,200 rows");
  });

  it("names tones, filters and columns", () => {
    expect(toneClass("bad")).toBe("danger");
    expect(toneClass(undefined)).toBe("muted");
    expect(filtersParam({ q: "", st: "open" })).toBe(`filters=${encodeURIComponent('{"st":"open"}')}`);
    expect(filtersParam({})).toBe("");
    expect(columnOf("=row.status")).toBe("status");
    expect(columnOf("=row.a + row.b")).toBe("");
  });

  it("adds parts in an owner's words", () => {
    const list = newPart("list", tickets, []);
    expect(list.item?.title).toBe("=row.title");
    expect(list.detail?.lines?.length).toBe(5);
    expect(partWords(list, [tickets])).toBe("List of Tickets");
    const sum = newPart("sum", tickets, []);
    expect(sum.of).toBe("amount_kobo");
    expect(partWords(sum, [tickets])).toBe("Total of Amount from Tickets");
    const count = newPart("count", tickets, [{ id: "count" }]);
    expect(count.id).toBe("count_2");
    expect(partWords({ ...list, group: { column: "status" } }, [tickets])).toBe("List of Tickets, in groups by Status");
    expect(freeId([], "9 bad")).toBe("bad");
    expect(move([1, 2, 3], 0, -1)).toEqual([1, 2, 3]);
    expect(move([1, 2, 3], 0, 1)).toEqual([2, 1, 3]);
  });

  it("sets each list and table to use the filters that fit its table", () => {
    expect(filterFits({ id: "m", type: "mine", column: "email" }, tickets)).toBe(true);
    expect(filterFits({ id: "d", type: "dates", column: "title" }, tickets)).toBe(false);
    const doc: PageDoc = {
      page: "page/v1",
      name: "tickets",
      title: "Tickets",
      filters: [
        { id: "q", type: "search" },
        { id: "d", type: "dates", column: "opened" },
        { id: "x", type: "choice", column: "nope", options: [{ value: "a" }] },
      ],
      blocks: [newPart("count", tickets, []), newPart("list", tickets, [])],
    };
    const out = withUses(doc, [tickets]);
    expect(out.blocks[0]?.source?.uses).toBeUndefined();
    expect(out.blocks[1]?.source?.uses).toEqual(["q", "d"]);
  });
});
