import { describe, expect, it } from "vitest";
import { ReadOrder } from "./usePagedList";

describe("a paged list's reads against its live patches", () => {
  it("shows reads in order and drops one older than what is shown", () => {
    const o = new ReadOrder();
    const a = o.start();
    const b = o.start();
    expect(o.take(b)).toBe("apply");
    // The older answer came last: it would put back what b replaced.
    expect(o.take(a)).toBe("drop");
  });

  it("drops a read asked before a live patch, and reads again when it was the last", () => {
    const o = new ReadOrder();
    expect(o.take(o.start())).toBe("apply");
    const stale = o.start();
    // Requests: a chat accepted elsewhere is patched out while the read is on its way.
    o.patched();
    expect(o.take(stale)).toBe("again");
    // The read again, asked after the patch, is shown.
    expect(o.take(o.start())).toBe("apply");
  });

  it("leaves the reading again to a later read already on its way", () => {
    const o = new ReadOrder();
    const first = o.start();
    o.patched();
    const after = o.start();
    expect(o.take(first)).toBe("drop");
    expect(o.take(after)).toBe("apply");
  });

  it("drops both reads asked before a patch, reading again once", () => {
    const o = new ReadOrder();
    const a = o.start();
    const b = o.start();
    o.patched();
    expect(o.take(a)).toBe("drop");
    expect(o.take(b)).toBe("again");
  });
});
