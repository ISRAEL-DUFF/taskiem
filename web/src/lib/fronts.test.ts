import { describe, expect, it } from "vitest";
import { frontNameProblem, frontSub, frontURL, itemProblem, itemRequest, nairaOf, orderToTurnOn, type Front } from "./fronts";

const kinds = [
  { name: "shop", title: "Shop", line: "" },
  { name: "food", title: "Food", line: "" },
];
const front = (p: Partial<Front>): Front => ({
  id: "f1",
  slug: "snacks",
  kind: "food",
  name: "Snacks",
  position: 1,
  enabled: false,
  actions: [],
  table_id: null,
  items: 0,
  ...p,
});

describe("fronts", () => {
  it("says where a front is, its kind and what it has", () => {
    expect(frontSub(front({ table_id: "t", items: 3 }), kinds)).toBe(
      "/snacks · Food · 3 items",
    );
    expect(
      frontSub(
        front({ slug: "", kind: "shop", table_id: "t", items: 1, actions: [{} as never] }),
        kinds,
      ),
    ).toBe("Your home page · Shop · 1 item · 1 form");
  });
  it("gives a front's address", () => {
    expect(frontURL("https://ada.taskiem.page/", front({}))).toBe(
      "https://ada.taskiem.page/snacks",
    );
  });
  it("checks a new front's name", () => {
    expect(frontNameProblem("  ")).toBe("Give the front a name.");
    expect(frontNameProblem("x".repeat(81))).toBe("A name is up to 80 characters.");
    expect(frontNameProblem("Snacks")).toBe("");
  });
});

describe("front items", () => {
  const takes = { price: true, description: true, category: true, available: true, photo: true };
  const blank = { name: "", price: "", description: "", category: "" };
  it("reads naira", () => {
    expect(nairaOf("1,500")).toBe(1500);
    expect(nairaOf("₦1500.50")).toBe(1500.5);
    expect(nairaOf("abc")).toBeNull();
    expect(nairaOf("")).toBeNull();
  });
  it("says what is wrong", () => {
    expect(itemProblem(blank, takes)).toMatch(/name/);
    expect(itemProblem({ ...blank, name: "Rice", price: "x" }, takes)).toMatch(/naira/);
    expect(itemProblem({ ...blank, name: "Rice", price: "1,000" }, takes)).toBe("");
  });
  it("sends only what is new or changed", () => {
    const was = { name: "Rice", price: "1000", description: "", category: "" };
    expect(itemRequest({ ...blank, name: "Rice", price: "1,200" }, takes)).toEqual({ name: "Rice", price_naira: 1200 });
    expect(itemRequest({ ...was, price: "1200" }, takes, was)).toEqual({ price_naira: 1200 });
    expect(itemRequest({ ...was, description: "" }, takes, was)).toEqual({});
  });
  it("finds the order form to turn on", () => {
    const a = (p: object) => ({ id: "a", kind: "order", builtin: false, enabled: false, workflow_id: null, ...p }) as never;
    expect(orderToTurnOn({ actions: [a({})] })).toBeDefined();
    expect(orderToTurnOn({ actions: [a({ enabled: true, workflow_id: "w" })] })).toBeUndefined();
    expect(orderToTurnOn({ actions: [] })).toBeUndefined();
  });
});
