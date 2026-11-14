import { afterEach, describe, expect, it } from "vitest";
import { appLink, splitBusiness } from "./pushOpen";

describe("a tapped notification", () => {
  const b = "0192f6c4-8a9e-7b3c-9d2e-112233445566";
  it("opens the page in its business", () => {
    expect(splitBusiness("/chat", `?with=abc&business=${b}`)).toEqual({ to: "/chat?with=abc", business: b });
    expect(splitBusiness("/approvals", `?business=${b}`)).toEqual({ to: "/approvals", business: b });
    expect(splitBusiness("/approvals", "?business=nope")).toEqual({ to: "/approvals", business: null });
  });
  it("goes only to a page of the app", () => {
    expect(appLink("/bills/abc")).toBe(true);
    expect(appLink(`/chat?with=c-1&business=${b}`)).toBe(true);
    for (const bad of ["https://evil.example", "//evil.example", "/a/../b", "javascript:x", 5, null]) expect(appLink(bad)).toBe(false);
  });
});

describe("a link that is not a tap", () => {
  it("is not taken for one", async () => {
    const { tappedFor } = await import("./pushOpen");
    // No worker wrote anything down (no caches here at all).
    expect(await tappedFor("0192f6c4-8a9e-7b3c-9d2e-112233445566")).toBe(false);
  });
});

describe("the worker's mark of a tap", () => {
  const b = "0192f6c4-8a9e-7b3c-9d2e-112233445566";
  const other = "0192f6c4-8a9e-7b3c-9d2e-665544332211";
  function fakeCaches(entry: { business: string; at: number } | null) {
    let kept: string | null = entry ? JSON.stringify(entry) : null;
    const cache = {
      match: async () => (kept ? new Response(kept) : undefined),
      delete: async () => {
        kept = null;
        return true;
      },
    };
    (globalThis as { caches?: unknown }).caches = { open: async () => cache };
    return () => kept;
  }
  afterEach(() => {
    delete (globalThis as { caches?: unknown }).caches;
  });

  it("is a tap for its own business once, and left for its own page by another", async () => {
    const { tappedFor } = await import("./pushOpen");
    const left = fakeCaches({ business: b, at: Date.now() });
    expect(await tappedFor(other)).toBe(false);
    expect(left()).not.toBeNull();
    expect(await tappedFor(b)).toBe(true);
    expect(await tappedFor(b)).toBe(false);
  });

  it("is not one when it is old, and the old one goes", async () => {
    const { tappedFor } = await import("./pushOpen");
    const left = fakeCaches({ business: b, at: Date.now() - 300_000 });
    expect(await tappedFor(other)).toBe(false);
    expect(left()).toBeNull();
  });
});
