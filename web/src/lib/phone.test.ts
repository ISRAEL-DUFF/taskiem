import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import { afterPhone, businessLine, codeDigits, codeProblem, countdown, lockWords, sentWords, showPhone, signedInTo, toE164, youPath } from "./phone";

// The one rule for typed numbers (C5): the same table is in the app's
// test/phone_test.dart. Keep the two the same.
export const PHONE_CASES: [string, string | null][] = [
  ["+234 0803 123 4567", "+2348031234567"],
  ["08031234567", "+2348031234567"],
  ["8031234567", "+2348031234567"],
  ["0803 123", null],
  ["+44 7700 900123", "+447700900123"],
  ["002348031234567", "+2348031234567"],
  ["+234 803 123", null],
  ["0803 123 4567", "+2348031234567"],
  ["0803-123-4567", "+2348031234567"],
  ["(0803) 123.4567", "+2348031234567"],
  ["+234 803 123 4567", "+2348031234567"],
  ["2348031234567", "+2348031234567"],
  ["00447700900123", "+447700900123"],
  ["+1 555 000 1111", "+15550001111"],
  ["+2346031234567", null],
  ["+23480312345678", null],
  ["1234567", null],
  ["", null],
  ["0803", null],
  ["ada@example.com", null],
  ["+0803123456", null],
  ["+44 77oo 900123", null],
];

describe("toE164", () => {
  it.each(PHONE_CASES)("reads %j as %j", (typed, want) => {
    expect(toE164(typed)).toBe(want);
  });
});

describe("words", () => {
  it("groups Nigerian numbers and leaves others", () => {
    expect(showPhone("+2348031234567")).toBe("+234 803 123 4567");
    expect(showPhone("+447700900123")).toBe("+447700900123");
  });
  it("says where the code went", () => {
    expect(sentWords("whatsapp", "+2348031234567")).toBe("We sent a 6-digit code to WhatsApp on +234 803 123 4567.");
    expect(sentWords("sms", "+2348031234567")).toBe("We sent a 6-digit code by SMS to +234 803 123 4567.");
  });
  it("says what went wrong with a code plainly", () => {
    expect(codeProblem(new ApiError(429, "x", [], { locked: true }))).toMatch(/Wait an hour/);
    expect(codeProblem(new ApiError(503, "x", [], { sms_unavailable: true }))).toMatch(/by SMS/);
    expect(codeProblem(new ApiError(429, "too many login attempts"))).toBe("Too many tries. Wait an hour and try again.");
    expect(codeProblem(new ApiError(401, "x", [], { code_wrong: true }))).toBe("That code is not right. Check it, or ask for a new one.");
    expect(codeProblem(new ApiError(401, "x", [], { ticket_expired: true }))).toBe("That code has expired: ask for a new one.");
    expect(codeProblem(new ApiError(401, "x", [], { tries_left: 2 }))).toBe("That PIN is not right. 2 tries left.");
    expect(codeProblem(new ApiError(401, "x", [], { tries_left: 1 }))).toBe("That PIN is not right. 1 try left.");
    expect(codeProblem(new ApiError(401, "this account is not active"))).toBe("This account can't sign in. Contact Taskiem.");
    expect(codeProblem(new ApiError(400, "x", [], { phone_invalid: true }))).toBe("That doesn't look like a phone number.");
    // Never a server's lower-case sentence as it came.
    expect(codeProblem(new ApiError(502, "the code could not be sent"))).toBe("The code could not be sent.");
  });
  it("names what a locked business asks for", () => {
    expect(lockWords("sso_required")).toMatch(/organisation/);
    expect(lockWords("passkey_required")).toMatch(/passkey/);
    expect(lockWords("second_proof_required")).toMatch(/prove/);
    expect(lockWords(null)).toBe("");
    expect(businessLine({ id: "1", name: "A", kind: "workflows" })).toBe("Workflows");
    expect(businessLine({ id: "1", name: "A", current: true })).toBe("Open now");
    expect(businessLine({ id: "1", name: "A", locked: true, lock: "passkey_required" })).toBe("Opens with your passkey");
  });
  it("counts down", () => {
    expect(countdown(45)).toBe("0:45");
    expect(countdown(60)).toBe("1:00");
    expect(countdown(-3)).toBe("0:00");
  });
});

describe("the way in's small rules", () => {
  it("takes the digits of a pasted code, then six", () => {
    expect(codeDigits("123 456")).toBe("123456");
    expect(codeDigits(" 12-34-56 78")).toBe("123456");
  });
  it("knows the person's own pages", () => {
    expect(youPath("/you")).toBe(true);
    expect(youPath("/you/settings?x=1")).toBe(true);
    expect(youPath("/second-proof")).toBe(true);
    expect(youPath("/youth")).toBe(false);
    expect(youPath("/invoices/1")).toBe(false);
  });
  it("goes where a phone sign-in was going", () => {
    expect(afterPhone("", null)).toBe("/you");
    expect(afterPhone("/you/settings", null)).toBe("/you/settings");
    expect(afterPhone("/invoices/1", null)).toBeNull();
    expect(afterPhone("", "business")).toBe("/you/start?what=business");
  });
  it("sends someone signed in on from /login and /signup", () => {
    expect(signedInTo("?start=business", "/you")).toBe("/you/start?what=business");
    expect(signedInTo("?start=business", "/start", false)).toBe("/start-something?what=business");
    expect(signedInTo("?next=%2Fyou%2Fsettings", "/you")).toBe("/you/settings");
    expect(signedInTo("?next=%2Finvoices", "/you")).toBe("/you");
    expect(signedInTo("?next=%2F%2Fevil.example", "/you")).toBe("/you");
  });
});
