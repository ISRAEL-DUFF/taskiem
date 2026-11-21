import { describe, expect, it } from "vitest";
import {
  RIDER_PAY_FIELDS,
  amountTyped,
  answerWords,
  chipFor,
  chipWords,
  mayHoldAccount,
  messageFill,
  nameWarning,
  namesDiffer,
  paidWords,
  riderFill,
  maskedAccount,
  saveAsAnswer,
  saveAsBank,
  saveAsBody,
  saveAsProblem,
  saveAsShown,
  saveAsStart,
  type PayReading,
} from "./paymessage";
import { payAmountKobo } from "./payments";

describe("a message that may hold an account number", () => {
  it("finds a run of 10 digits, alone or in groups", () => {
    expect(mayHoldAccount("0123456789 GTBank Chidi Okeke")).toBe(true);
    expect(mayHoldAccount("Acct: 0123456789, Access")).toBe(true);
    expect(mayHoldAccount("acct 012 345 6789 opay")).toBe(true);
    expect(mayHoldAccount("0123-456-789 kuda")).toBe(true);
  });
  it("is not a phone number, an amount or nothing", () => {
    expect(mayHoldAccount("call me on 08031234567")).toBe(false);
    expect(mayHoldAccount("send 45,000 today")).toBe(false);
    expect(mayHoldAccount("12345678901234")).toBe(false);
    expect(mayHoldAccount("")).toBe(false);
    expect(mayHoldAccount(undefined)).toBe(false);
  });
});

const now = new Date("2026-11-21T12:00:00Z");
const reading = (over: Partial<PayReading> = {}): PayReading => ({ found: true, account_number: "0123456789", ...over });

describe("the chip", () => {
  it("pays for an owner; for staff, only a saved account", () => {
    expect(chipFor(reading(), true)).toBe("pay");
    expect(chipFor(reading({ payee: "chidi" }), false)).toBe("pay");
    expect(chipFor(reading(), false)).toBe("ask");
    expect(chipFor({ found: false }, true)).toBeNull();
    expect(chipFor(undefined, true)).toBeNull();
  });
  it("says when the account was paid before, and pays again", () => {
    expect(chipWords(reading(), now)).toBe("Pay this account");
    const paid = { id: "p1", amount_kobo: 4_500_000, status: "paid", at: "2026-11-03T10:00:00Z" };
    expect(paidWords(paid, now)).toBe("Paid ₦45,000 on 3 Nov");
    expect(chipWords(reading({ paid }), now)).toBe("Paid ₦45,000 on 3 Nov · Pay again");
    expect(paidWords({ ...paid, status: "sending" }, now)).toBe("Sent ₦45,000 on 3 Nov");
    expect(paidWords({ ...paid, status: "waiting_ok" }, now)).toBe("₦45,000 waiting for the owner's OK since 3 Nov");
    expect(paidWords({ ...paid, at: "2025-12-30T10:00:00Z" }, now)).toBe("Paid ₦45,000 on 30 Dec 2025");
    // Late at night in Lagos is still that day.
    expect(paidWords({ ...paid, at: "2026-11-03T23:30:00Z" }, now)).toBe("Paid ₦45,000 on 4 Nov");
  });
});

describe("the Pay sheet as a message fills it", () => {
  it("fills the number, the bank, the amount and the name", () => {
    const f = messageFill(reading({ bank_name: "Guaranty Trust Bank", amount_kobo: 4_500_000, name: "Chidi Okeke" }));
    expect(f.way).toBe("account");
    expect(f.initial).toEqual({ number: "0123456789", bank: "Guaranty Trust Bank", amount: "45,000" });
    expect(f.expectName).toBe("Chidi Okeke");
    expect(payAmountKobo(f.initial.amount ?? "")).toBe(4_500_000);
  });
  it("pays the saved account by its @name when there is one", () => {
    const f = messageFill(reading({ payee: "chidi", bank_name: "GTBank" }));
    expect(f.way).toBe("person");
    expect(f.initial).toEqual({ who: "@chidi" });
    expect(f.fields.map((x) => x.key)).toContain("who");
  });
  it("types amounts as the box takes them", () => {
    expect(amountTyped(125_050)).toBe("1,250.50");
    expect(amountTyped(0)).toBe("");
    expect(amountTyped(undefined)).toBe("");
  });
});

describe("the name the message gives and the bank's", () => {
  it("is the same person in any order or case, with a middle name", () => {
    expect(namesDiffer("Chidi Okeke", "OKEKE CHIDI EMMANUEL")).toBe(false);
    expect(namesDiffer("chidi okeke", "Chidi Okeke")).toBe(false);
    expect(namesDiffer("", "Chidi Okeke")).toBe(false);
  });
  it("says so in words when they differ", () => {
    expect(namesDiffer("Chidi Okafor", "Chidi Okeke")).toBe(true);
    expect(nameWarning("Chidi Okafor", "CHIDI OKEKE")).toBe("The message says Chidi Okafor; the bank says CHIDI OKEKE.");
    expect(nameWarning("Chidi Okeke", "CHIDI OKEKE")).toBeNull();
    expect(nameWarning(undefined, "CHIDI OKEKE")).toBeNull();
  });
});

describe("the banks' answers", () => {
  it("names the account, or says there is none", () => {
    expect(answerWords({ bank_name: "GTBank", bank_code: "058", account_name: "CHIDI OKEKE" })).toBe("GTBank: CHIDI OKEKE");
    expect(answerWords({ bank_name: "Access Bank", bank_code: "044", problem: "No account ••6789 at Access Bank." })).toBe("Access Bank: no account");
    expect(answerWords({ bank_name: "Kuda", bank_code: "50211", problem: "The bank did not say just now." })).toBe("Kuda: the bank did not say just now");
  });
});

describe("paying a rider", () => {
  it("asks how much and what for, a delivery unless changed", () => {
    expect(RIDER_PAY_FIELDS.map((f) => f.key)).toEqual(["amount", "for", "note"]);
    expect(riderFill("r1", 250_000)).toEqual({ rider: "r1", for: "a delivery", amount: "2,500" });
    expect(riderFill("r1", -100)).toEqual({ rider: "r1", for: "a delivery" });
  });
});

describe("Save as… beside the chip", () => {
  it("shows for an account not saved, to whoever may manage saved things", () => {
    expect(saveAsShown(reading(), true)).toBe(true);
    expect(saveAsShown(reading({ payee: "chidi" }), true)).toBe(false);
    expect(saveAsShown(reading(), false)).toBe(false);
    expect(saveAsShown({ found: false }, true)).toBe(false);
    expect(saveAsShown(undefined, true)).toBe(false);
  });
  it("says the saved name on the chip once saved", () => {
    expect(chipWords(reading({ payee: "chidi-okafor" }))).toBe("Pay @chidi-okafor");
    expect(chipWords(reading({ payee: "@bayo" }))).toBe("Pay @bayo");
  });
  it("suggests a name from the message's name, as the Saved page's names", () => {
    expect(saveAsStart(reading({ name: "Chidi Okafor" }))).toEqual({ name: "@chidi-okafor", title: "Chidi Okafor" });
    expect(saveAsStart(reading({ name: " Ọlá Adé " }))).toEqual({ name: "@ola-ade", title: "Ọlá Adé" });
    expect(saveAsStart(reading())).toEqual({ name: "", title: "" });
  });
  it("masks the number", () => {
    expect(maskedAccount("0123456789")).toBe("••6789");
    expect(maskedAccount(undefined)).toBe("");
  });
  it("sends the bank's code with the list it came from, else the name alone", () => {
    const list = {
      from: "paystack",
      banks: [
        { code: "058", name: "Guaranty Trust Bank" },
        { code: "", name: "Some Bank" },
      ],
    };
    expect(saveAsBank("Guaranty Trust Bank", list)).toEqual({ bank_name: "Guaranty Trust Bank", bank_code: "058", code_from: "paystack" });
    expect(saveAsBank("Some Bank", list)).toEqual({ bank_name: "Some Bank" });
    expect(saveAsBank("gtbank", null)).toEqual({ bank_name: "gtbank" });
    expect(saveAsBank("Guaranty Trust Bank", { from: "", banks: list.banks })).toEqual({ bank_name: "Guaranty Trust Bank" });
    // Read from the message with its code, the list not holding it.
    expect(saveAsBank("OPay", { from: "paystack", banks: [] }, reading({ bank_name: "OPay", bank_code: "999" }))).toEqual({
      bank_name: "OPay",
      bank_code: "999",
      code_from: "paystack",
    });
    expect(saveAsBank(" ", list)).toBeNull();
  });
  it("says what is missing, where", () => {
    expect(saveAsProblem({ name: "", title: "Chidi", bank: "GTBank" })?.at).toBe("name");
    expect(saveAsProblem({ name: "@a b!", title: "Chidi", bank: "GTBank" })?.at).toBe("name");
    expect(saveAsProblem({ name: "@chidi", title: " ", bank: "GTBank" })).toEqual({ at: "title", words: "Give it a title, like Chidi, rider." });
    expect(saveAsProblem({ name: "@chidi", title: "Chidi", bank: "" })).toEqual({ at: "bank", words: "Choose the bank." });
    expect(saveAsProblem({ name: "@Chidi Rider", title: "Chidi, rider", bank: "GTBank" })).toBeNull();
  });
  it("saves as the Saved page does", () => {
    expect(saveAsBody(reading(), { name: "@Chidi Rider", title: " Chidi, rider " }, { bank_name: "Guaranty Trust Bank", bank_code: "058", code_from: "paystack" })).toEqual({
      name: "chidi-rider",
      title: "Chidi, rider",
      kind: "bank_account",
      bank_name: "Guaranty Trust Bank",
      bank_code: "058",
      code_from: "paystack",
      account_number: "0123456789",
    });
  });
  it("says the bank's answer", () => {
    const check = (over: object) => ({ state: "checked" as const, account_name: "CHIDI OKEKE", via: "paystack", at: null, problem: null, ...over });
    expect(saveAsAnswer({ saved: { name: "chidi", check: check({}) } }, "Chidi Okeke")).toEqual({ bankName: "CHIDI OKEKE", differs: undefined, problem: undefined });
    expect(saveAsAnswer({ saved: { name: "chidi", check: check({}) } }, "Chidi Okafor").differs).toBe("The message says Chidi Okafor; the bank says CHIDI OKEKE.");
    const later = "Paystack did not check the name just now; it is checked again later.";
    expect(saveAsAnswer({ saved: { name: "chidi", check: check({ state: "unchecked", account_name: null }) }, check_problem: later })).toEqual({
      bankName: undefined,
      differs: undefined,
      problem: later,
    });
    expect(saveAsAnswer({ saved: { name: "chidi", check: check({ state: "unchecked", account_name: null }) } })).toEqual({ bankName: undefined, differs: undefined, problem: undefined });
  });
});
