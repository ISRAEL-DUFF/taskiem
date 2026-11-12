import { describe, expect, it, vi } from "vitest";
import { registerWorker } from "./worker";

describe("registerWorker", () => {
  const now = (f: () => void) => f();

  it("registers /sw.js for the whole app once the page has loaded", () => {
    const register = vi.fn(() => Promise.resolve());
    let loaded: (() => void) | undefined;
    expect(registerWorker({ serviceWorker: { register } }, true, (f) => (loaded = f))).toBe(true);
    expect(register).not.toHaveBeenCalled();
    loaded?.();
    expect(register).toHaveBeenCalledWith("/sw.js", { scope: "/" });
  });

  it("does nothing in development or where the browser has no workers", () => {
    const register = vi.fn(() => Promise.resolve());
    expect(registerWorker({ serviceWorker: { register } }, false, now)).toBe(false);
    expect(registerWorker({}, true, now)).toBe(false);
    expect(register).not.toHaveBeenCalled();
  });

  it("ignores a registration that fails", async () => {
    const register = vi.fn(() => Promise.reject(new Error("blocked")));
    expect(registerWorker({ serviceWorker: { register } }, true, now)).toBe(true);
    await Promise.resolve();
    expect(register).toHaveBeenCalledOnce();
  });
});
