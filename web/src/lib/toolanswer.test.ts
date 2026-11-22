import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import {
  answerTone,
  answerWords,
  buttonValues,
  careWords,
  firstRows,
  nextAsk,
  startErrorWords,
  stillGoing,
  valuesLine,
} from "./toolanswer";
import { madeCare, madeIcon, madeToolsFor, type MadeTool } from "./tools";

describe("the answer", () => {
  it("draws a state's tone as the kit's pill", () => {
    expect(answerTone("good")).toBe("lime");
    expect(answerTone("warn")).toBe("warn");
    expect(answerTone("bad")).toBe("danger");
    expect(answerTone("plain")).toBe("plain");
    expect(answerTone(undefined)).toBe("plain");
  });
  it("is asked for again while the run is on its way, gently", () => {
    expect(["running", "queued", "waiting"].every(stillGoing)).toBe(true);
    expect(["completed", "failed", "cancelled", "needs_reconciliation"].some(stillGoing)).toBe(false);
    expect(nextAsk(0)).toBe(1000);
    expect(nextAsk(2)).toBe(2000);
    expect(nextAsk(50)).toBe(5000);
  });
  it("says where the run is, in words", () => {
    expect(answerWords({ status: "running", type: "outcome" })).toBe("Working on it…");
    expect(answerWords({ status: "waiting", type: "outcome" })).toMatch(/^Waiting for/);
    expect(answerWords({ status: "queued", type: "outcome" })).toMatch(/^Queued/);
    expect(answerWords({ status: "failed", type: "outcome", error: "Paystack said no" })).toBe("It did not finish: Paystack said no");
    expect(answerWords({ status: "failed", type: "outcome" })).toBe("It did not finish.");
    expect(answerWords({ status: "cancelled", type: "outcome" })).toMatch(/stopped/);
    expect(answerWords({ status: "completed", type: "outcome" })).toBe("Done.");
    expect(answerWords({ status: "completed", type: "text", text: "Logged" })).toBeUndefined();
  });
  it("shows a list's first ten rows, and how many more", () => {
    const rows = Array.from({ length: 24 }, (_, i) => i);
    expect(firstRows(rows)).toEqual({ shown: rows.slice(0, 10), more: 14 });
    expect(firstRows([1, 2])).toEqual({ shown: [1, 2], more: 0 });
  });
  it("fills a tool's form from a button", () => {
    expect(buttonValues({ label: "Close", tool: "close-ticket", inputs: { ref: "T-1", gone: null } })).toEqual({ ref: "T-1" });
    expect(buttonValues({ label: "Open", tool: "x" })).toEqual({});
  });
  it("puts the values in a line", () => {
    expect(valuesLine([{ name: "a", label: "Amount", value: "4500" }, { name: "w", label: "What for", value: "Diesel" }])).toBe("Amount: 4500 · What for: Diesel");
  });
});

describe("startErrorWords", () => {
  it("says a refusal plainly", () => {
    expect(startErrorWords(new ApiError(403, "only managers can start Log"))).toBe("This tool is not yours to use: only managers can start Log.");
    expect(startErrorWords(new ApiError(404, "no tool /x"))).toMatch(/not there any more/);
    expect(startErrorWords(new ApiError(422, "the values do not fit the tool's form", ["amount: must be a number"]))).toBe("Some answers do not fit: amount: must be a number.");
    expect(startErrorWords(new ApiError(429, "the plan's 100 runs a day are used; more tomorrow"))).toBe("Not started: the plan's 100 runs a day are used; more tomorrow");
    expect(startErrorWords(new ApiError(409, "that confirmation has expired; start the tool again"))).toBe("That confirmation has expired; start the tool again");
    expect(startErrorWords(new Error("Not confirmed"))).toBe("Not confirmed");
  });
});

describe("careWords", () => {
  it("says how careful a tool is", () => {
    expect(careWords("answers")).toBe("Answers at once");
    expect(careWords("yes")).toBe("Asks yes first");
    expect(careWords("step_up")).toBe("Asks for your passkey");
    expect(careWords(undefined)).toBe("");
  });
});

const tool = (command: string, name: string, extra: Partial<MadeTool> = {}): MadeTool => ({
  workflow_id: "w",
  environment: "prod",
  version: 1,
  command,
  name,
  about: "",
  says: [],
  care: { level: "answers" },
  always_ask: false,
  ...extra,
});

describe("Your tools", () => {
  const tools = [tool("expense", "Log an expense"), tool("leave-left", "Days of leave"), tool("pay-staff", "Pay staff", { care: { level: "step_up" } })];
  it("are found by their command or the start of their name or a word of it", () => {
    expect(madeToolsFor(tools, "").map((t) => t.command)).toEqual(["expense", "leave-left", "pay-staff"]);
    expect(madeToolsFor(tools, "exp").map((t) => t.command)).toEqual(["expense"]);
    expect(madeToolsFor(tools, "/leave").map((t) => t.command)).toEqual(["leave-left"]);
    expect(madeToolsFor(tools, "Log").map((t) => t.command)).toEqual(["expense"]);
    expect(madeToolsFor(tools, "staff").map((t) => t.command)).toEqual(["pay-staff"]);
    expect(madeToolsFor(tools, "zzz")).toEqual([]);
  });
  it("ask yes when an owner says always ask, and never less", () => {
    expect(madeCare(tool("a", "A"))).toBe("answers");
    expect(madeCare(tool("a", "A", { always_ask: true }))).toBe("yes");
    expect(madeCare(tool("a", "A", { always_ask: true, care: { level: "step_up" } }))).toBe("step_up");
  });
  it("are drawn with the app's icons", () => {
    expect(madeIcon("receipt")).toBe("invoices");
    expect(madeIcon("money")).toBe("money");
    expect(madeIcon(undefined)).toBe("bolt");
  });
});
