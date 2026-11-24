import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Recipes and Describe it (build plan M6), at a phone's width: the
// template library shows the recipes with the tool each makes; installing
// one takes its command, the next free one when it is taken, and says so;
// a draft Describe it makes is read back in plain steps with its tool.
// axe in both themes, nothing wider than the page.
test.use({ viewport: { width: 390, height: 844 }, bypassCSP: true });

async function check(page: Page, where: string) {
  const found: string[] = [];
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await page.waitForTimeout(150);
    const vs = await axe(page);
    if (vs.length) found.push(describe(`${where} (${scheme})`, vs));
  }
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  expect(found, found.join("\n\n")).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(390);
}

async function signIn(page: Page) {
  await page.goto("/login?with=email");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

test("the library's recipes make a tool, called by the word chosen at install", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page);
  await page.goto("/templates");
  await page.getByTestId("recipes").click();
  await expect(page.getByRole("button", { name: /Look something up from the chat/ })).toBeVisible();
  await expect(page.getByText("Use it from the chat: /find-customer")).toBeVisible();
  await expect(page.locator(".template-card")).toHaveCount(8);
  await check(page, "recipes");

  await page.getByRole("button", { name: /Look something up from the chat/ }).click();
  const tool = page.getByTestId("recipe-tool");
  await expect(tool).toContainText("Use it from the chat: /find-customer");
  await expect(tool).toContainText("Answers at once: it only reads");
  await expect(tool).toContainText("Says back a card");
  await expect(page.getByLabel("Steps")).toContainText("• Use it from the chat with /find-customer: Look up");
  const word = `stock-${Date.now() % 100000}`;
  await page.getByLabel("Its word").fill(`/${word.replace("-", " ")}`);
  await page.getByRole("button", { name: "Create draft" }).click();
  await expect(page.getByTestId("recipe-installed")).toContainText(`It is called with /${word}.`);
  await check(page, "a recipe installed");
  // Again: the word is taken, so it gets the next free one, and says so.
  await page.getByRole("button", { name: "Create draft" }).click();
  await expect(page.getByTestId("recipe-installed")).toContainText(`/${word} is taken, so it is called with /${word}-2.`);
});

test("Describe it reads a drafted tool back in plain steps", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page);
  // The model is not set up in the browser tests: the build's answers are
  // the server's own shape, as TestDescribeItMakesAWorkingTool gets them.
  const definition = {
    schema: "wd/v1",
    id: "wf_prices",
    version: 1,
    name: "Our prices",
    trigger: { type: "manual" },
    inputs: { schema: { type: "object", properties: {} } },
    outputs: [{ name: "prices", type: "string", title: "Prices", description: "The price list", value: "=steps.read.output.prices" }],
    tool: { name: "Our prices", command: "prices-now", about: "Shows the price list", for: "members", answer: { type: "text", text: "=outputs.prices" } },
    steps: [{ id: "read", type: "transform", name: "Read the price list", config: { output: { prices: "=saved.price_list.body" } } }],
  };
  await page.route("**/v1/ai/status", (r) => r.fulfill({ json: { enabled: true, provider: "anthropic", model: "claude-opus-5-5" } }));
  await page.route("**/v1/ai/build", (r) => r.fulfill({ status: 202, json: { id: "00000000-0000-7000-8000-000000000042" } }));
  await page.route("**/v1/ai/builds/00000000-0000-7000-8000-000000000042", (r) =>
    r.fulfill({
      json: {
        id: "00000000-0000-7000-8000-000000000042",
        status: "proposed",
        stage: "done",
        error: null,
        saved_workflow_id: null,
        saved_version: null,
        proposal: {
          definition,
          summary: "A tool, prices-now, that answers with the price list.",
          assumptions: [],
          problems: [],
          warnings: [],
          results: [{ name: "happy path", source: "generated", passed: true, status: "completed" }],
          rounds: 1,
          valid_first_try: true,
          connectors: [],
          templates_offered: [],
          usage: { input_tokens: 10, output_tokens: 10, cache_creation_tokens: 0, cache_read_tokens: 0 },
          model: "claude-opus-5-5",
          tables: [],
          needs: [],
        },
        read_back: [
          { number: "1", depth: 0, text: "When you start it" },
          { number: "2", depth: 0, text: "Read the price list" },
          { number: "•", depth: 0, text: "Use it from the chat with /prices-now: Our prices" },
          { number: "•", depth: 0, text: "Your own people can use it" },
          { number: "•", depth: 0, text: "It answers with words" },
        ],
      },
    }),
  );
  await page.goto("/workflows/new");
  await page.getByRole("button", { name: /Describe it/ }).click();
  await page.getByLabel("What should the workflow do?").fill("When I type prices-now, show me @price-list");
  await page.getByRole("button", { name: "Draft it" }).click();
  await expect(page.getByTestId("draft-tool")).toContainText("A tool for the chat: /prices-now · Our prices");
  const back = page.getByLabel("In plain steps");
  await expect(back).toContainText("• Use it from the chat with /prices-now: Our prices");
  await expect(back).toContainText("• It answers with words");
  await check(page, "Describe it, a tool");
});
