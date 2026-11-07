import { describe, expect, it } from "vitest";
import { throwsClosed } from "./sheetDrag";

describe("throwsClosed", () => {
  it("closes on a throw or a long pull, springs back otherwise", () => {
    expect(throwsClosed(80, 60, 600)).toBe(true); // fast
    expect(throwsClosed(250, 1000, 600)).toBe(true); // past a third
    expect(throwsClosed(80, 600, 600)).toBe(false); // slow and short
    expect(throwsClosed(-40, 20, 600)).toBe(false); // up
  });
});
