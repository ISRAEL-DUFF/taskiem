import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { approveAction, buttons, figure, heading, list, page, signalAction, status, table, toolAction, type PageBlock } from "./page.js";

// The page builder (docs/spec/pages.md §12): a page/v1 document written in
// code, its expressions compiled to CEL, its part kinds those the schema
// has.
interface Schema {
  $defs: { block: { properties: { type: { enum: string[] } } }; source: { properties: Record<string, unknown> } };
}
const schema = JSON.parse(readFileSync(new URL("../../schemas/page-v1.schema.json", import.meta.url), "utf8")) as Schema;

const kinds = { heading: true, figure: true, list: true, table: true, buttons: true, status: true } satisfies Record<PageBlock["type"], true>;

describe("page builder", () => {
  it("has the schema's part kinds and source fields", () => {
    expect(Object.keys(kinds).sort()).toEqual([...schema.$defs.block.properties.type.enum].sort());
    expect(Object.keys(schema.$defs.source.properties).sort()).toEqual(["filters", "inputs", "order", "output", "records", "table", "tool", "uses"]);
  });

  it("writes a page, its expressions as CEL", () => {
    const doc = page(
      { name: "deliveries", title: "Deliveries today", roles: ["manager"], place: { area: "storefront" }, filters: [{ id: "when", type: "dates", column: "offered_at" }] },
      heading("head", "Today"),
      figure("owed", { label: "Owed to riders", value: "sum", of: "owed", naira: true, source: { records: "deliveries" } }),
      list("today", {
        source: { records: "deliveries", uses: ["when"] },
        item: { title: ({ row }) => row.order + " · " + row.rider, sub: "=row.customer", state: { text: ({ row }) => row.status } },
        actions: [
          signalAction("Confirm", { workflow: "Confirm deliveries", name: "confirm", to: ({ row }) => row.id }, { when: ({ row }) => row.status === "delivered" }),
          toolAction("Note", "add-note", { ref: ({ row }) => row.id, kind: "delivery" }, { once: true }),
        ],
        status: { workflow: "Confirm deliveries", to: ({ row }) => row.id },
      }),
      table("all", { source: { table: "tickets" }, columns: [{ column: "title" }, { column: "done", edit: true }], actions: [approveAction("Approve", { workflow: "Refunds", run: "=row.run", step: "ok" })] }),
      buttons("new", [toolAction("New delivery", "new-delivery")]),
      status("state", "Confirm deliveries"),
    );
    expect(doc.page).toBe("page/v1");
    const l = doc.blocks[2] as Extract<PageBlock, { type: "list" }>;
    expect(l.item.title).toMatch(/^=row\.order \+ ["'] · ["'] \+ row\.rider$/);
    expect(l.item.sub).toBe("=row.customer");
    expect(l.actions?.[0]?.signal).toEqual({ workflow: "Confirm deliveries", name: "confirm", to: "=row.id" });
    expect(l.actions?.[0]?.when).toMatch(/^=row\.status == ["']delivered["']$/);
    expect(l.actions?.[1]).toEqual({ label: "Note", tool: "add-note", inputs: { ref: "=row.id", kind: "delivery" }, once: true });
    expect(l.status).toEqual({ workflow: "Confirm deliveries", to: "=row.id" });
    expect(JSON.parse(JSON.stringify(doc))).toEqual(doc);
  });

  it("refuses an expression that is not one", () => {
    expect(() => list("x", { source: { table: "t" }, item: { title: () => { while (true) { /* never */ } } } })).toThrow(/x.item.title/);
    expect(() => page({ name: "empty", title: "Empty" })).toThrow(/at least one part/);
  });
});
