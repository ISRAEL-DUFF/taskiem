import { describe, expect, it } from "vitest";
import { areaOffFor, areaOn, areaOfPage, neededBy, offWords, type AreaRow } from "./areas";

describe("areaOn", () => {
  it("shows an area unless the server says it is off", () => {
    expect(areaOn(undefined, "stock")).toBe(true);
    expect(areaOn({}, "stock")).toBe(true);
    expect(areaOn({ stock: true }, "stock")).toBe(true);
    expect(areaOn({ stock: false }, "stock")).toBe(false);
    expect(areaOn({ stock: false }, "money")).toBe(true);
  });
});

describe("areaOfPage", () => {
  it("finds the area of a page, longest address first", () => {
    expect(areaOfPage("/storefront")).toBe("storefront");
    expect(areaOfPage("/storefront/fronts/abc")).toBe("storefront");
    expect(areaOfPage("/storefront/riders/pay")).toBe("storefront");
    expect(areaOfPage("/storefront/orders")).toBe("orders");
    expect(areaOfPage("/storefront/orders/ref123")).toBe("orders");
    expect(areaOfPage("/storefront/stock")).toBe("stock");
    expect(areaOfPage("/storefront/suppliers/abc/prices")).toBe("stock");
    expect(areaOfPage("/trade-orders/1")).toBe("orders");
    expect(areaOfPage("/purchases/1")).toBe("stock");
    expect(areaOfPage("/invoices/new")).toBe("money");
    expect(areaOfPage("/invoices/")).toBe("money");
    expect(areaOfPage("/money/leaks")).toBe("money");
    expect(areaOfPage("/bills/9")).toBe("money");
    expect(areaOfPage("/settings/links")).toBe("links");
    expect(areaOfPage("/links/1/statement")).toBe("links");
  });
  it("leaves the always-on pages alone", () => {
    for (const p of ["/home", "/workflows", "/workflows/new", "/chat", "/settings", "/settings/payments", "/settings/areas", "/schools", "/forms", "/storefronts", "/invoicesx"]) {
      expect(areaOfPage(p)).toBeUndefined();
    }
  });
});

describe("areaOffFor", () => {
  it("names the off area a page needs", () => {
    expect(areaOffFor("/storefront/stock", { stock: false })).toBe("stock");
    expect(areaOffFor("/storefront/stock", { storefront: false })).toBeUndefined();
    expect(areaOffFor("/storefront/orders", { storefront: false, orders: true })).toBeUndefined();
    expect(areaOffFor("/home", { stock: false, money: false })).toBeUndefined();
  });
});

describe("words", () => {
  it("says nothing is deleted", () => {
    expect(offWords("stock")).toMatch(/Nothing is deleted/);
  });
  it("lists the areas that need one", () => {
    const rows: AreaRow[] = [
      { area: "storefront", name: "Online shop", covers: "", depends_on: ["orders"], enabled: true },
      { area: "orders", name: "Orders", covers: "", depends_on: [], enabled: true },
      { area: "schools", name: "Schools", covers: "", depends_on: ["money"], enabled: false },
    ];
    expect(neededBy(rows, "orders").map((r) => r.area)).toEqual(["storefront"]);
    expect(neededBy(rows, "money")).toEqual([]);
  });
});

import { forAreas, type Onboarding } from "./onboarding";

describe("forAreas", () => {
  const o = (done: string[]): Onboarding => {
    const ids = ["chat", "page", "workflow", "publish"] as const;
    const items = ids.map((id) => ({ id, done: done.includes(id) }));
    return { items, done: done.length, total: ids.length, complete: false, dismissed: false, self_serve: true, email_verified: true, can_send_email: true, docs_url: "", signed_up_at: null, first_run_at: null, seconds_to_first_run: null };
  };
  it("keeps the checklist as it is while the shop is on", () => {
    const x = o(["chat"]);
    expect(forAreas(x, undefined)).toBe(x);
    expect(forAreas(x, { storefront: true })).toBe(x);
  });
  it("leaves out making the shop page when the shop is off, and recounts", () => {
    const out = forAreas(o(["chat", "workflow"]), { storefront: false });
    expect(out.items.map((i) => i.id)).toEqual(["chat", "workflow", "publish"]);
    expect([out.done, out.total, out.complete]).toEqual([2, 3, false]);
    const all = forAreas(o(["chat", "workflow", "publish"]), { storefront: false });
    expect([all.done, all.total, all.complete]).toEqual([3, 3, true]);
  });
});
