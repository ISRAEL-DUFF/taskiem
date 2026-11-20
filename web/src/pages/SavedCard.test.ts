import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../api";
import { sendSaved } from "./SavedCard";

describe("sendSaved", () => {
  const ask409 = new ApiError(409, "Share Mum's account details with them?", [], { confirm_share: true });

  it("sends once when nothing needs a yes", async () => {
    const send = vi.fn(async (c: boolean) => (c ? "again" : "sent"));
    const ask = vi.fn();
    expect(await sendSaved(send, ask)).toBe("sent");
    expect(ask).not.toHaveBeenCalled();
  });

  it("asks in the server's words and sends again with the yes", async () => {
    const send = vi.fn(async (c: boolean) => {
      if (!c) throw ask409;
      return "sent";
    });
    const ask = vi.fn(async () => true);
    expect(await sendSaved(send, ask)).toBe("sent");
    expect(ask).toHaveBeenCalledWith("Share Mum's account details with them?");
    expect(send.mock.calls).toEqual([[false], [true]]);
  });

  it("sends nothing on a no, and passes other problems on", async () => {
    const send = vi.fn(async () => {
      throw ask409;
    });
    expect(await sendSaved(send, async () => false)).toBeUndefined();
    expect(send).toHaveBeenCalledTimes(1);
    const gone = new ApiError(404, "That saved thing is gone. Choose another.");
    await expect(sendSaved(async () => Promise.reject(gone), async () => true)).rejects.toBe(gone);
  });
});

describe("sharing a saved thing between businesses", () => {
  it("asks only for what is sensitive, as the server does", async () => {
    const { savedSensitive, shareQuestion } = await import("../lib/saved");
    expect(savedSensitive({ kind: "bank_account", details: {} })).toBe(true);
    expect(savedSensitive({ kind: "person", details: { email: "a@b.ng" } })).toBe(true);
    expect(savedSensitive({ kind: "person", details: { address: "4 Allen Avenue" } })).toBe(false);
    expect(savedSensitive({ kind: "place", details: { line: "4 Allen Avenue", phone: "+2348031234567" } })).toBe(true);
    expect(savedSensitive({ kind: "text", details: { body: "No refunds" } })).toBe(false);
    expect(shareQuestion({ name: "our-account", words: "Our account", savedKind: "bank_account" }, "Ada's Kitchen")).toBe("Share Our account's account details with Ada's Kitchen?");
    expect(shareQuestion({ name: "home", savedKind: "place" }, "")).toBe("Share home's phone number and details with them?");
  });
});
