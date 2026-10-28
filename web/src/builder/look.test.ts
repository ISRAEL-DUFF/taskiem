import { describe, expect, it } from "vitest";
import type { Step } from "@sdk/wd";
import { lookOf, stepTitle } from "./look";

const connectors: { ref: string; id: string; name: string; category: string; actions: Record<string, { title?: string }> }[] = [
  { ref: "whatsapp@1", id: "whatsapp", name: "WhatsApp", category: "messaging", actions: { send_message: { title: "Send a message" } } },
  { ref: "googlesheets@1", id: "googlesheets", name: "Google Sheets", category: "productivity", actions: { find_row: { title: "Find a row" } } },
  { ref: "paystack@1", id: "paystack", name: "Paystack", category: "payments", actions: {} },
];

describe("a step's look", () => {
  it("names the service and what it does there, with the service's kind of icon", () => {
    const wa = { id: "tell", type: "connector", connector: "whatsapp@1", action: "send_message" } as Step;
    expect(lookOf(wa, connectors)).toEqual({ icon: "chat", tone: "ok", title: "Send a message", line: "WhatsApp · Send a message" });
    const sheet = { id: "check", type: "connector", connector: "googlesheets@1", action: "find_row" } as Step;
    expect(lookOf(sheet, connectors).icon).toBe("tables");
    const pay = { id: "link", type: "connector", connector: "paystack@1", action: "create_payment_link" } as Step;
    expect(lookOf(pay, connectors)).toMatchObject({ icon: "card", tone: "warn", line: "Paystack · Create payment link" });
  });

  it("says a choice is an If with Yes or No, and hides the technical steps' words", () => {
    const choice = { id: "c", type: "branch", config: { paths: [{ name: "yes", when: "=true", steps: [] }], default: { steps: [] } } } as Step;
    expect(lookOf(choice).line).toBe("If · Yes or No");
    expect(lookOf({ id: "x", type: "transform", config: { output: {} } } as Step).line).toBe("Advanced · change the data");
  });

  it("uses a step's own name first", () => {
    const wa = { id: "tell", type: "connector", connector: "whatsapp@1", action: "send_message", name: "Tell the customer" } as Step;
    expect(stepTitle(wa, connectors)).toBe("Tell the customer");
    expect(stepTitle({ ...wa, name: undefined } as Step, connectors)).toBe("Send a message");
  });
});
