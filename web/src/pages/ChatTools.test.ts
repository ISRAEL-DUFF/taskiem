import { describe, expect, it } from "vitest";
import { mayEdit } from "./ChatTools";

describe("who may change what a tool shows", () => {
  const only = (...perms: string[]) => (p: string) => perms.includes(p);
  it("lets those who may edit tables change products, stock and rows", () => {
    expect(mayEdit("products", only("table.read"))).toBe(false);
    expect(mayEdit("products", only("table.read", "table.edit"))).toBe(true);
    expect(mayEdit("tables", only("table.edit"))).toBe(true);
  });
  it("needs both for services, and storefront.manage for prices and orders", () => {
    expect(mayEdit("services", only("table.edit"))).toBe(false);
    expect(mayEdit("services", only("table.edit", "storefront.manage"))).toBe(true);
    expect(mayEdit("orders", only("storefront.manage"))).toBe(true);
    expect(mayEdit("prices", only("table.edit"))).toBe(false);
  });
});
