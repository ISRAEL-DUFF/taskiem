import { describe, expect, it } from "vitest";
import { insideScroller, type ScrollNode } from "./pull";

const node = (
  over: Partial<ScrollNode>,
  parent: ScrollNode | null = null,
): ScrollNode => ({
  parentElement: parent,
  scrollHeight: 100,
  clientHeight: 100,
  tagName: "DIV",
  ...over,
});
const overflow = (m: Map<ScrollNode, string>) => (n: ScrollNode) =>
  m.get(n) ?? "visible";

describe("insideScroller", () => {
  it("is true inside a list that scrolls", () => {
    const body = node({ tagName: "BODY" });
    const list = node({ scrollHeight: 900, clientHeight: 400 }, body);
    const msg = node({}, list);
    expect(insideScroller(msg, overflow(new Map([[list, "auto"]])))).toBe(true);
  });
  it("is false for a list with nothing to scroll, and for the page itself", () => {
    const body = node({ tagName: "BODY", scrollHeight: 2000 });
    const list = node({}, body);
    const msg = node({}, list);
    expect(
      insideScroller(
        msg,
        overflow(
          new Map([
            [list, "auto"],
            [body, "auto"],
          ]),
        ),
      ),
    ).toBe(false);
    expect(insideScroller(null)).toBe(false);
  });
});
