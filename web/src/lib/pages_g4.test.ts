import { describe, expect, it } from "vitest";
import {
  barPath,
  chartLayout,
  laneStep,
  moveExpr,
  moveFrom,
  moveInputs,
  moveWords,
  newPart,
  partWords,
  withUses,
  type ChartPoint,
  type PageDoc,
} from "./pages";
import type { Table } from "./tables";

const tickets: Table = {
  id: "t1",
  name: "tickets",
  title: "Tickets",
  description: "",
  columns: [
    { name: "title", title: "Title", type: "text", required: true, personal: false },
    { name: "status", title: "Status", type: "text", required: false, personal: false },
    { name: "amount_kobo", title: "Amount", type: "integer", required: false, personal: false },
    { name: "opened", title: "Opened", type: "date", required: false, personal: false },
  ],
  postgres_table: "",
  connection: "",
  created_by: "",
  created_at: "",
  updated_at: "",
};

describe("a board (G4)", () => {
  it("moves by the keyboard one column at a time, staying at either end", () => {
    const lanes = ["open", "doing", "done"];
    expect(laneStep(lanes, "open", 1)).toBe("doing");
    expect(laneStep(lanes, "done", 1)).toBe("done");
    expect(laneStep(lanes, "open", -1)).toBe("open");
    expect(laneStep(lanes, "gone", 1)).toBe("open");
  });
  it("says each step to a screen reader", () => {
    expect(moveWords("picked", "Printer jam", "Open")).toMatch(/^Printer jam picked up, in Open\. Left and right arrows/);
    expect(moveWords("over", "Printer jam", "Doing", "Open")).toBe("Move Printer jam to Doing?");
    expect(moveWords("over", "Printer jam", "Open", "Open")).toBe("Printer jam stays in Open.");
    expect(moveWords("dropped", "Printer jam", "Doing")).toBe("Moving Printer jam to Doing.");
  });
  it("is made by the editor over a status column, and the move's values read the column a card goes to", () => {
    const b = newPart("board", tickets, []);
    expect(b.board?.column).toBe("status");
    expect(b.board?.columns.map((c) => c.label)).toEqual(["Open", "Doing", "Done"]);
    expect(b.item?.title).toBe("=row.title");
    const tool = { command: "move-ticket", name: "Move ticket", answers: false, outputs: [], inputs: [{ name: "ref", title: "Ticket", personal: false }, { name: "status", title: "Status", personal: false }, { name: "note", title: "Note", personal: false }] };
    expect(moveInputs(tool, "status")).toEqual({ ref: "=row.id", status: "=to" });
    expect(moveFrom("=to")).toEqual({ from: "to" });
    expect(moveExpr({ from: "to" })).toBe("=to");
    expect(moveExpr({ from: "column", column: "title" })).toBe("=row.title");
    const doc: PageDoc = { page: "page/v1", name: "desk", title: "Desk", filters: [{ id: "q", type: "search" }], blocks: [b] };
    expect(withUses(doc, [tickets]).blocks[0]?.source?.uses).toEqual(["q"]);
    expect(partWords({ ...b, board: { column: "status", columns: b.board?.columns ?? [], move: { label: "Move", tool: "move-ticket" } } }, [tickets])).toBe(
      "Board of Tickets in columns by Status: Open, Doing, Done; moving a card opens /move-ticket",
    );
  });
});

describe("a chart (G4)", () => {
  const pt = (share: number): ChartPoint => ({ label: "", long: "", value: 0, shown: "", share, tick: true });
  it("is made by the editor by week of a date column, adding up a number", () => {
    const c = newPart("chart", tickets, []);
    expect(c.chart).toEqual({ kind: "bars", by: "opened", period: "week", last: 8, of: "amount_kobo" });
    expect(partWords(c, [tickets])).toBe("Bars of Amount added up in Tickets, by week");
    const doc: PageDoc = { page: "page/v1", name: "desk", title: "Desk", filters: [{ id: "q", type: "search" }], blocks: [c] };
    expect(withUses(doc, [tickets]).blocks[0]?.source?.uses).toBeUndefined();
  });
  it("draws the server's places as pixels, bars at most 24 wide, from nought", () => {
    const m = chartLayout([pt(0.5), pt(1), pt(0)], 0, 300, 100);
    expect(m.map((x) => x.cx)).toEqual([50, 150, 250]);
    expect(m[0]).toMatchObject({ w: 24, top: 50, h: 50, up: true });
    expect(m[1]).toMatchObject({ top: 0, h: 100 });
    expect(m[2]?.h).toBe(0);
    // Below nought: the bar hangs from it.
    const n = chartLayout([pt(0)], 0.25, 100, 100);
    expect(n[0]).toMatchObject({ top: 75, h: 25, up: false });
  });
  it("rounds a bar's data end, never its foot", () => {
    expect(barPath(10, 20, 20, 50, true)).toBe("M10,70V24Q10,20 14,20H26Q30,20 30,24V70Z");
    expect(barPath(10, 20, 20, 50, false)).toBe("M10,20V66Q10,70 14,70H26Q30,70 30,66V20Z");
    expect(barPath(10, 20, 20, 0, true)).toBe("");
  });
});
