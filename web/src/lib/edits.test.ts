import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import { canEdit, editBlock, editRefusal, editTarget, lastEditable, refusedForTime } from "./edits";

// The rules the menu shows Edit by (chat-previews-and-editing.md §3.1).
const now = Date.parse("2026-11-26T12:00:00Z");
const ago = (min: number) => new Date(now - min * 60_000).toISOString();
const msg = (over: Record<string, unknown> = {}) => ({
  id: "m1",
  mine: true,
  kind: "text",
  body: "Rice is ₦45,000",
  created_at: ago(5),
  edits: 0,
  ...over,
});

describe("Edit in the menu", () => {
  it("is offered for our own text within 30 minutes", () => {
    expect(canEdit(msg(), now)).toBe(true);
    expect(canEdit(msg({ created_at: ago(29.99) }), now)).toBe(true);
    expect(editBlock(msg({ created_at: ago(30.02) }), now)).toBe("too_old");
  });

  it("is not offered for what may never be edited", () => {
    expect(editBlock(msg({ mine: false }), now)).toBe("not_yours");
    expect(editBlock(msg({ kind: "card", card: { doc: "order" } }), now)).toBe("card");
    expect(editBlock(msg({ kind: "diary" }), now)).toBe("card");
    expect(editBlock(msg({ kind: "taskiem" }), now)).toBe("card");
    expect(editBlock(msg({ offer: true }), now)).toBe("card");
    expect(editBlock(msg({ payment: "p1" }), now)).toBe("money");
    expect(editBlock(msg({ mentions: [{ kind: "invoice" }] }), now)).toBe("money");
    expect(editBlock(msg({ mentions: [{ kind: "business" }] }), now)).toBeUndefined();
    expect(editBlock(msg({ kind: "media", attachment: { kind: "voice" } }), now)).toBe("voice");
    expect(editBlock(msg({ forwarded: true }), now)).toBe("forwarded");
    expect(editBlock(msg({ deleted: true }), now)).toBe("deleted");
    expect(editBlock(msg({ line: "whatsapp" }), now)).toBe("elsewhere");
    expect(editBlock(msg({ local: "sending" }), now)).toBe("not_yours");
  });

  it("edits a file's caption, which may be emptied", () => {
    const m = msg({ kind: "media", attachment: { kind: "file" }, edits: 2 });
    expect(canEdit(m, now)).toBe(true);
    expect(editTarget(m)).toEqual({ id: "m1", text: "Rice is ₦45,000", edits: 2, caption: true });
  });

  it("finds our last message for the up arrow, only while it may be edited", () => {
    const list = [msg({ id: "a" }), msg({ id: "b", mine: false }), msg({ id: "c" }), msg({ id: "d", mine: false })];
    expect(lastEditable(list, now)?.id).toBe("c");
    expect(lastEditable([msg({ created_at: ago(40) })], now)).toBeUndefined();
    expect(lastEditable(undefined, now)).toBeUndefined();
  });

  it("says why an edit was refused, in plain words", () => {
    const old = new ApiError(403, "x", [], { not_editable: "too_old" });
    expect(refusedForTime(old)).toBe(true);
    expect(editRefusal(old)).toMatch(/can no longer be edited/);
    expect(editRefusal(new ApiError(409, "x", [], { stale: true }))).toMatch(/another device/);
  });
});
