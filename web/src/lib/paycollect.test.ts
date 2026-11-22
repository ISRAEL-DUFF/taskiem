import { describe, expect, it, vi } from "vitest";
import {
  addBody,
  addedWords,
  amountProblem,
  chatNames,
  chatPath,
  footWords,
  listBlocked,
  needsBank,
  onList,
  purposeKey,
  rememberChat,
  rowAccount,
  rowWho,
  sourceChat,
  sourceFor,
  sourceWords,
  typedKobo,
  type Collection,
  type CollectedRow,
} from "./paycollect";

const row = (over: Partial<CollectedRow> = {}): CollectedRow => ({
  id: "r1",
  saved_id: null,
  payee: "",
  title: "Account ••6789",
  name: "Chidi Okeke",
  bank_name: "GTBank",
  last4: "6789",
  amount_kobo: 4_500_000,
  purpose: "",
  note: "",
  source: { kind: "link", chat: "L1", message: "m1" },
  added_at: "",
  ...over,
});

const list = (rows: CollectedRow[], over: Partial<Collection> = {}): Collection => ({
  rows,
  count: rows.length,
  total_kobo: rows.reduce((t, r) => t + (r.amount_kobo ?? 0), 0),
  no_amount: rows.filter((r) => r.amount_kobo === null).length,
  in_batch: null,
  ...over,
});

describe("adding a message's reading", () => {
  const src = sourceFor({ kind: "link", id: "L1" }, "m1");
  it("goes by the saved account's @name when there is one, as the chip pays", () => {
    expect(addBody({ found: true, account_number: "0123456789", payee: "@bayo", amount_kobo: 4_500_000, name: "Bayo" }, src)).toEqual({
      payee: "bayo",
      amount_kobo: 4_500_000,
      name: "Bayo",
      source: { kind: "link", chat: "L1", message: "m1" },
    });
  });
  it("else the number and its bank, chosen when none was read", () => {
    expect(addBody({ found: true, account_number: "0123456789", bank_name: "GTBank" }, src)).toEqual({ account_number: "0123456789", bank_name: "GTBank", source: src });
    expect(addBody({ found: true, account_number: "0123456789" }, src, " Access Bank ")).toMatchObject({ bank_name: "Access Bank" });
    expect(needsBank({ found: true, account_number: "0123456789" })).toBe(true);
    expect(needsBank({ found: true, account_number: "0123456789", payee: "bayo" })).toBe(false);
    expect(needsBank({ found: true, account_number: "0123456789", bank_name: "GTBank" })).toBe(false);
  });
  it("a photo's source keeps its chat, to link back", () => {
    const p = sourceFor({ kind: "customer", id: "C9" }, "m2", true);
    expect(p).toEqual({ kind: "photo", chat: "customer:C9", message: "m2" });
    expect(sourceChat(p)).toEqual({ kind: "customer", id: "C9", photo: true });
    expect(sourceChat({ kind: "photo", chat: "nonsense", message: "m" })).toBeNull();
  });
  it("knows a message already on the list", () => {
    const l = list([row()]);
    expect(onList(l, src)).toBe(true);
    expect(onList(l, sourceFor({ kind: "customer", id: "L1" }, "m1"))).toBe(false);
    expect(onList(undefined, src)).toBe(false);
  });
});

describe("the foot of a chat", () => {
  it("says how many and how much, and how many need an amount", () => {
    const l = list([row(), row({ id: "r2", amount_kobo: 5_000_000 }), row({ id: "r3", amount_kobo: null })]);
    expect(footWords(l)).toEqual({ main: "3 to pay · ₦95,000", needs: "1 needs an amount" });
    expect(footWords(list([row({ amount_kobo: null }), row({ id: "r2", amount_kobo: null })]))).toEqual({ main: "2 to pay", needs: "2 need an amount" });
    expect(footWords(list([row()]))).toEqual({ main: "1 to pay · ₦45,000" });
    expect(addedWords({ count: 3 })).toBe("Added: 3 to pay");
  });
});

describe("where a row came from", () => {
  it("names the chat when this browser knows it, else its kind", () => {
    const names = { "link:L1": "Chidi Okeke", "group:G1": "Market women", "customer:C1": "Tolu Bello" };
    expect(sourceWords({ kind: "link", chat: "L1", message: "m" }, names)).toBe("from Chidi Okeke's message");
    expect(sourceWords({ kind: "link", chat: "L2", message: "m" }, names)).toBe("from a message from a linked business");
    expect(sourceWords({ kind: "group", chat: "G1", message: "m" }, names)).toBe("from a message in Market women");
    expect(sourceWords({ kind: "group", chat: "G1", message: "m", from: "Funke" }, names)).toBe("from Funke's message in Market women");
    expect(sourceWords({ kind: "photo", chat: "group:G1", message: "m", from: "Funke" }, names)).toBe("from a photo Funke sent in Market women");
    // A group's name is not who sent it.
    expect(sourceFor({ kind: "group", id: "G1", name: "Market women" }, "m").from).toBeUndefined();
    expect(sourceWords({ kind: "photo", chat: "customer:C1", message: "m" }, names)).toBe("from a photo Tolu Bello sent");
    expect(sourceWords({ kind: "customer", chat: "C2", message: "m" }, names)).toBe("from a message from a customer");
    expect(sourceWords(null)).toBe("added by hand");
  });
  it("opens its chat", () => {
    expect(chatPath({ kind: "link", chat: "L1", message: "m1" })).toBe("/chat?with=L1&msg=m1");
    expect(chatPath({ kind: "customer", chat: "C1", message: "m1" })).toBe("/chat?with=c-C1");
    expect(chatPath({ kind: "photo", chat: "group:G1", message: "m1" })).toBe("/you/groups/G1");
    expect(chatPath({ kind: "person", chat: "P1", message: "m1" })).toBe("/you/chats/P1");
    expect(chatPath(null)).toBeNull();
  });
  it("remembers chat names, the newest kept", () => {
    const store = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
    });
    rememberChat("link", "L1", "Chidi Okeke");
    rememberChat("link", "L1", "Chidi Okeke Ltd");
    rememberChat("group", "G1", "  ");
    expect(chatNames()).toEqual({ "link:L1": "Chidi Okeke Ltd" });
  });
});

describe("a row", () => {
  it("is titled by the message's name, else the account", () => {
    expect(rowWho(row())).toBe("Chidi Okeke");
    expect(rowWho(row({ name: "", title: "Bayo Rice Mills", payee: "bayo" }))).toBe("Bayo Rice Mills");
    expect(rowWho(row({ name: "", title: "" }))).toBe("Account ••6789");
    expect(rowAccount(row())).toBe("GTBank ••6789");
    expect(rowAccount(row({ payee: "bayo" }))).toBe("@bayo · GTBank ••6789");
  });
  it("takes amounts as people type them", () => {
    expect(typedKobo("45k")).toBe(4_500_000);
    expect(typedKobo("abc")).toBeUndefined();
    expect(amountProblem("")).toBe("How much?");
    expect(amountProblem("abc")).toBe("Type an amount, like 45,000 or 45k.");
    expect(amountProblem("0.5")).toBe("Pay at least ₦1.");
    expect(amountProblem("20,000")).toBeNull();
    expect(purposeKey("a delivery")).toBe("delivery");
  });
  it("blocks the check until each has an amount, and while the list waits in a batch", () => {
    expect(listBlocked(list([row({ amount_kobo: null })]))).toBe("Say how much for 1 person first.");
    expect(listBlocked(list([]))).toMatch(/empty/);
    expect(listBlocked(list([row()], { in_batch: "b1" }))).toMatch(/waits to be sent/);
    expect(listBlocked(list([row()]))).toBeNull();
  });
});

