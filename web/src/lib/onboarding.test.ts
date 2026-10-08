import { describe, expect, it } from "vitest";
import { CHECKLIST, formatDuration, helpLink, sampleInput, starterTemplates } from "./onboarding";
import type { Template } from "./templates";

const tpl = (id: string, connectors: string[], available = true): Template => ({
  id,
  title: id,
  summary: "",
  description: "",
  category: "sales",
  tags: [],
  connectors,
  variables: [],
  params: [],
  steps: [],
  available,
});

describe("getting started", () => {
  it("words every checklist step", () => {
    for (const id of ["verify_email", "connection", "workflow", "publish", "first_run", "invite"] as const) {
      expect(CHECKLIST[id].title).not.toBe("");
      expect(CHECKLIST[id].href.startsWith("/")).toBe(true);
    }
  });

  it("puts the starter templates first and hides unavailable ones", () => {
    const list = [tpl("kyc-bvn-check", ["dojah@1", "termii@1", "gmail@1"]), tpl("zeta", ["termii@1"], false), tpl("payroll-reminder", ["termii@1", "gmail@1"]), tpl("new-order-alert-sms", ["termii@1"]), tpl("alpha", ["termii@1"])];
    expect(starterTemplates(list).map((t) => t.id)).toEqual(["new-order-alert-sms", "payroll-reminder", "alpha", "kyc-bvn-check"]);
  });

  it("formats durations", () => {
    expect(formatDuration(42)).toBe("42 s");
    expect(formatDuration(245)).toBe("4 min 05 s");
    expect(formatDuration(7800)).toBe("2 h 10 min");
    expect(formatDuration(86400)).toBe("1 day");
    expect(formatDuration(-3)).toBe("0 s");
  });

  it("samples a test input from the inputs schema", () => {
    const schema = {
      type: "object",
      required: ["order_id", "customer_name", "total_naira", "paid", "kind"],
      properties: {
        order_id: { type: "string" },
        customer_name: { type: "string", default: "Chidi" },
        total_naira: { type: "number", minimum: 0 },
        paid: { type: "boolean" },
        kind: { type: "string", enum: ["online", "shop"] },
        note: { type: "string" },
      },
    };
    expect(sampleInput(schema)).toEqual({ order_id: "TEST-1", customer_name: "Chidi", total_naira: 1000, paid: true, kind: "online" });
    expect(sampleInput({ $ref: "#/types/Loan" }, { Loan: { type: "object", required: ["amount"], properties: { amount: { type: "integer", minimum: 5 } } } })).toEqual({ amount: 5 });
    expect(sampleInput(undefined)).toEqual({});
  });

  it("links help to the docs site only when there is one", () => {
    expect(helpLink("", "templates")).toBeNull();
    expect(helpLink("https://docs.taskiem.test/", "templates")).toBe("https://docs.taskiem.test/templates");
  });
});
