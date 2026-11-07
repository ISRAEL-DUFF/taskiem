import { describe, expect, it } from "vitest";
import { mayEdit } from "./ChatTools";

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
