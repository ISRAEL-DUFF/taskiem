import { describe, expect, it } from "vitest";
import { createElement as h } from "react";
import { renderToString } from "react-dom/server";
import { MemoryRouter, Navigate } from "react-router-dom";
import { safeNext } from "./safePath";

describe("where to go after sign-in", () => {
  it("keeps the app's own paths", () => {
    for (const v of ["/workflows", "/runs/x?y=1#z", "/", "/invoices/INV-1?a=%2F%2Fb"]) expect(safeNext(v, "/home")).toBe(v);
  });

  it("falls back for anything that would leave the app", () => {
    for (const v of [
      null,
      undefined,
      "",
      "//evil.example",
      "/\\evil.example",
      "/%5Cevil.example",
      "https://evil.example",
      "javascript:alert(1)",
      "workflows",
      "/a\nb",
      "/\t/evil.example",
      "/%09/evil.example",
      "/%0a",
      "/%zz",
    ])
      expect(safeNext(v, "/home")).toBe("/home");
  });

  it("never makes the router throw (a ?next= link once blanked the app)", () => {
    for (const v of ["//evil.example", "https://evil.example", "/\\evil.example", "/%5Cevil.example", "/workflows"])
      expect(() =>
        renderToString(h(MemoryRouter, { initialEntries: ["/login"] }, h(Navigate, { to: safeNext(v, "/home"), replace: true }))),
      ).not.toThrow();
  });
});
