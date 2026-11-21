import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { cardLine } from "./linkchat";
import { cardStatus, isDiary } from "./cardframe";
import {
  bareNumber,
  countdown,
  fromCustomer,
  lastWay,
  orderWays,
  pollEvery,
  refusalWords,
  rememberWay,
  shownState,
  spacedNumber,
  transferHead,
  waysOf,
} from "./paybytransfer";

const at = Date.parse("2026-11-21T14:00:00Z");
const plus = (ms: number) => new Date(at + ms).toISOString();

describe("the ways to pay", () => {
  it("keeps the server's order, transfer first", () => {
    expect(orderWays(["transfer", "taskiem", "checkout"])).toEqual(["transfer", "taskiem", "checkout"]);
  });
  it("puts the last way chosen for the seller first", () => {
    expect(orderWays(["transfer", "taskiem", "checkout"], "checkout")).toEqual(["checkout", "transfer", "taskiem"]);
    expect(orderWays(["transfer", "checkout"], "taskiem")).toEqual(["transfer", "checkout"]);
    expect(orderWays(["transfer", "nonsense"])).toEqual(["transfer"]);
  });
  it("falls back to the checkout when the server names none", () => {
    expect(waysOf({ due_kobo: 100, pay_link: "https://x" })).toEqual(["checkout"]);
    expect(waysOf({ due_kobo: 0, pay_link: "https://x" })).toEqual([]);
    expect(waysOf({ due_kobo: 100, ways_to_pay: [] })).toEqual([]);
  });
  it("remembers the way per seller, and survives blocked storage", () => {
    const m = new Map<string, string>();
    const store = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
    expect(lastWay("s1", store)).toBeNull();
    rememberWay("s1", "checkout", store);
    expect(lastWay("s1", store)).toBe("checkout");
    expect(lastWay("s2", store)).toBeNull();
    const blocked = {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("blocked");
      },
    };
    expect(() => rememberWay("s1", "transfer", blocked)).not.toThrow();
    expect(lastWay("s1", blocked)).toBeNull();
  });
});

describe("the account number", () => {
  it("reads in groups and copies without spaces", () => {
    expect(spacedNumber("0123456789")).toBe("0123 456 789");
    expect(bareNumber("0123 456 789")).toBe("0123456789");
  });
});

describe("the countdown", () => {
  it("says how long the number is good for", () => {
    expect(countdown(plus(29 * 60_000 + 12_000), at)).toEqual({ words: "Good for 29:12", warn: false, over: false });
    expect(countdown(plus(90 * 60_000), at)?.words).toBe("Good for 1:30:00");
  });
  it("warns in the last five minutes", () => {
    expect(countdown(plus(4 * 60_000 + 5_000), at)).toEqual({ words: "Expires in 4:05", warn: true, over: false });
    expect(countdown(plus(5 * 60_000), at)?.warn).toBe(true);
  });
  it("stops at zero", () => {
    expect(countdown(plus(-3000), at)).toEqual({ words: "Expires in 0:00", warn: true, over: true });
    expect(countdown(undefined, at)).toBeNull();
  });
  it("turns an open number past its time to expired", () => {
    expect(shownState({ state: "open", expires_at: plus(-1) }, at)).toBe("expired");
    expect(shownState({ state: "open", expires_at: plus(1000) }, at)).toBe("open");
  });
});

describe("the card's words", () => {
  const t = { amount_kobo: 8_300_000 };
  it("says each state", () => {
    expect(transferHead({ ...t, state: "asking" })).toBe("Getting an account number…");
    expect(transferHead({ ...t, state: "open" })).toBe("Send exactly ₦83,000");
    expect(transferHead({ ...t, state: "paid" }, "paid", "2026-11-21T14:52:00Z", () => "14:52")).toBe("Paid ₦83,000 · 14:52");
    expect(transferHead({ ...t, state: "part", received_kobo: 5_000_000 })).toBe("Received ₦50,000 of ₦83,000. ₦33,000 left");
    expect(transferHead({ ...t, state: "expired" })).toBe("This number has expired. Don't send to it");
    expect(transferHead({ state: "failed", problem: "Their bank did not give a number." })).toBe("Their bank did not give a number.");
  });
  it("says a refusal in plain words", () => {
    expect(refusalWords("too_many", undefined)).toMatch(/Too many numbers/);
    expect(refusalWords(undefined, "Nothing is left to pay on this bill.")).toBe("Nothing is left to pay on this bill.");
  });
  it("looks again every 2 seconds while asking, 10 while open", () => {
    expect(pollEvery("asking")).toBe(2000);
    expect(pollEvery("open")).toBe(10000);
    expect(pollEvery("paid")).toBeNull();
  });
  it("shows a customer's invoice number the same way", () => {
    expect(fromCustomer({ invoice: "i", reference: "INV-1", due_kobo: 0, state: "none" })).toBeNull();
    expect(fromCustomer({ invoice: "i", reference: "INV-1", due_kobo: 3_300_000, state: "part", amount_kobo: 8_300_000 })?.received_kobo).toBe(5_000_000);
  });
});

describe("the chat's cards", () => {
  it("tells the seller who is paying by transfer", () => {
    const card = { doc: "invoice" as const, event: "transfer", reference: "INV-0042", amount_kobo: 100 };
    expect(cardLine(card, false, "Ada's Kitchen")).toBe("Ada's Kitchen is paying by transfer");
    expect(cardLine({ ...card, doc: "bill" }, true, "Dangote")).toBe("You are paying by transfer");
    expect(cardStatus(card).word).toBe("By transfer");
    expect(isDiary({ ...card, doc: "bill" }, true)).toBe(false);
  });
});

describe("words never on screen", () => {
  // "virtual account", "dynamic", "rail" and "reference" are not said.
  const BANNED = /virtual account|\bdynamic\b|\brails?\b|\breferences?\b/i;
  it("in the card's words", () => {
    const words = [
      transferHead({ state: "asking" }),
      transferHead({ state: "open", amount_kobo: 1 }),
      transferHead({ state: "expired" }),
      transferHead({ state: "failed" }),
      refusalWords("too_many", undefined),
      refusalWords("disputed", undefined),
    ];
    for (const w of words) expect(w).not.toMatch(BANNED);
  });
  it("in the card's page", () => {
    const src = readFileSync(resolve(__dirname, "../pages/PayByTransfer.tsx"), "utf8");
    // What JSX shows, and the string literals: not the code's names.
    const shown = [...src.matchAll(/>([^<>{}]+)</g), ...src.matchAll(/"([^"]*)"/g)].map((m) => m[1] ?? "");
    for (const w of shown) expect(w).not.toMatch(/virtual account|\bdynamic\b|\brails?\b/i);
  });
});
