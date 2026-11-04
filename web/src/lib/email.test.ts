import { describe, expect, it } from "vitest";
import { badSenders, fromWords, messageWords, parseSenders } from "./email";

const msg = {
  id: "m",
  sender_domain: "firstbank.ng",
  size: 10,
  attachments: 0,
  run_id: null,
  received_at: "2026-10-21T09:00:00Z",
};

describe("parseSenders", () => {
  it("takes addresses and domains however they are typed", () => {
    expect(parseSenders(" Alerts@FirstBank.ng, shop.ng\n<ada@x.ng>; shop.ng ")).toEqual(["alerts@firstbank.ng", "shop.ng", "ada@x.ng"]);
    expect(parseSenders("")).toEqual([]);
  });
});

describe("badSenders", () => {
  it("finds what is neither an address nor a domain", () => {
    expect(badSenders(["alerts@firstbank.ng", "@shop.ng", "shop.ng", "nonsense", "a@b"])).toEqual(["nonsense", "a@b"]);
  });
});

describe("messageWords", () => {
  it("says what came of a message", () => {
    expect(messageWords({ ...msg, status: "started", note: null })).toBe("Started a run");
    expect(messageWords({ ...msg, status: "started", note: null, attachments: 2 })).toBe("Started a run, with 2 attachments");
    expect(messageWords({ ...msg, status: "confirmation", note: null })).toBe("A request to confirm forwarding");
    expect(
      messageWords({
        ...msg,
        status: "refused",
        note: "Not started: an automatic reply.",
      }),
    ).toBe("Not started: an automatic reply.");
  });
  it("names the sender's domain", () => {
    expect(fromWords({ ...msg, status: "started", note: null })).toBe("From firstbank.ng");
    expect(fromWords({ ...msg, status: "started", note: null, sender_domain: null })).toBe("From an unknown sender");
  });
});
