import { describe, expect, it } from "vitest";
import {
  ago,
  reorderTotal,
  reorderWords,
  shownAlerts,
  type ChatAlert,
} from "./alerts";

describe("alerts", () => {
  it("says how long ago", () => {
    const now = Date.parse("2026-11-07T10:00:00Z");
    expect(ago("2026-11-07T09:59:30Z", now)).toBe("Just now");
    expect(ago("2026-11-07T09:58:00Z", now)).toBe("2 min ago");
    expect(ago("2026-11-07T07:00:00Z", now)).toBe("3 h ago");
    expect(ago("2026-11-06T08:00:00Z", now)).toBe("Yesterday");
    expect(ago("2026-11-01T08:00:00Z", now)).toBe("1 Nov");
  });
  it("words a reorder", () => {
    const r = {
      link: "l",
      supplier: "Bayo Rice Mills",
      seller_item_id: "p1",
      quantity: 20,
      price_kobo: 7_800_000,
    };
    expect(reorderWords(r)).toBe("Reorder 20 from Bayo Rice Mills");
    expect(reorderTotal(r)).toBe("₦1,560,000");
  });
  it("leaves out those dealt with", () => {
    const a = (key: string) =>
      ({ key, kind: "overdue", at: "", title: "", line: "" }) as ChatAlert;
    expect(
      shownAlerts([a("x"), a("y")], new Set(["x"])).map((x) => x.key),
    ).toEqual(["y"]);
    expect(shownAlerts(undefined, new Set())).toEqual([]);
  });
});
