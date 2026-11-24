import { describe, expect, it } from "vitest";
import { foundSuggestion, savedSub, serviceSub, type Found } from "./found";

const at = "2026-11-24T10:00:00Z";

describe("what search everywhere finds besides the refs", () => {
  it("shows a service with its price and minutes", () => {
    expect(serviceSub({ total_kobo: 300_000, minutes: 30 })).toBe("₦3,000 · 30 min");
    expect(serviceSub({})).toBe("Price on the day");
    expect(foundSuggestion({ kind: "service", id: "s1", name: "Haircut", when: at, total_kobo: 300_000, minutes: 30 })).toEqual({
      kind: "service",
      id: "s1",
      name: "Haircut",
      sub: "₦3,000 · 30 min",
    });
  });

  it("shows a saved thing by its name, kind and owner only", () => {
    const mum: Found = { kind: "saved", id: "m1", name: "mum", when: at, saved_kind: "bank_account", owner: "mine" };
    expect(foundSuggestion(mum)).toEqual({ kind: "saved", id: "m1", name: "@mum", sub: "A bank account · Yours", savedKind: "bank_account", savedOwner: "mine" });
    expect(savedSub({ saved_kind: "place", owner: "business" })).toBe("A place · The business's");
    expect(savedSub({ saved_kind: "custom", kind_name: "Generator", owner: "business" })).toBe("A generator · The business's");
    expect(savedSub({ saved_kind: "custom", owner: "mine" })).toBe("One of your own kinds · Yours");
  });

  it("leaves the refs to refSuggestion", () => {
    expect(foundSuggestion({ kind: "invoice", id: "i1", name: "INV-0001", when: at })).toBeUndefined();
  });
});
