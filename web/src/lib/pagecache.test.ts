import { describe, expect, it } from "vitest";
import { PageCache } from "./pagecache";

describe("the memory of lists already loaded", () => {
  it("gives back a list with where it was scrolled, while it is fresh", () => {
    let now = 0;
    const c = new PageCache<string[]>(8, 1000, () => now);
    c.set("rice", ["a", "b"]);
    c.scrolled("rice", 640);
    expect(c.get("rice")).toEqual({ value: ["a", "b"], scroll: 640, at: 0 });
    now = 1001;
    expect(c.get("rice")).toBeUndefined();
    c.scrolled("rice", 10);
    expect(c.get("rice")).toBeUndefined();
  });

  it("keeps a few lists, the oldest going first", () => {
    const c = new PageCache<number>(2);
    c.set("a", 1);
    c.set("b", 2);
    c.set("a", 3);
    c.set("c", 4);
    expect(c.get("b")).toBeUndefined();
    expect(c.get("a")?.value).toBe(3);
    expect(c.get("c")?.value).toBe(4);
    c.delete("a");
    expect(c.get("a")).toBeUndefined();
    c.clear();
    expect(c.get("c")).toBeUndefined();
  });
});
