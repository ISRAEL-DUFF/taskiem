import { expect, test, type Page } from "@playwright/test";
import { axe } from "./a11y";

// Your tools (made tools M1, decision 0070): an owner makes a workflow
// started by hand a tool in the builder (Use it from chat), publishes it,
// finds it under Your tools in the chat's tools and by "/", runs it and
// sees its answer; made to always ask, it asks yes first.

// axe is injected into the page, which the Content Security Policy would refuse.
test.use({ bypassCSP: true });

const H = { headers: { "X-Taskiem-Request": "1" } };

async function signIn(page: Page) {
  await page.goto("/login?with=email");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

/** A draft workflow started by hand that gives back what it was told. */
async function draft(page: Page, name: string): Promise<string> {
  const definition = {
    schema: "wd/v1",
    id: `wf_${name.replace(/[^A-Za-z]+/g, "")}`,
    version: 1,
    name,
    trigger: { type: "manual" },
    inputs: {
      schema: {
        type: "object",
        required: ["what"],
        properties: {
          what: { type: "string", title: "What for" },
          amount: { type: "integer", title: "Amount" },
        },
      },
    },
    steps: [{ id: "note", type: "transform", config: { output: { said: "=trigger.body.what" } } }],
    outputs: [{ name: "what", type: "string", title: "What for", description: "What it was for", value: "=steps.note.output.said" }],
  };
  const r = await page.request.post("/v1/workflows", { ...H, data: { name, definition } });
  expect(r.ok(), await r.text()).toBeTruthy();
  return ((await r.json()) as { id: string }).id;
}

test("an owner makes a workflow a tool, uses it from chat, and makes it ask first", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page);
  const id = await draft(page, "Petty cash note");

  // Use it from chat, in the builder's Settings.
  await page.goto(`/workflows/${id}`);
  await page.getByRole("tab", { name: "Settings" }).click();
  const card = page.getByTestId("use-from-chat");
  await card.getByRole("switch", { name: "Use it from chat" }).check();
  await expect(card.getByLabel("Its name")).toHaveValue("Petty cash note");
  await expect(card.getByLabel("The word to call it by")).toHaveValue("petty-cash-note");
  await expect(card).toContainText("Typed as /petty-cash-note");
  await card.getByLabel("What it is for").fill("Note what money went on");
  // A sentence, picked from what it gives back: words, then the value.
  await expect(card.getByRole("radio", { name: /A sentence/ })).toBeChecked();
  await card.getByLabel("Words, part 1").fill("Noted: ");
  await expect(card.getByLabel("Value, part 2")).toHaveValue("what");
  await expect(card).toContainText("It says: Noted: [What for]");
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/is published; new runs use it/)).toBeVisible();
  await expect(card.getByTestId("tool-care")).toContainText("Answers at once");

  // Your tools in the Taskiem chat: "/" and the start of its name.
  await page.goto("/chat?with=taskiem");
  const box = page.getByLabel("Message to Taskiem");
  await box.fill("/petty");
  const panel = page.getByTestId("tools-panel");
  await expect(panel.getByTestId("your-tools")).toHaveText("Your tools");
  await panel.getByRole("button", { name: /Petty cash note/ }).click();
  const sheet = page.getByTestId("made-tool-sheet");
  await expect(sheet.getByRole("heading", { name: "Petty cash note" })).toBeVisible();
  await sheet.getByLabel("What for").fill("Diesel");
  await sheet.getByLabel("Amount").fill("4500");
  expect(await axe(page)).toEqual([]);
  await sheet.getByRole("button", { name: "Go" }).click();
  // It only reads: started at once, with what it read, then its answer.
  await expect(sheet.getByLabel("What was read")).toContainText("Diesel");
  await expect(sheet.getByTestId("tool-answer")).toHaveText(/Noted: Diesel/, { timeout: 30_000 });
  await expect(sheet.getByTestId("tool-answer")).toHaveAttribute("data-status", "completed");
  expect(await axe(page)).toEqual([]);
  await sheet.getByRole("button", { name: "Close" }).click();

  // The owner makes it always ask, on its page: then it asks yes first.
  await page.goto("/tools/petty-cash-note");
  const pageSheet = page.getByTestId("made-tool-sheet");
  await expect(pageSheet).toBeVisible();
  await pageSheet.getByRole("button", { name: "Close" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Petty cash note" })).toBeVisible();
  await page.getByRole("switch", { name: "Always ask before it runs" }).check();
  await expect(page.getByText("It asks yes before it runs")).toBeVisible();
  await expect(page.getByTestId("tool-page")).toContainText("Asks yes first");
  await page.getByRole("button", { name: "Use Petty cash note" }).click();
  await pageSheet.getByLabel("What for").fill("Fuel");
  await pageSheet.getByRole("button", { name: "Next" }).click();
  await expect(pageSheet.getByLabel("What will run")).toContainText("Fuel");
  await expect(pageSheet).toContainText("Check what will run, then say yes.");
  expect(await axe(page)).toEqual([]);
  await pageSheet.getByRole("button", { name: "Yes", exact: true }).click();
  await expect(pageSheet.getByTestId("tool-answer")).toHaveText(/Noted: Fuel/, { timeout: 30_000 });

  // A taken word is said plainly: another workflow asking for the same one.
  const other = await draft(page, "Another note");
  await page.goto(`/workflows/${other}`);
  await page.getByRole("tab", { name: "Settings" }).click();
  const second = page.getByTestId("use-from-chat");
  await second.getByRole("switch", { name: "Use it from chat" }).check();
  await second.getByLabel("The word to call it by").fill("petty-cash-note");
  await page.getByRole("button", { name: "Publish" }).click();
  // What it is for is needed: said by the field's name, not the shape's.
  await expect(second.getByTestId("tool-problems")).toContainText("Say in a line what it is for");
  // The draft it saved is open again before it is changed.
  await expect(page.getByRole("combobox", { name: "Version" })).toHaveValue("2");
  await expect(second.getByLabel("What it is for")).toHaveValue("");
  await second.getByLabel("What it is for").fill("Another note");
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(second.getByTestId("tool-problems")).toContainText("/petty-cash-note is already used by “Petty cash note”. Choose another word.");
});
