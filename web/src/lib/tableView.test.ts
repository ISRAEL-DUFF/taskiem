import { describe, expect, it } from "vitest";
import { columnView, moneyKobo, pageWords, statusTone } from "./tableView";
import type { Column } from "./tables";

const col = (name: string, type: Column["type"]): Column => ({
  name,
  title: "",
  type,
  required: false,
  personal: false,
});

describe("the Tables tool's viewer", () => {
  it("draws a column by the shop's catalogue, its type and its name", () => {
    const shop = { photo: "photo", price: "price", price_unit: "kobo" };
    expect(columnView(col("photo", "text"), shop)).toEqual({ kind: "photo" });
    expect(columnView(col("price", "integer"), shop)).toEqual({
      kind: "money",
      unit: "kobo",
    });
    expect(columnView(col("price", "number"))).toEqual({
      kind: "money",
      unit: "naira",
    });
    expect(columnView(col("total_kobo", "integer"))).toEqual({
      kind: "money",
      unit: "kobo",
    });
    expect(columnView(col("quantity", "integer"))).toEqual({ kind: "number" });
    expect(columnView(col("in_stock", "boolean"))).toEqual({ kind: "yesno" });
    expect(columnView(col("due_on", "date"))).toEqual({ kind: "date" });
    expect(columnView(col("status", "text"))).toEqual({ kind: "status" });
    expect(columnView(col("name", "text"))).toEqual({ kind: "text" });
    expect(columnView(col("image", "text"))).toEqual({ kind: "photo" });
  });

  it("colours a status by what it says", () => {
    expect(statusTone("Paid")).toBe("lime");
    expect(statusTone(" pending ")).toBe("warn");
    expect(statusTone("Cancelled")).toBe("danger");
    expect(statusTone("Blue")).toBe("plain");
  });

  it("reads money in naira or kobo as kobo", () => {
    expect(moneyKobo(45000)).toBe(4500000);
    expect(moneyKobo("12.5")).toBe(1250);
    expect(moneyKobo(4500000, "kobo")).toBe(4500000);
    expect(moneyKobo("n/a")).toBeNull();
    expect(moneyKobo(null)).toBeNull();
  });

  it("says which rows a page shows", () => {
    expect(pageWords(0, 25, 25, 312)).toBe("1–25 of 312");
    expect(pageWords(12, 25, 12, 312)).toBe("301–312 of 312");
    expect(pageWords(1, 25, 25, null)).toBe("26–50");
    expect(pageWords(0, 25, 0, 0)).toBe("No rows");
    expect(pageWords(0, 25, 1250, 1250).endsWith("of 1,250")).toBe(true);
  });
});
