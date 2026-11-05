import { beforeEach, describe, expect, it, vi } from "vitest";
import { getDraft, setDraft } from "./drafts";

// The unit tests run without a browser: a stand-in for sessionStorage.
function fakeStorage() {
  const m = new Map<string, string>();
  return {
    get length() {
      return m.size;
    },
    key: (i: number) => [...m.keys()][i] ?? null,
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
  };
}

describe("drafts", () => {
  beforeEach(() => {
    vi.stubGlobal("sessionStorage", fakeStorage());
    vi.stubGlobal("window", { dispatchEvent: () => true });
  });
  it("keeps an unsent message per conversation", () => {
    setDraft("a", "Ten bags");
    setDraft("b", "Hello");
    expect(getDraft("a")).toBe("Ten bags");
    expect(getDraft("b")).toBe("Hello");
    expect(getDraft("c")).toBe("");
  });
  it("forgets an empty one", () => {
    setDraft("a", "Ten bags");
    setDraft("a", "   ");
    expect(getDraft("a")).toBe("");
    expect(sessionStorage.length).toBe(0);
  });
  it("copes with storage that is blocked", () => {
    vi.stubGlobal("sessionStorage", {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("blocked");
      },
      removeItem: () => {
        throw new Error("blocked");
      },
    });
    expect(() => setDraft("a", "x")).not.toThrow();
    expect(getDraft("a")).toBe("");
  });
});
