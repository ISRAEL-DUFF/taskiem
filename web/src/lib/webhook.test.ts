import { describe, expect, it } from "vitest";
import { parseOrigins, schemaOf, withMethod } from "./webhook";

describe("withMethod", () => {
  it("keeps a fixed order and leaves POST alone out as the default", () => {
    expect(withMethod(["POST"], "GET", true)).toEqual(["GET", "POST"]);
    expect(withMethod(["GET", "POST"], "GET", false)).toBeUndefined();
    expect(withMethod(["POST"], "POST", false)).toBeUndefined();
    expect(withMethod(["POST"], "DELETE", true)).toEqual(["POST", "DELETE"]);
    expect(withMethod(["GET", "POST"], "POST", false)).toEqual(["GET"]);
  });
});

describe("parseOrigins", () => {
  it("splits on commas, trims and drops repeats", () => {
    expect(parseOrigins(" https://a.example, https://b.example,,https://a.example ")).toEqual(["https://a.example", "https://b.example"]);
    expect(parseOrigins("")).toEqual([]);
  });
});

describe("schemaOf", () => {
  it("describes a sample call's body", () => {
    expect(schemaOf({ order: "A1", total: 500, rate: 1.5, paid: true, items: [{ sku: "x" }], note: null })).toEqual({
      type: "object",
      properties: {
        order: { type: "string" },
        total: { type: "integer" },
        rate: { type: "number" },
        paid: { type: "boolean" },
        items: { type: "array", items: { type: "object", properties: { sku: { type: "string" } } } },
        note: {},
      },
    });
  });
});
