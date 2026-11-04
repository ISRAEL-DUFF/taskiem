import { describe, expect, it } from "vitest";
import { CHECKLIST, firstThings, formatDuration, helpLink, sampleInput, starterTemplates } from "./onboarding";
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
    for (const id of ["verify_email", "chat", "page", "connection", "workflow", "publish", "first_run", "invite"] as const) {
      expect(CHECKLIST[id].title).not.toBe("");
      expect(CHECKLIST[id].href.startsWith("/")).toBe(true);
    }
  });

  it("puts the starter templates first and hides unavailable ones", () => {
    const list = [
      tpl("kyc-bvn-check", ["dojah@1", "notify@1", "gmail@1"]),
      tpl("zeta", ["notify@1"], false),
      tpl("payroll-reminder", ["notify@1", "gmail@1"]),
      { ...tpl("new-order-alert-sms", ["notify@1"]), needs_developer: true },
      tpl("alpha", ["notify@1"]),
      tpl("daily-reminder", ["notify@1"]),
    ];
    // Nothing to connect first; a developer's template last.
    expect(starterTemplates(list).map((t) => t.id)).toEqual(["daily-reminder", "payroll-reminder", "alpha", "kyc-bvn-check", "new-order-alert-sms"]);
  });

  it("offers a product's templates first, and only to a business that connected it", () => {
    const list = [tpl("daily-reminder", ["notify@1"]), { ...tpl("payrolla-tell-staff", ["payrolla@1", "notify@1"]), product: "payrolla" }];
    expect(starterTemplates(list).map((t) => t.id)).toEqual(["daily-reminder"]);
    expect(starterTemplates(list, ["paystack", "payrolla"]).map((t) => t.id)).toEqual(["payrolla-tell-staff", "daily-reminder"]);
  });

  it("offers a service's templates first, once it is connected", () => {
    const list = [
      tpl("daily-reminder", ["notify@1"]),
      { ...tpl("slack-paid-invoices", ["slack@1"]), services: [["slack"]] },
      { ...tpl("books-copy-invoice", ["capability:books_invoice"]), services: [["quickbooks", "zoho_books"]] },
    ];
    expect(starterTemplates(list).map((t) => t.id)).toEqual(["daily-reminder"]);
    expect(starterTemplates(list, ["zoho_books"]).map((t) => t.id)).toEqual(["books-copy-invoice", "daily-reminder"]);
    expect(starterTemplates(list, ["slack", "quickbooks"]).map((t) => t.id)).toEqual(["books-copy-invoice", "slack-paid-invoices", "daily-reminder"]);
  });

  it("offers invoices among the starters, after the one that needs nothing connected", () => {
    const list = [tpl("chase-overdue-invoices", ["invoices@1", "notify@1"]), tpl("send-invoice", ["invoices@1", "notify@1"]), tpl("daily-reminder", ["notify@1"]), tpl("alpha", ["notify@1"])];
    expect(starterTemplates(list).map((t) => t.id)).toEqual(["daily-reminder", "send-invoice", "chase-overdue-invoices", "alpha"]);
  });

  it("offers invoices and money to those who may open them", () => {
    const can = (...perms: string[]) => (p: string) => perms.includes(p);
    expect(firstThings(can()).map((f) => f.id)).toEqual([]);
    expect(firstThings(can("invoice.read")).map((f) => f.id)).toEqual(["money"]);
    expect(firstThings(can("invoice.read", "invoice.manage")).map((f) => [f.id, f.href])).toEqual([
      ["invoice", "/invoices/new"],
      ["money", "/money"],
    ]);
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
