import { describe, expect, it } from "vitest";
import { leavesPage } from "./leave";

describe("which links leave a page with unsaved work", () => {
  const here = "https://app.taskiem.test/workflows/abc?v=2";
  it("another page of the console, in this tab", () => {
    expect(leavesPage("/home", here)).toBe(true);
    expect(leavesPage("https://app.taskiem.test/workflows", here, "_self")).toBe(true);
  });
  it("not the same page with another query or hash, another site, or a new tab", () => {
    expect(leavesPage("/workflows/abc?v=3", here)).toBe(false);
    expect(leavesPage("#steps", here)).toBe(false);
    expect(leavesPage("https://docs.taskiem.test/guide", here)).toBe(false);
    expect(leavesPage("/home", here, "_blank")).toBe(false);
  });
});
