import { describe, expect, it } from "vitest";
import { catalog, idFrom, search, suggested } from "./catalog";

const connectors: { ref: string; id: string; name: string; category: string; actions: Record<string, { title: string }> }[] = [
  { ref: "whatsapp@1", id: "whatsapp", name: "WhatsApp", category: "messaging", actions: { send_buttons: { title: "Send an interactive message" }, send_message: { title: "Send a message" } } },
  { ref: "googlesheets@1", id: "googlesheets", name: "Google Sheets", category: "productivity", actions: { append_row: { title: "Add a row" } } },
  { ref: "paystack@1", id: "paystack", name: "Paystack", category: "payments", actions: { check_balance: { title: "Check the balance" } } },
  { ref: "internal@1", id: "internal", name: "Internal", category: "other", actions: { x: { title: "X" } } },
];
const capabilities = [
  { name: "collect_link", title: "Send a payment link" },
  { name: "pay_bank", title: "Pay a bank account" },
  { name: "payout.settled", title: "A payout settled", kind: "event" },
];

describe("what can be added", () => {
  const all = catalog(connectors, capabilities);
  const pick = (title: string) => {
    const p = all.find((x) => x.title === title);
    if (!p) throw new Error(`no ${title}`);
    return p;
  };
  it("groups services by what they are for, payments by what they do", () => {
    const of = (title: string) => all.find((p) => p.title === title)?.group;
    expect(of("Send a message")).toBe("Messages");
    expect(of("Add a row")).toBe("Tables and sheets");
    expect(of("Send a payment link")).toBe("Payments");
    expect(of("Check the balance")).toBe("Other services");
    expect(of("If")).toBe("Decide and repeat");
    expect(all.some((p) => p.title === "X" || p.title === "A payout settled")).toBe(false);
  });

  it("makes the step, named in words", () => {
    const wa = pick("Send a message").make("tell");
    expect(wa).toMatchObject({ id: "tell", type: "connector", connector: "whatsapp@1", action: "send_message", name: "Send a message" });
    const choice = pick("If").make("c");
    expect(choice).toMatchObject({ type: "branch", config: { paths: [{ name: "yes" }], default: { steps: [] } } });
    expect(pick("Pay a bank account").make("pay")).toMatchObject({ type: "capability", uses: "pay_bank" });
  });

  it("offers a message, an approval and a payment link first", () => {
    expect(suggested(all).map((p) => p.title)).toEqual(["Send a message", "Ask for approval", "Send a payment link"]);
  });

  it("finds by every word, in the title, the service or the group", () => {
    expect(search(all, "whatsapp send").map((p) => p.title)).toEqual(["Send an interactive message", "Send a message"]);
    expect(search(all, "payments").map((p) => p.title)).toEqual(["Send a payment link", "Pay a bank account"]);
    expect(search(all, "")).toEqual([]);
  });

  it("names a new step's id in short words", () => {
    expect(pick("Run code").base).toBe("code");
    expect(pick("Send a message").base).toBe("send_message");
    expect(pick("Pay a bank account").base).toBe("pay_bank");
    expect(idFrom("Send a message", new Set())).toBe("send_a_message");
    expect(idFrom("Send a message", new Set(["send_a_message"]))).toBe("send_a_message_2");
    expect(idFrom("1st reminder!", new Set())).toBe("s_1st_reminder");
  });
});
