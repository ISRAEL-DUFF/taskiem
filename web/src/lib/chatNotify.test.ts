import { describe, expect, it } from "vitest";
import { beforeEach, vi } from "vitest";
import {
  claimMessage,
  decideNotify,
  notifyLine,
  type NotifyInput,
} from "./chatNotify";

const NOW = Date.parse("2026-11-04T10:00:00Z");
const input = (o: Partial<NotifyInput> = {}): NotifyInput => ({
  m: { mine: false, read_at: undefined, created_at: "2026-11-04T09:59:50Z" },
  muted: false,
  viewing: false,
  hidden: true,
  prefs: { browser: true, sound: true, asked: true },
  permission: "granted",
  now: NOW,
  ...o,
});

describe("notifyLine", () => {
  it("is the first line of the words", () => {
    expect(notifyLine({ kind: "text", body: "Rice is back\nand beans" })).toBe(
      "Rice is back",
    );
    expect(notifyLine({ kind: "text", body: "\n\n  Ten bags  \nmore" })).toBe(
      "Ten bags",
    );
  });
  it("is cut at a hundred characters", () => {
    expect(notifyLine({ kind: "text", body: "x".repeat(150) })).toHaveLength(
      100,
    );
  });
  it("says what a file is, with its words", () => {
    expect(
      notifyLine({
        kind: "text",
        body: "",
        attachment: { kind: "photo", type: "image/png", size: 1 },
      }),
    ).toBe("Photo");
    expect(
      notifyLine({
        kind: "text",
        body: "The new bags",
        attachment: { kind: "photo", type: "image/png", size: 1 },
      }),
    ).toBe("Photo: The new bags");
    expect(
      notifyLine({
        kind: "text",
        body: "",
        attachment: { kind: "voice", type: "audio/webm", size: 1 },
      }),
    ).toBe("Voice note");
    expect(
      notifyLine({
        kind: "text",
        body: "",
        attachment: {
          kind: "file",
          type: "application/pdf",
          size: 1,
          name: "waybill.pdf",
        },
      }),
    ).toBe("waybill.pdf");
  });
  it("shows nothing of a document: not its reference, amount or names", () => {
    const line = notifyLine({
      kind: "card",
      card: {
        doc: "bill",
        event: "sent",
        reference: "INV-0042",
        amount_kobo: 1250000,
        note: "Ada Okafor",
      },
    });
    expect(line).toBe("A bill");
    expect(line).not.toMatch(/INV|12,500|Okafor|₦/);
    expect(
      notifyLine({
        kind: "card",
        card: { doc: "order", event: "ordered", reference: "PO-1" },
      }),
    ).toBe("An order");
  });
  it("has a line even for a message with no words", () => {
    expect(notifyLine({ kind: "text", body: "" })).toBe("New message");
  });
});

describe("decideNotify", () => {
  it("notifies and pings for a message that comes while the tab is hidden", () => {
    expect(decideNotify(input())).toEqual({
      notify: true,
      sound: true,
      ask: false,
    });
  });
  it("does nothing for our own, a read one, a muted conversation, or the one in front", () => {
    const none = { notify: false, sound: false, ask: false };
    expect(
      decideNotify(
        input({
          m: {
            mine: true,
            read_at: undefined,
            created_at: "2026-11-04T09:59:55Z",
          },
        }),
      ),
    ).toEqual(none);
    expect(
      decideNotify(
        input({
          m: {
            mine: false,
            read_at: "2026-11-04T09:59:56Z",
            created_at: "2026-11-04T09:59:55Z",
          },
        }),
      ),
    ).toEqual(none);
    expect(decideNotify(input({ muted: true }))).toEqual(none);
    expect(decideNotify(input({ viewing: true }))).toEqual(none);
  });
  it("does nothing for an old message (the stream replays a few seconds)", () => {
    expect(
      decideNotify(
        input({
          m: {
            mine: false,
            read_at: undefined,
            created_at: "2026-11-04T09:50:00Z",
          },
        }),
      ),
    ).toEqual({ notify: false, sound: false, ask: false });
  });
  it("shows no notification with the tab in front, or without permission, but may ping", () => {
    expect(decideNotify(input({ hidden: false }))).toEqual({
      notify: false,
      sound: true,
      ask: false,
    });
    expect(decideNotify(input({ permission: "denied" }))).toEqual({
      notify: false,
      sound: true,
      ask: false,
    });
  });
  it("is silent until chosen: no sound, no notification", () => {
    expect(
      decideNotify(
        input({ prefs: { browser: false, sound: false, asked: true } }),
      ),
    ).toEqual({ notify: false, sound: false, ask: false });
  });
  it("asks once, only for a message that came while hidden, and never where asking cannot work", () => {
    const fresh = { browser: false, sound: false, asked: false };
    expect(
      decideNotify(input({ prefs: fresh, permission: "default" })).ask,
    ).toBe(true);
    expect(
      decideNotify(
        input({ prefs: fresh, permission: "default", hidden: false }),
      ).ask,
    ).toBe(false); // not on load, not while looking
    expect(
      decideNotify(
        input({ prefs: { ...fresh, asked: true }, permission: "default" }),
      ).ask,
    ).toBe(false); // once
    expect(
      decideNotify(input({ prefs: fresh, permission: "denied" })).ask,
    ).toBe(false);
    expect(
      decideNotify(input({ prefs: fresh, permission: "unsupported" })).ask,
    ).toBe(false);
  });
});

describe("claimMessage", () => {
  beforeEach(() => {
    const m = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => void m.set(k, v),
    });
    vi.stubGlobal("navigator", {}); // no Web Locks: the shared record alone
  });
  it("gives a message to the first that asks, and to no one after", async () => {
    expect(await claimMessage("m1", 1000)).toBe(true);
    expect(await claimMessage("m1", 1001)).toBe(false);
    expect(await claimMessage("m2", 1002)).toBe(true);
  });
  it("gives it up after ten minutes, and keeps only the recent", async () => {
    expect(await claimMessage("m1", 0)).toBe(true);
    expect(await claimMessage("m1", 11 * 60_000)).toBe(true);
    for (let i = 0; i < 300; i++) await claimMessage(`x${i}`, 20 * 60_000 + i);
    const kept = Object.keys(
      JSON.parse(localStorage.getItem("taskiem-chat-handled") ?? "{}"),
    );
    expect(kept.length).toBeLessThanOrEqual(200);
    expect(kept).toContain("x299");
  });
  it("is the one at a time through the Web Locks API where there is one", async () => {
    const order: string[] = [];
    vi.stubGlobal("navigator", {
      locks: {
        request: async (_name: string, fn: () => unknown) => {
          order.push("lock");
          return fn();
        },
      },
    });
    expect(await claimMessage("m9", 5)).toBe(true);
    expect(order).toEqual(["lock"]);
  });
});
