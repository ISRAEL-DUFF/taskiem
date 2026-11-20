import { describe, expect, it } from "vitest";
import {
  bankFields,
  bankValueOf,
  checkLine,
  cleanName,
  nameProblem,
  numberProblem,
  providerLine,
  savedBase,
  savedSub,
  showConnectStep,
  type BankList,
  type SavedThing,
} from "./saved";

const bayo: SavedThing = {
  id: "1",
  owner: "business",
  name: "bayo",
  title: "Bayo (rice)",
  kind: "bank_account",
  hint: "GTBank ••6789",
  details: { bank_name: "Guaranty Trust Bank", account_number: "••6789" },
  check: { state: "unchecked", account_name: null, via: null, at: null },
  to_change: true,
  created_at: "2026-11-20T10:00:00Z",
  updated_at: "2026-11-20T10:00:00Z",
};

describe("saved things", () => {
  it("cleans and checks names as the server does", () => {
    expect(cleanName(" @Bola Stores ")).toBe("bola-stores");
    expect(nameProblem("@mum")).toBeNull();
    expect(nameProblem("x")).toMatch(/2 to 32/);
    expect(nameProblem("bola--stores")).toMatch(/only letters/);
    expect(nameProblem("-mum")).toMatch(/only letters/);
    expect(nameProblem("")).toMatch(/short name/);
  });

  it("checks account numbers", () => {
    expect(numberProblem("012 345-6789")).toBeNull();
    expect(numberProblem("12345")).toBe("An account number is 10 digits.");
  });

  it("words a row", () => {
    expect(savedSub(bayo)).toBe("@bayo · GTBank ••6789");
    expect(checkLine(bayo)).toEqual({ words: "Name not checked yet", tone: "warn" });
    expect(checkLine({ ...bayo, check: { state: "checked", account_name: "BAYO ADEWALE", via: "iswallet/taskiem", at: "x" } })).toEqual({
      words: "Bank's name: BAYO ADEWALE",
      tone: "ok",
    });
  });

  it("picks banks from a list with codes, or by name", () => {
    const wallet: BankList = { from: "iswallet/taskiem", banks: [{ code: "000013", name: "GUARANTY TRUST BANK" }, { code: "000015", name: "Zenith Bank" }] };
    const byName: BankList = { from: "", banks: [{ code: "", name: "Guaranty Trust Bank" }] };
    expect(bankFields(wallet, "c:000013")).toEqual({ bank_name: "GUARANTY TRUST BANK", bank_code: "000013", code_from: "iswallet/taskiem" });
    expect(bankFields(byName, "n:Guaranty Trust Bank")).toEqual({ bank_name: "Guaranty Trust Bank" });
    expect(bankFields(wallet, "c:999")).toBeNull();
    // A saved account's bank is found again: by code from the same list, else by name.
    expect(bankValueOf(wallet, { bank_name: "Guaranty Trust Bank" })).toBe("c:000013");
    expect(bankValueOf(wallet, { bank_name: "x", bank_code: "000015", code_from: "iswallet/taskiem" })).toBe("c:000015");
    expect(bankValueOf(byName, { bank_name: "Kuda" })).toBe("");
  });

  it("says where names are checked, and to whom the connect step is shown", () => {
    expect(providerLine({ connected: { channel: "iswallet/taskiem", name: "Your Taskiem wallet", wallet: true }, owner: true })).toBe(
      "Names are checked with the bank through your Taskiem wallet.",
    );
    expect(providerLine({ connected: { channel: "paystack/main", name: "Paystack", wallet: false }, owner: false })).toMatch(/through Paystack\.$/);
    expect(showConnectStep({ connected: null, owner: true })).toBe(true);
    expect(showConnectStep({ connected: null, owner: false })).toBe(false);
    expect(savedBase(true)).toBe("/v1/me/saved");
  });
});
