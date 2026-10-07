import { describe, expect, it } from "vitest";
import { diffLines, newestPerMajor, writeLines, type CatalogueVersion } from "./catalogue";

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
