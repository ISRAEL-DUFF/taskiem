import { describe, expect, it } from "vitest";
import {
  CHIP_ICONS,
  readSettings,
  sellsLine,
  settingsRows,
} from "./oneproduct";

describe("one product pages", () => {
  it("offers the twelve icons the server takes", () => {
    expect(CHIP_ICONS.map((c) => c.name)).toEqual([
      "truck",
      "cash",
      "shield",
      "star",
      "clock",
      "check",
      "gift",
      "leaf",
      "phone",
      "sparkle",
      "heart",
      "return",
    ]);
  });

  it("reads the settings typed, leaving out what is empty", () => {
    const rows = settingsRows({
      promise: "Pay on delivery nationwide",
      chips: [{ icon: "truck", text: "Free delivery" }],
      stock_line: false,
    });
    expect(rows).toEqual({
      promise: "Pay on delivery nationwide",
      stock: false,
      chips: [{ icon: "truck", text: "Free delivery" }],
    });
    expect(
      readSettings({
        promise: "  ",
        stock: true,
        chips: [{ icon: "shield", text: " 30-day warranty " }],
      }),
    ).toEqual({
      settings: {
        stock_line: true,
        chips: [{ icon: "shield", text: "30-day warranty" }],
      },
      problem: "",
    });
    expect(settingsRows(null)).toEqual({ promise: "", stock: false, chips: [] });
  });

  it("says what is wrong, in plain words", () => {
    const base = { promise: "", stock: false, chips: [] };
    expect(readSettings({ ...base, promise: "a".repeat(81) }).problem).toMatch(
      /up to 80 characters/,
    );
    expect(
      readSettings({ ...base, promise: "Order at www.example.com" }).problem,
    ).toMatch(/no links/);
    expect(
      readSettings({
        ...base,
        chips: Array.from({ length: 7 }, () => ({ icon: "star", text: "Good" })),
      }).problem,
    ).toBe("Up to 6 chips.");
    expect(
      readSettings({ ...base, chips: [{ icon: "bank", text: "CBN approved" }] })
        .problem,
    ).toBe("Choose an icon for chip 1.");
    expect(
      readSettings({ ...base, chips: [{ icon: "star", text: "a".repeat(41) }] })
        .problem,
    ).toMatch(/Chip 1 needs 1 to 40 characters/);
  });

  it("says what the page sells", () => {
    expect(
      sellsLine({ name: "Smart Ear Cleaner", packages: [1, 2, 3] }),
    ).toBe("Smart Ear Cleaner, in 3 packages");
    expect(sellsLine({ name: "Soap", packages: [1] })).toBe(
      "Soap, in 1 package",
    );
    expect(sellsLine(undefined)).toMatch(/sells nothing/);
  });
});
