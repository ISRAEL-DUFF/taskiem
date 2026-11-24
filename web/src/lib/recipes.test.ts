import { describe, expect, it } from "vitest";
import { commandOf, initialValues, installedWords, lineLabel, recipeWords, toParams, type TemplateParam } from "./templates";

// Recipes: templates that make a tool used from the chat (made tools §13,
// build plan M6).
describe("recipes", () => {
  it("writes a typed word as a command", () => {
    expect(commandOf(" /Add Sale ")).toBe("add-sale");
    expect(commandOf("price-of")).toBe("price-of");
    const p: TemplateParam[] = [{ name: "command", type: "command", title: "Its word", description: "d", required: true, example: "add-sale" }];
    expect(toParams(p, { command: "/Find Customer" })).toEqual({ command: "find-customer" });
  });
  it("shows who may use it, and sends the roles typed as a list", () => {
    const p: TemplateParam[] = [
      { name: "roles", type: "roles", title: "Who can use it", description: "d", required: false, default: ["owner", "manager"], example: ["owner", "manager"] },
    ];
    expect(initialValues(p)).toEqual({ roles: "owner, manager" });
    expect(toParams(p, { roles: "owner, staff ,," })).toEqual({ roles: ["owner", "staff"] });
  });
  it("says what the tool is in plain words", () => {
    expect(recipeWords({ name: "Look up", about: "a", for: "members", answer: "card", command: "find-customer", care: "answers" })).toEqual([
      "Your team uses it",
      "Answers at once: it only reads",
      "Says back a card",
    ]);
    expect(recipeWords({ name: "Today", about: "a", for: "customers", answer: "list", command: "today", care: "yes", companion: "Daily report" })).toEqual([
      "Your customers use it",
      "Asks yes first",
      "Says back a list",
      'Comes with "Daily report", a schedule whose button opens it',
    ]);
  });
  it("says the command it was given, the next free one when it was taken", () => {
    expect(installedWords({ asked: "add-sale", command: "add-sale", changed: false })).toBe("It is called with /add-sale.");
    expect(installedWords({ asked: "add-sale", command: "add-sale-2", changed: true, companion: { id: "x", name: "Daily report" } })).toBe(
      "/add-sale is taken, so it is called with /add-sale-2. Its schedule, Daily report, was saved as a draft too.",
    );
  });
  it("reads a tool's lines as bullets", () => {
    expect(lineLabel({ number: "•", depth: 0, text: "Use it from the chat with /prices: Prices" })).toBe("• Use it from the chat with /prices: Prices");
  });
});
