import { describe, expect, it } from "vitest";
import { mayEdit, startRow } from "./ChatTools";
import type { ToolRow } from "../lib/tools";

describe("who may change what a tool shows", () => {
  // As auth.tsx: "a|b" is either of them.
  const only = (...perms: string[]) => (p: string) => p.split("|").some((x) => perms.includes(x));
  it("lets those who may edit tables change products, stock and rows", () => {
    expect(mayEdit("products", only("table.read"))).toBe(false);
    expect(mayEdit("products", only("table.read", "table.edit"))).toBe(true);
    expect(mayEdit("tables", only("table.edit"))).toBe(true);
  });
  it("needs both for services, and storefront.manage for prices and orders", () => {
    expect(mayEdit("services", only("table.edit"))).toBe(false);
    expect(mayEdit("services", only("table.edit", "storefront.manage"))).toBe(true);
    expect(mayEdit("orders", only("storefront.manage"))).toBe(true);
    // A cashier works the orders without the storefront's settings.
    expect(mayEdit("orders", only("order.handle"))).toBe(true);
    expect(mayEdit("orders", only("table.edit"))).toBe(false);
    expect(mayEdit("prices", only("table.edit"))).toBe(false);
  });
});

describe("the row a bill's card opens", () => {
  const bill = (id: string, link: string, reference: string): ToolRow =>
    ({ id, name: reference, sub: "", href: "", tags: [], link, data: { reference } }) as ToolRow;
  // Two suppliers each number their own invoices: both sent INV-0001.
  const rows = [bill("b-bayo", "bayo", "INV-0001"), bill("b-grace", "grace", "INV-0001"), bill("b-grace-2", "grace", "INV-0002")];
  it("opens the number from this business, not another's with the same number", () => {
    expect(startRow(rows, "INV-0001", "grace")?.id).toBe("b-grace");
    expect(startRow(rows, "INV-0001", "bayo")?.id).toBe("b-bayo");
    expect(startRow(rows, "INV-0002", "grace")?.id).toBe("b-grace-2");
  });
  it("opens by id anywhere, and not a number shared by two outside a conversation", () => {
    expect(startRow(rows, "b-grace")?.id).toBe("b-grace");
    expect(startRow(rows, "INV-0001")).toBeUndefined();
    expect(startRow(rows, "INV-0002")?.id).toBe("b-grace-2");
    expect(startRow(rows, "INV-0009", "grace")).toBeUndefined();
  });
});
