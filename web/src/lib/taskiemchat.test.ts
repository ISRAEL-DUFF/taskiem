import { afterEach, describe, expect, it, vi } from "vitest";
import {
  boldParts,
  forgetInflight,
  inflight,
  inflightOutcome,
  keepInflight,
  mergeTaskiem,
  type TaskiemMessage,
} from "./taskiemchat";

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

describe("mergeTaskiem keeps the ids that press the buttons", () => {
  const base = {
    id: "11111111-1111-1111-1111-111111111111",
    inbound: false,
    text: "Record it?",
    channel: "web",
    created_at: "2026-01-01T10:00:00Z",
  };
  it("a copy from the history, with titles only, does not take them away", () => {
    const given = { ...base, buttons: [{ id: "yes", title: "Yes" }] };
    const kept = { ...base, buttons: [{ title: "Yes" }] };
    const out = mergeTaskiem([given as never], [kept as never]);
    expect(out).toHaveLength(1);
    expect(out[0]?.buttons?.[0]?.id).toBe("yes");
  });
});

describe("a message being sent", () => {
  const web = (id: string, text: string, at: string, local = false) =>
    ({
      id,
      channel: "web",
      inbound: true,
      kind: "text",
      text,
      created_at: at,
      local,
    }) as TaskiemMessage;
  it("is replaced by its stored copy when that comes first, not shown twice", () => {
    const local = web("local-1", "status", "2026-01-01T10:00:00Z", true);
    const stored = web(
      "22222222-2222-2222-2222-222222222222",
      "status",
      "2026-01-01T10:00:00.4Z",
    );
    const out = mergeTaskiem([local], [stored]);
    expect(out.map((m) => m.id)).toEqual([stored.id]);
  });
  it("is kept while nothing like it has come, and one stored copy takes only one", () => {
    const a = web("local-1", "status", "2026-01-01T10:00:00Z", true);
    const b = web("local-2", "status", "2026-01-01T10:00:01Z", true);
    const stored = web(
      "33333333-3333-3333-3333-333333333333",
      "status",
      "2026-01-01T10:00:02Z",
    );
    expect(mergeTaskiem([a], [])).toHaveLength(1);
    expect(mergeTaskiem([a, b], [stored])).toHaveLength(2);
  });
  it("a copy already known does not take a local one away", () => {
    const stored = web(
      "44444444-4444-4444-4444-444444444444",
      "status",
      "2026-01-01T09:00:00Z",
    );
    const local = web("local-1", "status", "2026-01-01T10:00:00Z", true);
    expect(mergeTaskiem([stored, local], [stored])).toHaveLength(2);
  });
});

describe("inflightOutcome", () => {
  const f = { text: "cash in 5,000 till", at: "2026-01-01T10:00:00Z" };
  it("is sent when the history has it", () => {
    const m = {
      id: "x",
      channel: "web",
      inbound: true,
      kind: "text",
      text: f.text,
      created_at: "2026-01-01T10:00:01Z",
    } as TaskiemMessage;
    expect(inflightOutcome([m], f)).toBe("sent");
  });
  it("is not sent when it is not there, or only an old one with the same words is", () => {
    expect(inflightOutcome([], f)).toBe("not-sent");
    const old = {
      id: "y",
      channel: "web",
      inbound: true,
      kind: "text",
      text: f.text,
      created_at: "2025-12-01T10:00:00Z",
    } as TaskiemMessage;
    expect(inflightOutcome([old], f)).toBe("not-sent");
  });
});

describe("forgetInflight", () => {
  afterEach(() => vi.unstubAllGlobals());
  const store = () => {
    const m = new Map<string, string>();
    vi.stubGlobal("sessionStorage", {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => void m.set(k, v),
      removeItem: (k: string) => void m.delete(k),
    });
  };
  const last = { text: "status", at: "2026-01-01T10:00:00Z" };
  it("forgets the last page's message when it is still the one kept", () => {
    store();
    keepInflight(last);
    forgetInflight(last);
    expect(inflight()).toBeUndefined();
  });
  it("keeps a message this page has sent since", () => {
    store();
    const mine = { text: "status", at: "2026-01-01T10:00:05Z" };
    keepInflight(mine);
    forgetInflight(last);
    expect(inflight()).toEqual(mine);
  });
});
