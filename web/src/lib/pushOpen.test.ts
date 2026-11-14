import { describe, expect, it } from "vitest";
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
