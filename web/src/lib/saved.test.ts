import { describe, expect, it } from "vitest";
import {
  bankFields,
  bankValueOf,
  checkLine,
  detailsProblem,
  filterBanks,
  kindChoice,
  cleanName,
  nameProblem,
  savedSuggestions,
  numberProblem,
  providerLine,
  savedBase,
  savedSub,
  showConnectStep,
  kindProblem,
  shownValue,
  valuesProblem,
  type BankList,
  type KindField,
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
  check: { state: "unchecked", account_name: null, via: null, at: null, problem: null },
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
    expect(checkLine({ ...bayo, check: { state: "unchecked", account_name: null, via: null, at: null, problem: "Choose the bank again." } })).toEqual({
      words: "Choose the bank again.",
      tone: "warn",
    });
    expect(checkLine({ ...bayo, check: { state: "checked", account_name: "BAYO ADEWALE", via: "iswallet/taskiem", at: "x", problem: null } })).toEqual({
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

  it("searches banks by the start of their words", () => {
    const banks = [
      { code: "1", name: "Guaranty Trust Bank" },
      { code: "2", name: "Zenith Bank" },
      { code: "3", name: "First Bank of Nigeria" },
      { code: "4", name: "First City Monument Bank" },
      { code: "5", name: "GTBank" },
    ];
    const names = (typed: string, chosen = "") => filterBanks(banks, typed, chosen).map((b) => b.name);
    expect(names("")).toHaveLength(5);
    expect(names("guar")).toEqual(["Guaranty Trust Bank"]);
    expect(filterBanks([...banks, { code: "6", name: "Kuda Microfinance Bank" }], "fi").map((b) => b.name)).toEqual(["First Bank of Nigeria", "First City Monument Bank"]);
    expect(names("first")).toEqual(["First Bank of Nigeria", "First City Monument Bank"]);
    expect(names("first city")).toEqual(["First City Monument Bank"]);
    expect(names("gtb")).toEqual(["GTBank"]);
    expect(names("bank")).toHaveLength(5); // "bank" alone is not a word to search by
    expect(names("zzz")).toEqual([]);
    expect(names("zzz", "c:2")).toEqual(["Zenith Bank"]); // the chosen one stays
  });

  it("words the kinds and checks their forms", () => {
    expect(kindChoice("person")).toBe("A person");
    expect(kindChoice("provider_account")).toBe("An account with a provider");
    expect(detailsProblem("place", { area: "Yaba" })).toBe("Give the place its street and number.");
    expect(detailsProblem("text", { body: "  " })).toBe("Write the text.");
    expect(detailsProblem("provider_account", { provider: "DSTV", number: "123" })).toBe("Choose what the provider is.");
    expect(detailsProblem("person", {})).toBeNull();
    expect(savedSub({ ...bayo, kind: "person", hint: "" })).toBe("@bayo");
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

describe("saved things in the picker", () => {
  const text: SavedThing = { ...bayo, id: "t", kind: "text", name: "bank-details", title: "Our bank details", hint: "2 lines of text", details: { body: "Pay to Bola\nGTBank 0123" } };
  const mine: SavedThing = { ...bayo, id: "m", owner: "mine", name: "bank-details", title: "My bank details", kind: "text", hint: "1 line of text", details: { body: "Mine" } };
  it("offers one's own first, texts with their words, and says whose when two share a name", () => {
    const s = savedSuggestions([mine], [text, bayo], false);
    expect(s.map((x) => x.id)).toEqual(["m", "t", "1"]);
    expect(s[0]).toMatchObject({ kind: "saved", name: "bank-details", body: "Mine", sub: "My bank details · 1 line of text · Yours" });
    expect(s[1]?.sub).toBe("Our bank details · 2 lines of text · The business's");
    expect(s[2]).toMatchObject({ savedKind: "bank_account", body: undefined, sub: "Bayo (rice) · GTBank ••6789" });
  });
  it("offers texts only where nothing else can be shared yet", () => {
    expect(savedSuggestions([], [text, bayo], true).map((x) => x.id)).toEqual(["t"]);
  });
});

describe("kinds the owner makes", () => {
  const fields: KindField[] = [
    { key: "plate_number", label: "Plate number", type: "text", required: true },
    { key: "value", label: "Value", type: "money" },
    { key: "insurance_due", label: "Insurance due", type: "date" },
    { key: "chassis_number", label: "Chassis number", type: "text", required: true, sensitive: true },
  ];
  it("shows money as naira, a date as a day, and leaves a masked value as it is", () => {
    expect(shownValue(fields[1]!, "4500000")).toBe("₦4,500,000");
    expect(shownValue(fields[2]!, "2027-03-03")).toBe("3 Mar 2027");
    expect(shownValue(fields[3]!, "••3456")).toBe("••3456");
  });
  it("asks for each required field, a sensitive one kept counting", () => {
    expect(valuesProblem(fields, { plate_number: "" })).toBe("Plate number is needed.");
    expect(valuesProblem(fields, { plate_number: "LND 1", chassis_number: "" }, { chassis_number: "••3456" })).toBeNull();
  });
  it("checks a kind before it is sent", () => {
    expect(kindProblem("", fields)).toMatch(/name/);
    expect(kindProblem("Car", [])).toMatch(/at least one field/);
    expect(kindProblem("Car", [{ label: "A", type: "text" }, { label: "a", type: "text" }])).toMatch(/Two fields are called/);
    expect(kindProblem("Car", [{ label: "Fuel", type: "choice", options: ["Petrol", " "] }])).toMatch(/at least 2 choices/);
    expect(kindProblem("Car", fields)).toBeNull();
  });
});
