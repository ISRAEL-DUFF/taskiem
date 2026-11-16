import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import { businessLine, codeProblem, countdown, lockWords, sentWords, showPhone, toE164 } from "./phone";

describe("toE164", () => {
  it("reads Nigerian numbers written the local way", () => {
    expect(toE164("0803 123 4567")).toBe("+2348031234567");
    expect(toE164("0803-123-4567")).toBe("+2348031234567");
    expect(toE164("(0803) 123.4567")).toBe("+2348031234567");
  });
  it("reads international numbers", () => {
    expect(toE164("+234 803 123 4567")).toBe("+2348031234567");
    expect(toE164("2348031234567")).toBe("+2348031234567");
    expect(toE164("00447700900123")).toBe("+447700900123");
    expect(toE164("+1 555 000 1111")).toBe("+15550001111");
  });
  it("refuses what cannot be a number", () => {
    expect(toE164("")).toBeNull();
    expect(toE164("0803")).toBeNull();
    expect(toE164("ada@example.com")).toBeNull();
    expect(toE164("+0803123456")).toBeNull();
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
    expect(codeProblem(new ApiError(429, "x", [], { locked: true }))).toMatch(/locked for an hour/);
    expect(codeProblem(new ApiError(503, "x", [], { sms_unavailable: true }))).toMatch(/by SMS/);
    expect(codeProblem(new ApiError(401, "x"))).toMatch(/not right/);
    expect(codeProblem(new ApiError(400, "x"))).toMatch(/country code/);
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
