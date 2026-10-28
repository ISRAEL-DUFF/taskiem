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

test("a draft saved without a trigger opens as a manual one", async ({ page }) => {
  await signIn(page);
  // Drafts may be incomplete; the builder still draws one with no trigger.
  const id = await workflow(page, "No trigger flow", { schema: "wd/v1", steps: [] });
  await page.goto(`/workflows/${id}`);
  await expect(page.getByRole("heading", { level: 1, name: "No trigger flow" })).toBeVisible();
  await expect(page.getByRole("heading", { level: 1, name: "This page stopped working" })).toHaveCount(0);
});

test("a page that fails says so and leaves the rest of the console working", async ({ page }) => {
  await signIn(page);
  const id = await workflow(page, "Broken answer flow", { schema: "wd/v1", trigger: { type: "manual" }, steps: [] });
  // An answer the builder cannot draw at all, as a broken server might send.
  await page.route(`**/v1/workflows/${id}/versions/1`, (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ definition: null, layout: {}, problems: [], version: { version: 1, digest: "x" } }) }));
  await page.goto(`/workflows/${id}`);
  await expect(page.getByRole("heading", { level: 1, name: "This page stopped working" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Try again" })).toBeVisible();
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.getByRole("link", { name: "Go to Home" }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
});

test("leaving the builder with unsaved changes asks first", async ({ page }) => {
  await signIn(page);
  const id = await workflow(page, "Leave flow", {
    schema: "wd/v1", id: "wf_leave", version: 1, name: "Leave", trigger: { type: "manual" },
    steps: [{ id: "start", type: "transform", config: { output: "first" } }],
  });
  await page.goto(`/workflows/${id}`);
  await expect(page.getByTestId("node-start")).toBeVisible();
  // Saved: leaving asks nothing.
  const home = page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Home" });
  await home.click();
  await expect(page).toHaveURL(/\/home$/);
  await page.goto(`/workflows/${id}`);
  await page.getByRole("tab", { name: "JSON" }).click();
  const json = page.locator("textarea.mono");
  await json.fill((await json.inputValue()).replace('"first"', '"mine"'));
  await expect(page.getByText("Unsaved changes")).toBeVisible();

  // Another page of the console: asked, and staying keeps the edits.
  const asked: string[] = [];
  page.once("dialog", (d) => (asked.push(d.message()), void d.dismiss()));
  await home.click();
  await expect.poll(() => asked).toEqual([expect.stringContaining("unsaved changes")]);
  await expect(page).toHaveURL(new RegExp(`/workflows/${id}$`));
  await expect(page.locator("textarea.mono")).toHaveValue(/"mine"/);

  // Closing the tab: the browser asks.
  const kinds: string[] = [];
  page.once("dialog", (d) => (kinds.push(d.type()), void d.dismiss()));
  await page.close({ runBeforeUnload: true });
  await expect.poll(() => kinds).toEqual(["beforeunload"]);
  expect(page.isClosed()).toBe(false);
});

test("a step edited as JSON keeps its id; the ID field renames it", async ({ page }) => {
  await signIn(page);
  const id = await workflow(page, "Step JSON flow", {
    schema: "wd/v1", id: "wf_stepjson", version: 1, name: "Step JSON", trigger: { type: "manual" },
    steps: [
      { id: "first", type: "transform", config: { output: "one" } },
      { id: "second", type: "transform", needs: ["first"], config: { output: "two" } },
    ],
  });
  await page.goto(`/workflows/${id}`);
  await page.getByTestId("node-first").click();
  const win = page.getByTestId("step-window");
  await win.getByText("Edit this step as JSON").click();
  const json = win.locator("details textarea.mono");
  await json.fill((await json.inputValue()).replace('"id": "first"', '"id": "second"').replace('"one"', '"uno"'));
  await expect(win.getByTestId("step-json-problem")).toContainText('The id stays "first"');
  // Nothing was taken: two steps, still called first and second.
  await expect(page.getByTestId("node-first")).toHaveCount(1);
  await expect(page.getByTestId("node-second")).toHaveCount(1);
  await expect(page.getByText("Unsaved changes")).toHaveCount(0);

  // The ID field checks the id is free, and renames what runs after it.
  const field = win.getByLabel("ID");
  await field.fill("second");
  await field.blur();
  await expect(win.getByText("That id is taken.")).toBeVisible();
  await field.fill("renamed");
  await field.blur();
  await expect(page.getByTestId("node-renamed")).toHaveCount(1);
  await win.getByRole("button", { name: "Done" }).click();
  await page.getByRole("tab", { name: "JSON" }).click();
  const whole = JSON.parse(await page.locator("textarea.mono").inputValue()) as { steps: { id: string; needs?: string[] }[] };
  expect(whole.steps.map((s) => [s.id, s.needs ?? []])).toEqual([["renamed", []], ["second", ["renamed"]]]);
});
