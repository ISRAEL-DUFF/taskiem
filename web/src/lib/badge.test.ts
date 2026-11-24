import { describe, expect, it } from "vitest";
import { setAppBadge } from "./badge";

const device = () => {
  const calls: string[] = [];
  return {
    calls,
    nav: {
      setAppBadge: async (n?: number) => void calls.push(`set ${n}`),
      clearAppBadge: async () => void calls.push("clear"),
    },
  };
};

describe("the app's icon badge", () => {
  it("shows the count, and takes it off at none", () => {
    const d = device();
    setAppBadge(3, d.nav);
    setAppBadge(0, d.nav);
    setAppBadge(-2, d.nav);
    setAppBadge(4.9, d.nav);
    expect(d.calls).toEqual(["set 3", "clear", "clear", "set 4"]);
  });
  it("does nothing where the device has no badge, or refuses one", () => {
    expect(() => setAppBadge(3, {})).not.toThrow();
    expect(() =>
      setAppBadge(3, {
        setAppBadge: () => {
          throw new Error("no");
        },
      }),
    ).not.toThrow();
    expect(() =>
      setAppBadge(3, { setAppBadge: () => Promise.reject(new Error("no")) }),
    ).not.toThrow();
  });
});
