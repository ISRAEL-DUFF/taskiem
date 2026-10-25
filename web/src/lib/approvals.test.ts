import { describe, expect, it } from "vitest";
import { fieldLabel, fieldValue, subjectWords } from "./approvals";

describe("an approval's subject in words", () => {
  it("leads with the amount in naira, who it goes to and the account", () => {
    const w = subjectWords({ amount_kobo: 4500000, to: "Kunle Supplies", bank: "GTBank", account_number: "0123454821", note: "Friday run" });
    expect(w.amount).toBe("₦45,000");
    expect(w.to).toBe("Kunle Supplies");
    expect(w.account).toBe("GTBank ••4821");
    expect(w.rest).toEqual([["Note", "Friday run"]]);
  });

  it("takes any field in kobo as the amount, and a plain amount as written", () => {
    expect(subjectWords({ fee_kobo: 150050 }).amount).toBe("₦1,500.50");
    expect(subjectWords({ amount: 250000 }).amount).toBe("250,000");
  });

  it("lists the rest by label, and leaves out what is not a plain value", () => {
    const w = subjectWords({ bvn_checked: true, due_date: "2026-10-31", lines: [1, 2], meta: { a: 1 } });
    expect(w.amount).toBeUndefined();
    expect(w.rest).toEqual([
      ["Bvn checked", "Yes"],
      ["Due date", "2026-10-31"],
    ]);
  });

  it("says nothing for a subject that is not fields", () => {
    for (const s of [null, undefined, "pay", 12, [1]]) expect(subjectWords(s)).toEqual({ rest: [] });
  });

  it("labels and values", () => {
    expect(fieldLabel("amount_kobo")).toBe("Amount");
    expect(fieldLabel("week")).toBe("Week");
    expect(fieldValue("count", 1200)).toBe("1,200");
  });
});
