import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import {
  accountDigits,
  canSendTo,
  CONTACTS_ON_PHONE,
  forwardCap,
  forwardWords,
  KEPT_WORDS,
  lastSeenWords,
  linkCode,
  mayDeleteForEveryone,
  mergePeople,
  messageLine,
  phoneWords,
  PRIVACY_ROWS,
  PRIVACY_SWITCHES,
  REPORT_CONSENT,
  requestsWords,
  rowPath,
  rowPreview,
  SCAM_WARNING,
  sendProblem,
  shownName,
  splitTitle,
  startWords,
  tickState,
  withLatestPeople,
  type PeopleMessage,
} from "./people";

const msg = (id: string, at: string, extra: Partial<PeopleMessage> = {}): PeopleMessage => ({ id, mine: false, kind: "text", body: id, read_at: null, created_at: at, ...extra });

describe("people chats", () => {
  it("never says or suggests the chats are end-to-end encrypted", () => {
    for (const words of [KEPT_WORDS, REPORT_CONSENT, SCAM_WARNING, CONTACTS_ON_PHONE, ...PRIVACY_ROWS.map((r) => r.hint), ...PRIVACY_SWITCHES.map((s) => s.hint)]) {
      expect(words).not.toMatch(/end[- ]to[- ]end|encrypt|only you (and|can)|nobody else can read/i);
    }
    expect(KEPT_WORDS).toMatch(/kept by Taskiem/);
    expect(KEPT_WORDS).toMatch(/reported/);
    expect(KEPT_WORDS).toMatch(/law/);
  });

  it("shows the name the viewer gave someone, and a deleted account as such", () => {
    expect(shownName({ name: "Kemi Adeyemi", contact_name: "Kemi (school)" })).toBe("Kemi (school)");
    expect(shownName({ name: "Kemi Adeyemi", contact_name: null })).toBe("Kemi Adeyemi");
    expect(shownName({ name: "Kemi Adeyemi", contact_name: "  " })).toBe("Kemi Adeyemi");
    expect(shownName({ name: "x", contact_name: "y", deleted: true })).toBe("Deleted account");
  });

  it("says when someone was last here, only as their settings let", () => {
    const now = new Date(2026, 10, 17, 15, 0);
    expect(lastSeenWords({ online: true, last_seen_at: now.toISOString() }, now)).toBe("online");
    expect(lastSeenWords({ online: null, last_seen_at: null }, now)).toBe("");
    expect(lastSeenWords(undefined, now)).toBe("");
    expect(lastSeenWords({ online: false, last_seen_at: new Date(2026, 10, 17, 14, 5).toISOString() }, now)).toBe("last seen today at 14:05");
    expect(lastSeenWords({ online: false, last_seen_at: new Date(2026, 10, 16, 9, 12).toISOString() }, now)).toBe("last seen yesterday at 09:12");
    expect(lastSeenWords({ online: false, last_seen_at: new Date(2026, 9, 3, 9, 12).toISOString() }, now)).toBe("last seen on 3 October 2026");
  });

  it("writes a Nigerian number as people read it", () => {
    expect(phoneWords("+2348031234567")).toBe("+234 803 123 4567");
    expect(phoneWords("+447700900123")).toBe("+447700900123");
    expect(phoneWords(null)).toBe("");
  });

  it("gives one tick, two, or read only when the answer says read", () => {
    expect(tickState({ local: true })).toBe("sending");
    expect(tickState({ status: "sent" })).toBe("sent");
    expect(tickState({})).toBe("sent");
    expect(tickState({ status: "delivered" })).toBe("delivered");
    expect(tickState({ status: "read" })).toBe("read");
  });

  it("caps forwards at five chats, one for a message forwarded many times", () => {
    expect(forwardCap({})).toBe(5);
    expect(forwardCap({ forwarded_many: true })).toBe(1);
    expect(forwardWords({ forwarded: true })).toBe("Forwarded");
    expect(forwardWords({ forwarded: true, forwarded_many: true })).toBe("Forwarded many times");
    expect(forwardWords({})).toBe("");
  });

  it("lets our own message be deleted for everyone within 24 hours", () => {
    const now = Date.parse("2026-11-17T12:00:00Z");
    expect(mayDeleteForEveryone({ mine: true, created_at: "2026-11-17T01:00:00Z" }, now)).toBe(true);
    expect(mayDeleteForEveryone({ mine: true, created_at: "2026-11-16T11:00:00Z" }, now)).toBe(false);
    expect(mayDeleteForEveryone({ mine: false, created_at: "2026-11-17T11:00:00Z" }, now)).toBe(false);
    expect(mayDeleteForEveryone({ mine: true, deleted: true, created_at: "2026-11-17T11:00:00Z" }, now)).toBe(false);
    expect(mayDeleteForEveryone({ mine: true, local: true, created_at: "2026-11-17T11:00:00Z" }, now)).toBe(false);
  });

  it("opens each row of the one list where its kind lives", () => {
    expect(rowPath({ kind: "person", id: "a" })).toBe("/you/people/a");
    expect(rowPath({ kind: "business", id: "b" })).toBe("/you/chats/b");
    expect(rowPreview({ last: "Hi", last_mine: true, blocked: false })).toBe("You: Hi");
    expect(rowPreview({ last: "Hi", last_mine: false, blocked: true })).toBe("You blocked them");
    expect(rowPreview({ last: "", last_mine: false, blocked: false, waiting: true })).toBe("Waiting for them to accept");
    expect(requestsWords(1)).toBe("1 message request");
    expect(requestsWords(3)).toBe("3 message requests");
  });

  it("reads a person's link pasted whole or as its code", () => {
    expect(linkCode("http://localhost:18080/p/ab23cd45ef")).toBe("ab23cd45ef");
    expect(linkCode("https://taskiem.com/p/AB23CD45EF/")).toBe("ab23cd45ef");
    expect(linkCode(" ab23cd45ef ")).toBe("ab23cd45ef");
    expect(linkCode("https://taskiem.com/m/ab23cd45ef")).toBe("");
    expect(linkCode("not a link")).toBe("");
  });

  it("takes a bank account as ten digits", () => {
    expect(accountDigits("0123 456-789")).toBe("0123456789");
    expect(accountDigits("12345")).toBe("");
  });

  it("says what a message holds in a line", () => {
    expect(messageLine({ body: "Hi" })).toBe("Hi");
    expect(messageLine({ body: "", deleted: true })).toBe("This message was deleted");
    expect(messageLine({ body: "", attachment: { kind: "photo", type: "image/png", size: 1 } })).toBe("Photo");
    expect(messageLine({ body: "the cake", attachment: { kind: "photo", type: "image/png", size: 1 } })).toBe("Photo: the cake");
    expect(messageLine({ body: "", card: { type: "split", title: "x", amount_kobo: 300_000, what: "the cake" } })).toBe("Your share: ₦3,000 for the cake");
    expect(splitTitle({ amount_kobo: 150_050 })).toBe("Your share: ₦1,500.50");
  });

  it("keeps sends on their way and older pages when the latest is read again", () => {
    const a = msg("a", "2026-11-17T10:00:00Z");
    const b = msg("b", "2026-11-17T11:00:00Z");
    const local = msg("local-1", "2026-11-17T12:00:00Z", { local: true, mine: true });
    expect(mergePeople([b], [a]).map((m) => m.id)).toEqual(["a", "b"]);
    expect(withLatestPeople([a, b, local], [b], true).map((m) => m.id)).toEqual(["a", "b", "local-1"]);
    expect(withLatestPeople([a, b], [b], false).map((m) => m.id)).toEqual(["b"]);
  });

  it("says a refused send in the server's plain words", () => {
    expect(sendProblem(new ApiError(403, "this message was not sent: it looks like a scam."))).toBe("This message was not sent: it looks like a scam.");
    expect(sendProblem(new ApiError(500, "boom"))).toBeNull();
    expect(sendProblem(new Error("x"))).toBeNull();
    expect(startWords(new ApiError(410, "link_replaced"))).toMatch(/no longer works/);
  });

  it("sends cards and forwards only to chats that take them", () => {
    expect(canSendTo({ gone: false, blocked: false, request: false })).toBe(true);
    expect(canSendTo({ gone: true, blocked: false, request: false })).toBe(false);
    expect(canSendTo({ gone: false, blocked: true, request: false })).toBe(false);
    expect(canSendTo({ gone: false, blocked: false, request: true })).toBe(false);
  });
});
