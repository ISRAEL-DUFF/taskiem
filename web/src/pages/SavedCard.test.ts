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
