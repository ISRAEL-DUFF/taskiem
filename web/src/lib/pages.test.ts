import { describe, expect, it } from "vitest";
import { canSort, columnOf, columnsFor, conditionCount, filterFits, importWords, pageFileName, readPageFile, versionTitle, filtersParam, freeId, grouped, keptFilters, layout, move, newPart, partWords, publishWords, rowsWord, toneClass, viewWithFilters, whoWords, withUses, type DrawnFilter, type DrawnPage, type DrawnPart, type PageDoc, type PageDetail, type PageVersion } from "./pages";
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
  it("drops saved filters the page no longer has, and a choice it no longer offers", () => {
    const filters: DrawnFilter[] = [
      { id: "q", type: "search", label: "Search", value: "" },
      { id: "st", type: "choice", label: "Show", value: "", options: [{ value: "open", label: "Open" }] },
    ];
    expect(keptFilters({ q: "ink", st: "open", gone: "x", empty: "" }, filters)).toEqual({ q: "ink", st: "open" });
    expect(keptFilters({ st: "closed" }, filters)).toEqual({});
  });

  it("draws a page again once without saved filters the server refuses", async () => {
    const page = { filters: [], blocks: [] } as unknown as DrawnPage;
    const asked: string[] = [];
    let stale = 0;
    const refused = Object.assign(new Error("This page has no filter gone."), { status: 400, body: { code: "page_problems" } });
    const load = (q: string) => {
      asked.push(q);
      return q ? Promise.reject(refused) : Promise.resolve(page);
    };
    await expect(viewWithFilters(load, { gone: "x" }, () => stale++)).resolves.toBe(page);
    expect(asked).toEqual([filtersParam({ gone: "x" }), ""]);
    expect(stale).toBe(1);
    // Any other refusal is said as it is, and nothing is retried.
    const down = Object.assign(new Error("down"), { status: 500, body: {} });
    asked.length = 0;
    await expect(viewWithFilters(() => Promise.reject(down), { q: "a" }, () => stale++)).rejects.toBe(down);
    expect(stale).toBe(1);
  });

  it("never offers a choice or dates filter on a personal column", () => {
    const people = { ...tickets, columns: [...tickets.columns, { name: "born", title: "Born", type: "date", required: false, personal: true }, { name: "nick", title: "Nick", type: "text", required: false, personal: true }] } as typeof tickets;
    expect(filterFits({ id: "d", type: "dates", column: "born" }, people)).toBe(false);
    expect(filterFits({ id: "c", type: "choice", column: "nick" }, people)).toBe(false);
    expect(filterFits({ id: "d", type: "dates", column: "opened" }, people)).toBe(true);
  });

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
    // Mine on a phone column too (g-pages-mine).
    const withPhone: Table = { ...tickets, columns: [...tickets.columns, { name: "phone", title: "Phone", type: "phone", required: false, personal: true }] };
    expect(filterFits({ id: "m", type: "mine", column: "phone" }, withPhone)).toBe(true);
    expect(columnsFor(withPhone, "mine").map((c) => c.name)).toEqual(["email", "phone"]);
    expect(filterFits({ id: "m", type: "mine", column: "title" }, withPhone)).toBe(false);
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

  it("reads a page's file, says what making it found, and names versions", () => {
    expect(readPageFile("not json")).toEqual({ error: "This file is not a page saved from Taskiem: it could not be read." });
    expect(readPageFile('{"wd":"wd/v1"}')).toEqual({ error: "This file is not a page saved from Taskiem." });
    expect(readPageFile("[]")).toHaveProperty("error");
    const ok = readPageFile('{"page":"page/v1","name":"desk","title":"Desk","blocks":[]}');
    expect("document" in ok && ok.document.name).toBe("desk");
    expect(pageFileName("desk")).toBe("desk.page.json");
    const page = { name: "desk-2" } as PageDetail;
    expect(importWords({ page, missing: [], renamed_from: null })).toEqual([]);
    expect(importWords({ page, missing: ["This business has no table called tickets."], renamed_from: "desk" })).toEqual([
      "Another page was called desk, so this one is desk-2.",
      "Some of what it shows is not here yet:",
      "This business has no table called tickets.",
    ]);
    const doc: PageDoc = { page: "page/v1", name: "x", title: "X", blocks: [{ id: "a", type: "figure", source: { table: "t", filters: [{ column: "a", op: "eq", value: 1 }, { column: "b", op: "is_null" }] } }, { id: "h", type: "heading" }] };
    expect(conditionCount(doc)).toBe(2);
    expect(conditionCount(null)).toBe(0);
    const v: PageVersion = { version: 3, published_by: "Ada", published_at: "", summary: "", changes: [], roles: [], who: "", current: true };
    expect(versionTitle(v)).toBe("Version 3 (what people see now)");
    expect(versionTitle({ ...v, current: false })).toBe("Version 3");
  });

  it("offers a column's order only where the server says this viewer may", () => {
    expect(canSort({ sortable: false })).toBe(false);
    expect(canSort({ sortable: true })).toBe(true);
    expect(canSort({})).toBe(true);
  });
});
