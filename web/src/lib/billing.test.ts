import { describe, expect, it } from "vitest";
import { blockerLines, isUpgrade, meters, naira, tiers, type Plan } from "./billing";

const plan = (id: string, tier: string, monthly: number): Plan => ({
  id,
  name: id,
  tier,
  monthly_kobo: monthly,
  annual_kobo: monthly * 10,
  limits: {},
  features: {},
  public: true,
});

describe("billing", () => {
  it("formats kobo as naira", () => {
    expect(naira(1_500_000)).toBe("₦15,000.00");
    expect(naira(123_456_789)).toBe("₦1,234,567.89");
    expect(naira(5)).toBe("₦0.05");
    expect(naira(-250)).toBe("-₦2.50");
  });
  it("compares plans as the server does", () => {
    const starter = plan("starter", "starter", 1_500_000);
    const growth = plan("growth", "growth", 6_000_000);
    expect(isUpgrade(tiers, starter, "monthly", growth, "monthly")).toBe(true);
    expect(isUpgrade(tiers, growth, "monthly", starter, "monthly")).toBe(false);
    expect(isUpgrade(tiers, growth, "monthly", growth, "annual")).toBe(true);
    expect(isUpgrade(tiers, growth, "annual", growth, "monthly")).toBe(false);
    expect(isUpgrade(tiers, growth, "monthly", plan("growth_plus", "growth", 9_000_000), "monthly")).toBe(true);
  });
  it("meters usage against limits", () => {
    const m = meters(
      { max_running_runs: 5, max_workflows: 20, ai_monthly_tokens: 0, whatsapp_templates_monthly: 500 },
      { runs_today: 0, runs_this_month: 9, running_runs: 5, queued_runs: 0, workflows: 17, secrets: 0, connections: 0, ai_tokens_this_month: 10, whatsapp_templates_this_month: { sent: 100, overage: 0 } },
    );
    const by = Object.fromEntries(m.map((x) => [x.key, x]));
    expect(by.max_running_runs?.level).toBe("full");
    expect(by.max_workflows?.level).toBe("warn");
    expect(by.max_workflows?.fraction).toBeCloseTo(0.85);
    expect(by.ai_monthly_tokens?.fraction).toBeNull();
    expect(by.whatsapp_templates_monthly?.level).toBe("ok");
  });
  it("reads downgrade blockers", () => {
    expect(blockerLines({ code: "downgrade_blocked", blockers: [{ limit: "max_workflows", used: 26, allowed: 20, remove: "remove 6 workflows (26 of 20 allowed)" }] })).toEqual([
      "remove 6 workflows (26 of 20 allowed)",
    ]);
    expect(blockerLines({})).toEqual([]);
  });
});
