import { describe, expect, it } from "vitest";
import { sendNotes } from "./chattools";
import type { LinkMessage } from "./linkchat";

const message = { id: "m1" } as unknown as LinkMessage;

describe("sendNotes", () => {
  it("says nothing for a plain send", () => {
    expect(sendNotes({ message })).toEqual([]);
    expect(sendNotes({ message, also: [] })).toEqual([]);
  });
  it("points to the Taskiem chat when only Taskiem saw it", () => {
    const [n] = sendNotes({ messages: [] });
    expect(n?.kind).toBe("info");
    expect(n?.href).toBe("/chat?with=taskiem");
  });
  it("names the copies, and each one not sent", () => {
    const notes = sendNotes({
      message,
      also: [
        { link: "a", name: "BUA Foods", sent: true },
        { link: "b", name: "Cherry", sent: true },
        { link: "c", name: "Dangote", sent: false, reason: "the link is paused" },
      ],
    });
    expect(notes).toEqual([
      { text: "Also sent to BUA Foods and Cherry.", kind: "success" },
      { text: "Not sent to Dangote: the link is paused.", kind: "error" },
    ]);
  });
});
