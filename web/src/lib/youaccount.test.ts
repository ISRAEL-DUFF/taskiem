import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import {
  accountDeletedPath,
  deleteBlocked,
  deleteBody,
  deletedWords,
  deleteProblem,
  deleteProofs,
  holdersWords,
  NOTIFY_KINDS,
  photoFileProblem,
  photoProblem,
  privacyLine,
  toldOf,
} from "./youaccount";

describe("You's photo", () => {
  it("checks the file before it is sent", () => {
    expect(photoFileProblem({ type: "image/jpeg", size: 1000 })).toBeNull();
    expect(photoFileProblem({ type: "image/webp", size: 10 * 1024 * 1024 })).toBeNull();
    expect(photoFileProblem({ type: "text/plain", size: 10 })).toMatch(/JPEG, PNG or WebP/);
    expect(photoFileProblem({ type: "image/png", size: 10 * 1024 * 1024 + 1 })).toMatch(/too big/);
  });
  it("says a refused photo plainly", () => {
    expect(photoProblem(new ApiError(400, "This photo cannot be used: not an image."))).toBe("This photo cannot be used: not an image.");
    expect(photoProblem(new ApiError(400, "bad"))).toMatch(/can't be used as a photo/);
    expect(photoProblem(new ApiError(429, "x"))).toMatch(/Too many tries/);
    expect(photoProblem(new ApiError(500, "x"))).toBeNull();
  });
});

describe("You's notifications and privacy", () => {
  it("has the five kinds, people's two last", () => {
    expect(NOTIFY_KINDS.map((k) => k.key)).toEqual(["messages", "updates", "offers", "people", "requests"]);
  });
  it("says which businesses have the number", () => {
    const date = (iso: string) => iso.slice(0, 10);
    expect(privacyLine({ chat_id: "c", orders: 3, last_order_at: "2026-10-12T10:00:00Z" }, date)).toBe("You chat with them · 3 orders, the latest 2026-10-12");
    expect(privacyLine({ chat_id: null, orders: 1, last_order_at: null }, date)).toBe("1 order");
    expect(privacyLine({ chat_id: "c", orders: 0, last_order_at: null }, date)).toBe("You chat with them");
    expect(privacyLine({ chat_id: null, orders: 0, last_order_at: null }, date)).toBe("They have your number");
    expect(holdersWords(0)).toMatch(/^No business/);
    expect(holdersWords(1)).toBe("1 business has your number.");
    expect(holdersWords(4)).toBe("4 businesses have your number.");
  });
});

describe("deleting the account", () => {
  it("offers the proofs the account has", () => {
    expect(deleteProofs("+2348031234567", { passkey: true, password: true }, true)).toEqual(["code", "passkey", "password"]);
    expect(deleteProofs("+2348031234567", { passkey: true, password: false }, false)).toEqual(["code"]);
    expect(deleteProofs(null, { passkey: false, password: true }, true)).toEqual(["password"]);
    expect(deleteProofs(undefined, undefined, true)).toEqual([]);
  });

  it("sends confirm and one proof", () => {
    expect(deleteBody({ code: "123456" })).toEqual({ confirm: true, code: "123456" });
    expect(deleteBody({ password: "pw" })).toEqual({ confirm: true, password: "pw" });
    expect(deleteBody({ credential: { id: "x" } })).toEqual({ confirm: true, credential: { id: "x" } });
  });

  it("names the businesses that stand in the way, with what to do", () => {
    const owns = deleteBlocked(new ApiError(409, "x", [], { owns_business: true, businesses: ["Ada Kitchen"] }));
    expect(owns?.title).toBe("You own a business on Taskiem");
    expect(owns?.businesses).toEqual(["Ada Kitchen"]);
    expect(owns?.steps[0]).toMatch(/Make someone else its owner/);
    const member = deleteBlocked(new ApiError(409, "x", [], { in_business: true, businesses: ["A", "B"] }));
    expect(member?.title).toBe("You are part of businesses on Taskiem");
    expect(member?.steps[0]).toMatch(/Ask its owner to remove you/);
    expect(deleteBlocked(new ApiError(409, "x", [], { no_phone: true }))).toBeNull();
    expect(deleteBlocked(new Error("x"))).toBeNull();
  });

  it("says a refused proof plainly", () => {
    expect(deleteProblem(new ApiError(401, "x", [], { password_wrong: true }))).toBe("That password is not right.");
    expect(deleteProblem(new ApiError(401, "x", [], { code_wrong: true }))).toMatch(/That code is not right/);
    expect(deleteProblem(new ApiError(401, "that passkey response is not valid here; try again"))).toMatch(/passkey could not be used/);
    expect(deleteProblem(new ApiError(403, "x", [], { reauth: ["phone_code"] }))).toMatch(/^Confirm it is you first/);
    expect(deleteProblem(new ApiError(409, "x", [], { no_phone: true }))).toMatch(/no phone/);
    expect(deleteProblem(new ApiError(429, "x"))).toMatch(/Too many tries/);
    expect(deleteProblem(new ApiError(500, "x"))).toBeNull();
  });

  it("carries only a count to the signed-out page", () => {
    expect(accountDeletedPath(2)).toBe("/account-deleted?told=2");
    expect(accountDeletedPath(-1)).toBe("/account-deleted?told=0");
    expect(toldOf("?told=2")).toBe(2);
    expect(toldOf("?told=x")).toBeUndefined();
    expect(toldOf("")).toBeUndefined();
  });

  it("says it is gone, and what the businesses were told", () => {
    expect(deletedWords(0)).toMatch(/No business was told anything\./);
    expect(deletedWords(1)).toMatch(/The business you chatted with was told only that you left Taskiem\./);
    expect(deletedWords(3)).toMatch(/The 3 businesses you chatted with were told only that you left Taskiem\./);
  });
});
