import { describe, expect, it } from "vitest";
import { actorLabel, isClosed, missingItems, reviewTarget, statusesFor, toRFC3339, updateTarget } from "./ops";

const items = [
  { key: "identity", text: "" },
  { key: "classes", text: "" },
  { key: "docs", text: "" },
];

describe("operator console helpers", () => {
  it("lists checklist items not confirmed, in order", () => {
    expect(missingItems(items, {})).toEqual(["identity", "classes", "docs"]);
    expect(missingItems(items, { classes: true, docs: false })).toEqual(["identity", "docs"]);
    expect(missingItems(items, { identity: true, classes: true, docs: true })).toEqual([]);
  });
  it("binds step-ups to the decision and the status", () => {
    expect(reviewTarget("abc", "approve")).toBe("abc/approve");
    expect(updateTarget("inc", "resolved")).toBe("inc/resolved");
  });
  it("knows statuses per kind", () => {
    expect(statusesFor("maintenance")).toContain("in_progress");
    expect(statusesFor("incident")).toContain("monitoring");
    expect(isClosed("incident", "resolved")).toBe(true);
    expect(isClosed("maintenance", "resolved")).toBe(false);
  });
  it("labels actors and converts times", () => {
    expect(actorLabel("operator:ada@taskiem.test")).toBe("ada@taskiem.test");
    expect(actorLabel("cli:ops")).toBe("cli:ops");
    expect(toRFC3339("")).toBeUndefined();
    expect(toRFC3339("2026-10-08T10:00")).toMatch(/^2026-10-0\dT\d\d:00:00\.000Z$/);
  });
});
