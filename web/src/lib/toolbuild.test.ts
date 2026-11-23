import { describe, expect, it } from "vitest";
import type { Output } from "@sdk/wd";
import {
  commandProblem,
  COMMAND,
  firstAnswer,
  itemField,
  itemValue,
  newTool,
  outputName,
  pickOf,
  pickValue,
  sentenceParts,
  sentenceText,
  suggestCommand,
  toolProblem,
  toolProblemWords,
} from "./toolbuild";

const out = (name: string, type: Output["type"], title: string): Output => ({
  name,
  type,
  title,
  description: title,
  value: `=steps.s.output.${name}`,
});

describe("suggestCommand", () => {
  it("makes a command from a name", () => {
    expect(suggestCommand("Log an expense")).toBe("log-an-expense");
    expect(suggestCommand("  Leave left?  ")).toBe("leave-left");
    expect(suggestCommand("Café orders")).toBe("cafe-orders");
    expect(suggestCommand("2024 Stock count")).toBe("stock-count");
  });
  it("keeps to 2 to 24 letters, cut where a word ends", () => {
    const long = suggestCommand("Record what every customer bought today");
    expect(long.length).toBeLessThanOrEqual(24);
    expect(long).toBe("record-what-every");
    expect(suggestCommand("x")).toBe("x-tool");
    expect(suggestCommand("!!")).toBe("my-tool");
  });
  it("always gives a command the validator takes", () => {
    for (const n of ["A", "Log an expense", "123", "Ọjà", "a b c d e f g h i j k l m n o p"])
      expect(COMMAND.test(suggestCommand(n)), n).toBe(true);
  });
});

describe("commandProblem", () => {
  it("says what is wrong in words", () => {
    expect(commandProblem("")).toMatch(/word to call it/);
    expect(commandProblem("1st")).toMatch(/letter/);
    expect(commandProblem("Log")).toMatch(/letter/);
    expect(commandProblem("log it")).toMatch(/small letters, numbers and dashes/);
    expect(commandProblem("a")).toMatch(/2 to 24/);
    expect(commandProblem("expense")).toBeUndefined();
  });
});

describe("the answer's values", () => {
  it("writes and reads back a picked output or words", () => {
    expect(pickValue({ output: "total" })).toBe("=outputs.total");
    expect(pickValue({ text: "Done" })).toBe("Done");
    expect(pickOf(pickValue({ text: "=not a formula" }))).toEqual({ text: "=not a formula" });
    expect(pickOf("=outputs.total")).toEqual({ output: "total" });
    expect(pickOf("Plain")).toEqual({ text: "Plain" });
    expect(pickOf("=outputs.a + outputs.b")).toBeUndefined();
    expect(pickOf("")).toBeUndefined();
  });
  it("makes a sentence of words and values, and reads it back", () => {
    const parts = [{ text: "Logged " }, { output: "amount" }, { text: " for " }, { output: "what" }];
    const text = sentenceText(parts);
    expect(text).toBe('="Logged " + string(outputs.amount) + " for " + string(outputs.what)');
    expect(sentenceParts(text)).toEqual(parts);
  });
  it("keeps words with quotes and plus signs", () => {
    const parts = [{ text: 'She said "yes" + more: ' }, { output: "n" }];
    expect(sentenceParts(sentenceText(parts))).toEqual(parts);
  });
  it("joins words side by side, drops empty ones, and is plain words alone", () => {
    expect(sentenceText([{ text: "All " }, { text: "done" }, { text: "" }])).toBe("All done");
    expect(sentenceParts("All done")).toEqual([{ text: "All done" }]);
    expect(sentenceText([])).toBe("");
    expect(sentenceText([{ output: "" }])).toBe("");
  });
  it("leaves a sentence made by hand alone", () => {
    expect(sentenceParts("=outputs.name + ' has ' + string(outputs.days)")).toBeUndefined();
    expect(sentenceParts("=size(outputs.rows)")).toBeUndefined();
  });
  it("reads a row's field", () => {
    expect(itemValue("name")).toBe("=item.name");
    expect(itemValue("unit price")).toBe('=item["unit price"]');
    expect(itemValue(" ")).toBe("");
    expect(itemField("=item.name")).toBe("name");
    expect(itemField('=item["unit price"]')).toBe("unit price");
    expect(itemField(undefined)).toBe("");
    expect(itemField("=item.a + item.b")).toBeUndefined();
  });
});

describe("firstAnswer", () => {
  const outputs = [out("what", "string", "What for"), out("amount_kobo", "integer", "Amount"), out("rows", "list", "Expenses")];
  it("starts a sentence with the first value", () => {
    expect(firstAnswer("text", outputs, "Log")).toEqual({ type: "text", text: '="What for: " + string(outputs.what)' });
    expect(firstAnswer("text", [], "Log")).toEqual({ type: "text", text: "Done." });
  });
  it("starts a card with the values as lines, money as its amount", () => {
    expect(firstAnswer("card", outputs, "Log")).toEqual({
      type: "card",
      title: "Log",
      lines: [{ label: "What for", value: "=outputs.what" }],
      amount: "=outputs.amount_kobo",
    });
  });
  it("starts a list from the first list", () => {
    expect(firstAnswer("list", outputs, "Log")).toEqual({ type: "list", from: "rows", title: "Expenses", item: { title: "=item.name" } });
  });
  it("is part of a new tool when there is something to show", () => {
    expect(newTool("Log an expense", outputs)).toMatchObject({ name: "Log an expense", command: "log-an-expense", about: "", answer: { type: "text" } });
    expect(newTool("", [])).toEqual({ name: "My tool", command: "my-tool", about: "" });
  });
});

describe("outputName", () => {
  it("names an output from its words, apart from the others", () => {
    expect(outputName("Total spent")).toBe("total_spent");
    expect(outputName("Total spent", ["total_spent"])).toBe("total_spent_2");
    expect(outputName("₦")).toBe("result");
    expect(/^[a-z][a-z0-9_]{0,39}$/.test(outputName("3 things, all of them, the very long name of it"))).toBe(true);
  });
});

describe("toolProblemWords", () => {
  it("says a taken or reserved command plainly", () => {
    expect(toolProblemWords(`"invoice" is one of Taskiem's own commands; choose another word`)).toBe("/invoice is one of Taskiem's own words. Choose another word.");
    expect(toolProblemWords("conflict: in prod, the command /expense is already Petty cash's; choose another")).toBe("/expense is already used by “Petty cash”. Choose another word.");
    expect(toolProblemWords("in prod, the command /expense was just taken by another workflow; choose another")).toMatch(/was just taken/);
    expect(toolProblemWords("a tool's command is 2 to 24 lowercase letters")).toBe("A tool's command is 2 to 24 lowercase letters");
  });
});

describe("toolProblem", () => {
  it("says a field's problem by the field", () => {
    expect(toolProblem({ path: "/tool/about", message: "schema: minLength: got 0, want 1", plain: "Schema: minLength" })).toMatch(/^Say in a line what it is for/);
    expect(toolProblem({ path: "/tool/name", message: "schema: maxLength" })).toMatch(/^Give it a name/);
    expect(toolProblem({ path: "/tool/command", message: "schema: pattern" })).toMatch(/2 to 24 small letters/);
  });
  it("keeps a taken word in its own words, else the server's plain words", () => {
    expect(toolProblem({ path: "/tool/command", message: `"run" is one of Taskiem's own commands; choose another word`, plain: "x" })).toBe("/run is one of Taskiem's own words. Choose another word.");
    expect(toolProblem({ path: "/tool/answer", message: "no output is called \"x\"", plain: "The answer shows something it does not give back." })).toBe("The answer shows something it does not give back.");
  });
});
