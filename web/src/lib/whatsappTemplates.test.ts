import { describe, expect, it } from "vitest";
import { approvedCount, canSubmit, statusWords, summaryLine, type TemplateView } from "./whatsappTemplates";

const tpl = (name: string, status: TemplateView["status"]): TemplateView => ({ name, category: "utility", language: "en", status, purpose: "" });

describe("whatsapp templates", () => {
  it("says each status in words", () => {
    expect(statusWords("not_submitted")).toBe("Not submitted");
    expect(statusWords("pending")).toBe("Waiting for Meta");
    expect(statusWords("approved")).toBe("Approved");
    expect(statusWords("rejected")).toBe("Rejected");
    expect(statusWords("paused")).toBe("Paused by Meta");
    expect(statusWords("disabled")).toBe("Disabled by Meta");
  });

  it("offers Submit only for templates Meta does not hold", () => {
    expect(canSubmit(tpl("taskiem_invoice", "not_submitted"))).toBe(true);
    for (const s of ["pending", "approved", "rejected", "paused", "disabled"] as const) {
      expect(canSubmit(tpl("taskiem_invoice", s))).toBe(false);
    }
  });

  it("counts and summarises", () => {
    const ts = [tpl("a", "approved"), tpl("b", "pending"), tpl("c", "rejected")];
    expect(approvedCount(ts)).toEqual({ approved: 1, total: 3 });
    expect(summaryLine({ tracked: false, templates: ts })).toMatch(/Connect your own number/);
    expect(summaryLine({ tracked: true, templates: ts, note: "Until these templates are approved, their messages fall back: b, c." })).toBe(
      "1 of 3 templates approved. Until these templates are approved, their messages fall back: b, c.",
    );
    expect(summaryLine({ tracked: true, templates: [tpl("a", "approved")] })).toMatch(/^All 1 templates are approved/);
  });
});
