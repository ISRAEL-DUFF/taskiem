import { describe, expect, it } from "vitest";
import { builtInByKind, capabilityLine, diffLines, newestPerMajor, writeLines, type CatalogueVersion } from "./catalogue";

const v = (version: string): CatalogueVersion => ({
  id: "p_acme_ledger",
  version,
  ref: `p_acme_ledger@${version.split(".")[0]}`,
  name: "Ledger",
  description: "",
  category: "payments",
  hosts: [],
  actions: {},
  pii: [],
  triggers: [],
  publisher: "acme",
  publisher_name: "Acme",
  licence: "MIT",
  digest: "",
  consent: { hosts: [], writes: {} },
});

describe("catalogue", () => {
  it("offers the newest version of each major", () => {
    expect(newestPerMajor([v("2.0.1"), v("2.0.0"), v("1.4.0"), v("1.3.9")]).map((x) => x.version)).toEqual(["2.0.1", "1.4.0"]);
  });
  it("says what each write may do", () => {
    expect(writeLines({ hosts: [], writes: { pay: "reconcilable_write", notify: "unsafe_write" } })).toEqual([
      "notify: changes things; never retried after a timeout",
      "pay: changes things; checked before any retry",
    ]);
  });
  it("puts what needs consent first in an upgrade", () => {
    const lines = diffLines({
      from: "1.0.0",
      to: "1.1.0",
      added_hosts: ["files.example.net"],
      added_actions: ["get_statement", "refund"],
      added_writes: ["refund"],
      removed_hosts: ["old.example.com"],
      needs_consent: true,
    });
    expect(lines).toEqual(["New host: files.example.net", "New action that changes things: refund", "New action: get_statement", "No longer reaches: old.example.com"]);
    expect(diffLines({ from: "1.0.0", to: "1.0.1", needs_consent: false })).toEqual(["No change to hosts, actions or classes"]);
  });
});

describe("builtInByKind", () => {
  const cs = [
    { id: "paystack", name: "Paystack", kind: "rail" as const, category: "payments", description: "Payments and transfers" },
    { id: "iswallet", name: "iswallet", kind: "rail" as const, category: "payments", description: "Wallet-to-wallet transfers", capabilities: ["pay_bank", "get_payout"] },
    { id: "googlesheets", name: "Google Sheets", kind: "tool" as const, category: "productivity" },
    { id: "notify", name: "Tell me", category: "messaging" },
    { id: "x_mine", name: "My own", kind: "tool" as const },
    { id: "p_e2e_ledger", name: "Example ledger", kind: "tool" as const },
  ];
  it("groups Taskiem's own connectors by kind, by name, leaving out an organisation's own and installed ones", () => {
    const g = builtInByKind(cs, "");
    expect(g.rail.map((c) => c.id)).toEqual(["iswallet", "paystack"]);
    expect(g.tool.map((c) => c.id)).toEqual(["googlesheets", "notify"]);
    expect(g.product).toEqual([]);
  });
  it("finds by name, id, description or category", () => {
    expect(builtInByKind(cs, "ISWALLET").rail.map((c) => c.id)).toEqual(["iswallet"]);
    expect(builtInByKind(cs, "transfers").rail).toHaveLength(2);
    expect(builtInByKind(cs, "productivity").tool.map((c) => c.id)).toEqual(["googlesheets"]);
    expect(builtInByKind(cs, "bank accounts").rail.map((c) => c.id)).toEqual(["iswallet"]);
  });
  it("says what a rail can do", () => {
    expect(capabilityLine(["get_payout", "balance", "pay_bank", "pay_wallet"])).toBe("Can pay bank accounts, pay wallets and check the balance.");
    expect(capabilityLine(["refund"])).toBe("Can refund payments.");
    expect(capabilityLine(["list_banks"])).toBe("");
    expect(capabilityLine(undefined)).toBe("");
  });
});
