import { describe, expect, it } from "vitest";
import { toolSavedKind } from "./toolsaved";
import { madeToolWords, type MadeTool } from "./tools";
import { answerWords, waitLink, waitWords } from "./toolanswer";

const tool = (command: string, says: string[] = []): MadeTool => ({
  workflow_id: command,
  environment: "prod",
  version: 1,
  command,
  name: command,
  about: "",
  says,
  care: { level: "yes" },
  always_ask: false,
});

describe("saved things as a tool's inputs", () => {
  it("takes the x-saved hint first", () => {
    expect(toolSavedKind("branch", { type: "string", "x-saved": "place" })).toBe("place");
    expect(toolSavedKind("payee", { type: "string", "x-saved": "bank_account" })).toBe("bank_account");
    // A hint for a kind no button fills offers none, whatever the name.
    expect(toolSavedKind("phone", { type: "string", "x-saved": "file" })).toBeUndefined();
  });
  it("then its format", () => {
    expect(toolSavedKind("contact", { type: "string", format: "phone-ng" })).toBe("phone");
    expect(toolSavedKind("x", { type: "string", format: "email" })).toBe("email");
    expect(toolSavedKind("when", { type: "string", format: "date" })).toBe("date");
    expect(toolSavedKind("site", { type: "string", format: "uri" })).toBeUndefined();
  });
  it("then its name and title", () => {
    expect(toolSavedKind("customer_phone", { type: "string" })).toBe("phone");
    expect(toolSavedKind("customerEmail", { type: "string" })).toBe("email");
    expect(toolSavedKind("deliver_to", { type: "string" })).toBe("place");
    expect(toolSavedKind("x", { type: "string", title: "Delivery address" })).toBe("place");
    expect(toolSavedKind("account_number", { type: "string" })).toBe("bank_account");
    expect(toolSavedKind("due", { type: "string" })).toBe("date");
    expect(toolSavedKind("customer", { type: "string" })).toBe("person");
    expect(toolSavedKind("what_for", { type: "string", title: "What for" })).toBeUndefined();
  });
  it("offers nothing for a choice, a number or a yes/no", () => {
    expect(toolSavedKind("customer", { type: "string", enum: ["a", "b"] })).toBeUndefined();
    expect(toolSavedKind("phone", { type: "integer" })).toBeUndefined();
    expect(toolSavedKind("place", { type: "boolean" })).toBeUndefined();
  });
});

describe("words that reach a tool", () => {
  const tools = [tool("expense", ["spent", "i bought"]), tool("stock"), tool("refund", ["i bought"])];
  it("by its command with words after it", () => {
    expect(madeToolWords(tools, "/expense 4500 diesel")).toMatchObject({ by: "command", words: "4500 diesel" });
    expect(madeToolWords(tools, "/expense")).toBeUndefined();
    expect(madeToolWords(tools, "/nope 4500")).toBeUndefined();
  });
  it("by a phrase one tool says", () => {
    const r = madeToolWords(tools, "Spent  4,500 on diesel");
    expect(r?.tool.command).toBe("expense");
    expect(r).toMatchObject({ by: "phrase", words: "4,500 on diesel" });
    expect(madeToolWords(tools, "spentthrift")).toBeUndefined();
  });
  it("not by a phrase two tools say: the chat asks which", () => {
    expect(madeToolWords(tools, "i bought rice")).toBeUndefined();
  });
});

describe("who a run waits for", () => {
  it("says it in words", () => {
    expect(waitWords({ kind: "approval", who: "Ada" })).toBe("Waiting for Ada to approve.");
    expect(waitWords({ kind: "approval", who: "you", you: true })).toBe("Waiting for you to approve: open Approvals.");
    expect(waitWords({ kind: "form", who: "you", you: true, request: "r1" })).toBe("Waiting for your answer: open To fill in.");
    expect(waitWords({ kind: "form", who: "the cashiers" })).toBe("Waiting for the cashiers to answer.");
    expect(answerWords({ status: "waiting", type: "outcome", waiting_for: [{ kind: "approval", who: "an owner" }] })).toBe(
      "Waiting for an owner to approve.",
    );
    expect(answerWords({ status: "running", type: "outcome", waiting_for: [{ kind: "form", who: "Bayo Stores" }] })).toBe(
      "Waiting for Bayo Stores to answer.",
    );
  });
  it("links where the person acts", () => {
    expect(waitLink([{ kind: "form", who: "you", you: true, request: "r1" }])).toEqual({ to: "/forms/requests/r1", label: "To fill in" });
    expect(waitLink([{ kind: "approval", who: "you", you: true }])).toEqual({ to: "/approvals", label: "Approvals" });
    expect(waitLink([{ kind: "approval", who: "Ada" }])).toBeUndefined();
  });
});
