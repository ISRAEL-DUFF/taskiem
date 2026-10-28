import { expect, test, type Page } from "@playwright/test";

// The JSON tab takes only a definition the builder can draw, and a page
// that fails says so in plain words instead of blanking the app.

const H = { "X-Taskiem-Request": "1" };

async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

async function workflow(page: Page, name: string, definition: unknown): Promise<string> {
  const r = await page.request.post("/v1/workflows", { headers: H, data: { name, definition } });
  expect(r.ok(), `${r.status()} ${await r.text()}`).toBeTruthy();
  return String(((await r.json()) as { id: string }).id);
}

test("the JSON tab refuses a definition the builder cannot draw, and keeps the edits", async ({ page }) => {
  await signIn(page);
  const id = await workflow(page, "JSON tab flow", {
    schema: "wd/v1", id: "wf_jsontab", version: 1, name: "JSON tab", trigger: { type: "manual" },
    steps: [{ id: "start", type: "transform", config: { output: "first" } }],
  });
  await page.goto(`/workflows/${id}`);
  await expect(page.getByTestId("node-start")).toBeVisible();
  await page.getByRole("tab", { name: "JSON" }).click();
  const json = page.locator("textarea.mono");
  // A change that works is taken...
  await json.fill((await json.inputValue()).replace('"first"', '"mine"'));
  await expect(page.getByTestId("json-problem")).toHaveCount(0);
  // ...a definition without its steps is not.
  await json.fill('{"name": "no steps", "trigger": {"type": "manual"}}');
  await expect(page.getByTestId("json-problem")).toContainText('"steps" must be a list');
  await json.fill('{"trigger": {"type": "manual"}, "steps": [{"id": "a", "type": "branch", "config": {"paths": [null]}}]}');
  await expect(page.getByTestId("json-problem")).toContainText("paths 1 must be an object");
  await page.getByRole("tab", { name: "Build" }).click();
  await expect(page.getByTestId("node-start")).toBeVisible();
  await expect(page.getByText("Unsaved changes")).toBeVisible();
  await page.getByRole("tab", { name: "JSON" }).click();
  await expect(page.locator("textarea.mono")).toHaveValue(/"mine"/);
});

test("a page that fails says so and leaves the rest of the console working", async ({ page }) => {
  await signIn(page);
  // A draft the API stores (drafts may be incomplete) but the builder
  // cannot draw: it has no trigger.
  const id = await workflow(page, "No trigger flow", { schema: "wd/v1", steps: [] });
  await page.goto(`/workflows/${id}`);
  await expect(page.getByRole("heading", { level: 1, name: "This page stopped working" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Try again" })).toBeVisible();
  await page.getByRole("link", { name: "Go to Home" }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
});
