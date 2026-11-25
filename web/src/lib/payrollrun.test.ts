import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";
import {
  actionWords,
  adjustmentBody,
  adjustmentWords,
  breakdown,
  changesWords,
  koboOf,
  lockWarning,
  nextDecision,
  openToChange,
  parsePayrollCard,
} from "./payrollrun";

describe("this month's payroll (P4b-3)", () => {
  it("knows which decision is next", () => {
    expect(nextDecision("COMPUTED")).toBe("approve");
    expect(nextDecision("APPROVED")).toBe("lock");
    expect(nextDecision("LOCKED")).toBeNull();
    expect(nextDecision("DRAFT")).toBeNull();
    expect(openToChange("DRAFT")).toBe(true);
    expect(openToChange("COMPUTED")).toBe(true);
    expect(openToChange("APPROVED")).toBe(false);
    expect(actionWords("lock")).toBe("Lock and pay");
    expect(lockWarning("28 Nov")).toBe("Locking pays everyone on 28 Nov. A locked payroll can't be unlocked.");
  });

  it("reads amounts as typed", () => {
    expect(koboOf("100k")).toBe(10_000_000);
    expect(koboOf("₦20,000")).toBe(2_000_000);
    expect(koboOf("1.5m")).toBe(150_000_000);
    expect(koboOf("N 500")).toBe(50_000);
    expect(koboOf("0.5")).toBeNull();
    expect(koboOf("abc")).toBeNull();
    expect(adjustmentBody("e1", "deduction", 2_000_000, " Advance repaid ", "k")).toEqual({
      employee_id: "e1",
      type: "deduction",
      amount_kobo: 2_000_000,
      reason: "Advance repaid",
      key: "k",
    });
  });

  it("says a person's breakdown and the changes in plain words", () => {
    const b = breakdown([
      { kind: "earning", code: "BASIC", label: "Basic", amount_kobo: 1 },
      { kind: "deduction", code: "PAYE", label: "Tax (PAYE)", amount_kobo: 2 },
      { kind: "employer", code: "PENSION_ER", label: "Pension (employer's)", amount_kobo: 3 },
    ]);
    expect(b.earnings.map((i) => i.label)).toEqual(["Basic"]);
    expect(b.deductions.map((i) => i.label)).toEqual(["Tax (PAYE)"]);
    expect(b.employer.map((i) => i.label)).toEqual(["Pension (employer's)"]);
    expect(adjustmentWords({ id: "a", employee_id: "e", name: "Zainab", type: "bonus", amount_kobo: 10_000_000, reason: "Q4 target" })).toBe("₦100,000 bonus, Q4 target");
    expect(changesWords({ new: ["Ada Obi"], left: ["Musa Ali"], pay_changed: [{ name: "Zainab Bello", from_kobo: 40_000_000, to_kobo: 45_000_000 }] })).toEqual([
      "New this month: Ada Obi",
      "Not paid this month: Musa Ali",
      "Zainab Bello: pay ₦400,000 → ₦450,000",
    ]);
    expect(changesWords(undefined)).toEqual([]);
  });

  it("reads the chat's card", () => {
    expect(parsePayrollCard('{"run_id":"r1","period":"November 2026"}')).toEqual({ run_id: "r1", period: "November 2026" });
    expect(parsePayrollCard("not json")).toBeNull();
    expect(parsePayrollCard('{"period":"x"}')).toBeNull();
  });

  // No "disbursement", "run", "scope" or "exclusion" on owners' screens:
  // the words shown (JSX text, and strings with a space in them, which
  // paths, ids and class names have none of).
  it("never says run, disbursement, scope or exclusion to an owner", () => {
    for (const f of ["../pages/PayrollRun.tsx", "./payrollrun.ts"]) {
      const src = readFileSync(resolve(__dirname, f), "utf8");
      const sf = ts.createSourceFile(f, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
      const words: string[] = [];
      const visit = (n: ts.Node) => {
        if ((ts.isStringLiteral(n) || ts.isNoSubstitutionTemplateLiteral(n) || ts.isTemplateHead(n) || ts.isTemplateMiddle(n) || ts.isTemplateTail(n)) && /\s/.test(n.text.trim()))
          words.push(n.text);
        if (ts.isJsxText(n) && n.getText().trim()) words.push(n.getText().trim());
        ts.forEachChild(n, visit);
      };
      visit(sf);
      expect(words.length).toBeGreaterThan(5);
      expect(words.filter((w) => /\b(runs?|disbursements?|scopes?|exclusions?)\b/i.test(w))).toEqual([]);
    }
  });
});
