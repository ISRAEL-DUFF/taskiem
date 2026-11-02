import { describe, expect, it } from "vitest";
import { boldParts, mergeTaskiem, type TaskiemMessage } from "./taskiemchat";

const msg = (id: string, at: string, text = "x"): TaskiemMessage => ({
  id,
  channel: "web",
  inbound: false,
  kind: "text",
  text,
  created_at: at,
});
const NIL = "00000000-0000-0000-0000-000000000000";

describe("the Taskiem conversation", () => {
  it("merges the history and the answers by id, oldest first", () => {
    const a = [
      msg("2", "2026-10-31T10:01:00Z"),
      msg("1", "2026-10-31T10:00:00Z"),
    ];
    const b = [
      msg("2", "2026-10-31T10:01:00Z", "again"),
      msg("3", "2026-10-31T10:02:00Z"),
    ];
    const out = mergeTaskiem(a, b);
    expect(out.map((m) => m.id)).toEqual(["1", "2", "3"]);
    expect(out.map((m) => m.text)).toEqual(["x", "again", "x"]);
  });
  it("keeps a message that was not kept (a code) as sent", () => {
    expect(
      mergeTaskiem(
        [],
        [
          msg(NIL, "2026-10-31T10:00:00Z", "4821"),
          msg(NIL, "2026-10-31T10:00:01Z", "reply"),
        ],
      ),
    ).toHaveLength(2);
  });
  it("reads WhatsApp's *bold*", () => {
    expect(boldParts("Sold *3 cartons* for ₦30,000")).toEqual([
      { text: "Sold ", bold: false },
      { text: "3 cartons", bold: true },
      { text: " for ₦30,000", bold: false },
    ]);
    expect(boldParts("a * b * c")).toEqual([
      { text: "a ", bold: false },
      { text: " b ", bold: true },
      { text: " c", bold: false },
    ]);
  });
});
