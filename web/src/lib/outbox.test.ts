import { describe, expect, it } from "vitest";
import {
  asMessage,
  attachmentOf,
  newClientId,
  sendError,
  type Outgoing,
} from "./outbox";

const o = (p: Partial<Outgoing> = {}): Outgoing => ({
  clientId: "c1",
  body: "Ten bags",
  state: "sending",
  at: "2026-01-01T10:00:00Z",
  ...p,
});

describe("newClientId", () => {
  it("is a uuid, and a new one each time", () => {
    const a = newClientId();
    expect(a).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    );
    expect(newClientId()).not.toBe(a);
  });
});

describe("attachmentOf", () => {
  it("names what kind of file it is", () => {
    expect(
      attachmentOf(new File(["x"], "a.png", { type: "image/png" })).kind,
    ).toBe("photo");
    expect(
      attachmentOf(new File(["x"], "a.pdf", { type: "application/pdf" })).kind,
    ).toBe("file");
    expect(
      attachmentOf(new File(["x"], "v.webm", { type: "audio/webm" })).kind,
    ).toBe("voice");
  });
});

describe("asMessage", () => {
  it("is ours, in the thread, not yet sent", () => {
    const m = asMessage(o(), "link-1");
    expect(m).toMatchObject({
      id: "local-c1",
      link: "link-1",
      mine: true,
      body: "Ten bags",
      local: "sending",
      created_at: "2026-01-01T10:00:00Z",
    });
  });
  it("carries the quote, the file and why it failed", () => {
    const quote = {
      pair_id: "q",
      mine: false,
      kind: "text" as const,
      body: "Hi",
    };
    const f = new File(["x"], "a.png", { type: "image/png" });
    const m = asMessage(
      o({
        quote,
        file: f,
        preview: "data:image/png;base64,AA",
        state: "failed",
        error: "No connection",
      }),
      "l",
    );
    expect(m.reply).toEqual(quote);
    expect(m.attachment?.kind).toBe("photo");
    expect(m).toMatchObject({
      local: "failed",
      localError: "No connection",
      localPreview: "data:image/png;base64,AA",
    });
  });
  it("has no body for a photo sent without words", () => {
    expect(asMessage(o({ body: "" }), "l").body).toBeUndefined();
  });
});

describe("sendError", () => {
  const e = (status?: number, message = "") =>
    Object.assign(new Error(message), status ? { status } : {});
  it("says what happened and what to do", () => {
    expect(sendError(e())).toMatch(/no connection/i);
    expect(sendError(e(429))).toMatch(/too fast/i);
    expect(sendError(e(500))).toMatch(/Retry/);
  });
  it("gives the server's own words for a refusal", () => {
    expect(sendError(e(409, "you are no longer linked with Dangote"))).toBe(
      "you are no longer linked with Dangote",
    );
  });
});
