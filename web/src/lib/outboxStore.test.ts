import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ post: vi.fn(), upload: vi.fn() }));
vi.mock("../api", () => api);

import {
  clearOutbox,
  discardOutgoing,
  onSent,
  outboxOf,
  pacing,
  retryOffline,
  retryOutgoing,
  sendOutgoing,
} from "./outboxStore";
import type { Outgoing } from "./outbox";

const out = (clientId: string, body = clientId): Outgoing => ({
  clientId,
  body,
  state: "sending",
  at: "2026-01-01T10:00:00Z",
});
const sent = (id: string) => ({
  message: {
    id,
    link: "l",
    mine: true,
    kind: "text",
    body: id,
    created_at: "2026-01-01T10:00:01Z",
  },
});
const settle = async () => {
  for (let i = 0; i < 20; i++) await Promise.resolve();
};
const error = (status?: number, message = "x") =>
  Object.assign(new Error(message), status ? { status } : {});

describe("the outbox store", () => {
  beforeEach(() => {
    clearOutbox();
    api.post.mockReset();
    api.upload.mockReset();
    pacing.waitMs = 0;
    pacing.tries = 3;
  });

  it("keeps a message until the server has it, then tells whoever listens, with its conversation", async () => {
    api.post.mockResolvedValue(sent("m1"));
    const heard: [string, string][] = [];
    const off = onSent((link, m) => heard.push([link, m.id]));
    sendOutgoing("A", out("c1"));
    expect(outboxOf("A")).toHaveLength(1); // at once, before the server answers
    await Promise.resolve();
    expect(api.post).toHaveBeenCalledWith(
      "/v1/link-chats/A/messages",
      expect.objectContaining({ client_id: "c1", text: "c1" }),
    );
    await settle();
    expect(outboxOf("A")).toHaveLength(0);
    expect(heard).toEqual([["A", "m1"]]);
    off();
  });

  it("sends one at a time per conversation, in order", async () => {
    const order: string[] = [];
    let release: () => void = () => undefined;
    api.post.mockImplementation(
      async (_p: string, body: { client_id: string }) => {
        order.push(`start ${body.client_id}`);
        if (body.client_id === "c1")
          await new Promise<void>((r) => (release = r));
        order.push(`end ${body.client_id}`);
        return sent(body.client_id);
      },
    );
    sendOutgoing("A", out("c1"));
    sendOutgoing("A", out("c2"));
    sendOutgoing("B", out("c3")); // another conversation does not wait
    await settle();
    expect(order).toEqual(["start c1", "start c3", "end c3"]);
    release();
    await settle();
    expect(order).toEqual([
      "start c1",
      "start c3",
      "end c3",
      "end c1",
      "start c2",
      "end c2",
    ]);
  });

  it("keeps a message that cannot be sent, in its conversation, with why", async () => {
    api.post.mockRejectedValue(error());
    sendOutgoing("A", out("c1"));
    await settle();
    const [o] = outboxOf("A");
    expect(o).toMatchObject({ state: "failed", retryOnline: true });
    expect(o?.error).toMatch(/no connection/i);
    expect(outboxOf("B")).toHaveLength(0);
  });

  it("does not go again for a refusal, but does when the connection is back", async () => {
    api.post
      .mockRejectedValueOnce(error(409, "you are no longer linked"))
      .mockRejectedValueOnce(error());
    sendOutgoing("A", out("c1"));
    sendOutgoing("A", out("c2"));
    await settle();
    expect(outboxOf("A").map((o) => [o.clientId, o.retryOnline])).toEqual([
      ["c1", false],
      ["c2", true],
    ]);
    api.post.mockResolvedValue(sent("m2"));
    retryOffline();
    await settle();
    expect(outboxOf("A").map((o) => o.clientId)).toEqual(["c1"]); // only the one that had no connection
  });

  it("retries the same send (the same client id)", async () => {
    api.post.mockRejectedValueOnce(error(500)).mockResolvedValue(sent("m1"));
    sendOutgoing("A", out("c1"));
    await settle();
    expect(outboxOf("A")[0]?.state).toBe("failed");
    retryOutgoing("A", "c1");
    await settle();
    expect(outboxOf("A")).toHaveLength(0);
    expect(api.post.mock.calls.map((c) => c[1].client_id)).toEqual([
      "c1",
      "c1",
    ]);
  });

  it("waits and tries again by itself when it is refused as too fast, a few times", async () => {
    api.post
      .mockRejectedValueOnce(error(429))
      .mockRejectedValueOnce(error(429))
      .mockResolvedValue(sent("m1"));
    sendOutgoing("A", out("c1"));
    await settle();
    await new Promise((r) => setTimeout(r, 5));
    await settle();
    expect(api.post).toHaveBeenCalledTimes(3);
    expect(outboxOf("A")).toHaveLength(0);
    // Too fast every time: it gives up and says so.
    api.post.mockReset();
    api.post.mockRejectedValue(error(429));
    sendOutgoing("A", out("c2"));
    await new Promise((r) => setTimeout(r, 10));
    await settle();
    expect(api.post).toHaveBeenCalledTimes(3);
    expect(outboxOf("A")[0]?.error).toMatch(/too fast/i);
  });

  it("skips a message deleted before its turn, and forgets all on sign-out", async () => {
    let release: () => void = () => undefined;
    api.post.mockImplementation(
      async (_p: string, body: { client_id: string }) => {
        if (body.client_id === "c1")
          await new Promise<void>((r) => (release = r));
        return sent(body.client_id);
      },
    );
    sendOutgoing("A", out("c1"));
    sendOutgoing("A", out("c2"));
    discardOutgoing("A", "c2");
    release();
    await settle();
    expect(api.post.mock.calls.map((c) => c[1].client_id)).toEqual(["c1"]);
    sendOutgoing("A", out("c3"));
    clearOutbox();
    expect(outboxOf("A")).toHaveLength(0);
  });

  it("sends a file as a form with the client id", async () => {
    api.upload.mockResolvedValue(sent("m1"));
    const file = new File(["x"], "bags.png", { type: "image/png" });
    sendOutgoing("A", { ...out("c1", "The new bags"), file, replyTo: "r1" });
    await settle();
    const form = api.upload.mock.calls[0]?.[1] as FormData;
    expect(api.upload.mock.calls[0]?.[0]).toBe("/v1/link-chats/A/files");
    expect(form.get("client_id")).toBe("c1");
    expect(form.get("text")).toBe("The new bags");
    expect(form.get("reply_to")).toBe("r1");
    expect((form.get("file") as File).name).toBe("bags.png");
  });
});
