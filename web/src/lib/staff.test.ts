import { describe, expect, it } from "vitest";
import { asksBriefing, saleOf } from "./staff";
import type { StockItem } from "./stock";

describe("staff", () => {
  it("adds up a sale", () => {
    const items = [
      { id: "a", name: "Rice", price_kobo: 7_800_000, stock: 4, available: true },
      { id: "b", name: "Garri", price_kobo: 3_200_000, stock: 0, available: true },
    ] as StockItem[];
    expect(saleOf(items, {}).count).toBe("Nothing yet");
    const s = saleOf(items, { a: 2, b: 1, c: 5 });
    expect(s.lines.map((l) => l.item.id)).toEqual(["a", "b"]);
    expect(s.total).toBe(18_800_000);
    expect(s.count).toBe("3 things");
    expect(saleOf(items, { a: 1 }).count).toBe("1 thing");
  });
});

describe("the briefing and the chart", () => {
  it("words a change", async () => {
    const { changeWords } = await import("./staff");
    expect(changeWords(12, "last Wednesday")).toBe("Up 12% on last Wednesday");
    expect(changeWords(-3, "the week before")).toBe("Down 3% on the week before");
    expect(changeWords(0, "last Monday")).toBe("The same as last Monday");
    expect(changeWords(null, "x")).toBeUndefined();
  });
  it("scales the bars", async () => {
    const { barHeights } = await import("./staff");
    expect(barHeights([{ day: "", label: "", kobo: 0 }, { day: "", label: "", kobo: 50 }, { day: "", label: "", kobo: 100 }])).toEqual([0.04, 0.5, 1]);
    expect(barHeights([{ day: "", label: "", kobo: 0 }])).toEqual([0.04]);
  });
  it("greets by the first name, and words the cash", async () => {
    const { cashWords, firstName } = await import("./staff");
    expect(firstName("Ada Obi", "ada@x.test")).toBe("Ada");
    expect(firstName("", "tunde@x.test")).toBe("tunde");
    const n = (k: number) => `₦${k / 100}`;
    expect(cashWords(500, 500, n)).toBe("As sold");
    expect(cashWords(400, 500, n)).toBe("₦1 short");
    expect(cashWords(600, 500, n)).toBe("₦1 over");
  });
});

describe("asksBriefing", () => {
  it("is the briefing asked for, not a question about it", () => {
    for (const w of ["briefing", "Briefing", "show briefing", "show me the briefing", "today's briefing", "morning briefing!"])
      expect(asksBriefing(w)).toBe(true);
    for (const w of ["what is in the briefing", "briefings", "how did we do"])
      expect(asksBriefing(w)).toBe(false);
  });
});
