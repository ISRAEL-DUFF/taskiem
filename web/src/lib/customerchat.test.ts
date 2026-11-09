import { describe, expect, it } from "vitest";
import {
  customerLetter,
  customerTitle,
  phoneWords,
  latestPage,
  customerID,
  customerKey,
  mergeCustomer,
  owesWords,
  replyNote,
  type CustomerMessage,
} from "./customerchat";

const naira = (k: number) => `₦${(k / 100).toLocaleString("en-NG")}`;
const msg = (id: string, at: string): CustomerMessage => ({
  id,
  mine: false,
  kind: "text",
  body: id,
  read_at: null,
  created_at: at,
});

describe("customers' conversations", () => {
  it("keys a thread for the address and reads it back", () => {
    expect(customerKey("abc")).toBe("c-abc");
    expect(customerID("c-abc")).toBe("abc");
    expect(customerID("taskiem")).toBeUndefined();
    expect(customerID(null)).toBeUndefined();
  });

  it("says how a reply goes: STOP first, then more than a day", () => {
    expect(
      replyNote({ stopped: true, window_open: false, phone: "+234" })?.tone,
    ).toBe("stop");
    expect(
      replyNote({ stopped: false, window_open: false, phone: "" })?.text,
    ).toMatch(/erased/);
    expect(
      replyNote({ stopped: false, window_open: false, phone: "+234" }),
    ).toEqual({
      tone: "warn",
      text: expect.stringMatching(/More than a day/),
    });
    expect(
      replyNote({ stopped: false, window_open: true, phone: "+234" }),
    ).toBeNull();
  });

  it("says what a customer owes, or nothing", () => {
    expect(owesWords(1250000, 2, naira)).toBe("Owes ₦12,500 on 2 invoices");
    expect(owesWords(500000, 1, naira)).toBe("Owes ₦5,000 on 1 invoice");
    expect(owesWords(0, 0, naira)).toBe("");
    expect(owesWords(undefined, undefined, naira)).toBe("");
  });

  it("merges messages by id, oldest first", () => {
    const a = msg("a", "2026-11-09T08:00:00Z");
    const b = msg("b", "2026-11-09T08:01:00Z");
    const c = msg("c", "2026-11-09T08:02:00Z");
    const merged = mergeCustomer([b, c], [a, { ...b, body: "b again" }]);
    expect(merged.map((m) => m.id)).toEqual(["a", "b", "c"]);
    expect(merged[1]?.body).toBe("b again");
  });

  it("takes the latest page in place of what it covers", () => {
    const old = msg("old", "2026-11-09T07:00:00Z");
    const gone = msg("gone", "2026-11-09T08:00:00Z");
    const kept = msg("kept", "2026-11-09T08:01:00Z");
    const local = { ...msg("local-1", "2026-11-09T08:02:00Z"), local: true };
    // Everything is on the page: a message deleted on the server goes.
    expect(
      latestPage([gone, kept, local], [kept], false).map((m) => m.id),
    ).toEqual(["kept", "local-1"]);
    // After STOP the page is empty: only a send under way stays.
    expect(latestPage([gone, kept], [], false)).toEqual([]);
    // More before the page: older messages loaded above stay, and one
    // the page's span no longer has goes.
    const later = msg("later-gone", "2026-11-09T08:01:30Z");
    expect(
      latestPage([old, kept, later], [kept], true).map((m) => m.id),
    ).toEqual(["old", "kept"]);
  });

  it("says a number as people do, and names the thread", () => {
    expect(phoneWords("+2348031112222")).toBe("+234 803 111 2222");
    expect(phoneWords("+447700900123")).toBe("+447700900123");
    const unknown = { name: "+2348031112222", phone: "+2348031112222" };
    expect(customerTitle(unknown)).toBe("+234 803 111 2222");
    expect(customerLetter(unknown)).toBe("Customer");
    const known = { name: "Chidi Obi", phone: "+2348031112222" };
    expect(customerTitle(known)).toBe("Chidi Obi");
    expect(customerLetter(known)).toBe("Chidi Obi");
  });
});
