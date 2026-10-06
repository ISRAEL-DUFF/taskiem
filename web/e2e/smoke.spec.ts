import { expect, test, type Page } from "@playwright/test";

// SCREENSHOT_DIR=... saves screenshots of the main screens for review.
const shot = async (page: Page, name: string) => {
  if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: `${process.env.SCREENSHOT_DIR}/${name}.png`, fullPage: true });
};

test("sign in, build, publish, run, and verify the audit chain", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/login/);
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();

  // Create a workflow; the editor opens on its first draft.
  await page.getByRole("button", { name: "New workflow" }).click();
  await page.getByLabel("Name").fill("Smoke flow");
  await page.getByRole("button", { name: "Create" }).click();
  await expect(page.getByTestId("node-start")).toBeVisible();

  // Add a code step after the first one, publish, start a run.
  await page.getByTestId("node-start").click();
  await page.getByRole("button", { name: "+ code" }).click();
  await expect(page.getByTestId("node-code")).toBeVisible();

  // The code view shows the workflow as TypeScript; an edit there applies.
  await page.getByRole("tab", { name: "Code" }).click();
  const code = page.getByLabel("Workflow code");
  await expect(code).toHaveValue(/\.next\("code", code\(/);
  await code.fill((await code.inputValue()).replace(/name: "([^"]*)"/, 'name: "$1 (from code)"'));
  await page.getByRole("button", { name: "Apply to workflow" }).click();
  await expect(page.getByRole("button", { name: "Apply to workflow" })).toBeDisabled();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.getByRole("tab", { name: "Canvas" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/is published; new runs use it/)).toBeVisible();
  await page.getByTestId("node-code").click();
  await shot(page, "editor");

  await page.getByRole("button", { name: "Start run" }).click();
  await page.getByRole("dialog").locator("textarea").fill('{"n": 2}');
  await page.getByRole("button", { name: "Start", exact: true }).click();
  await expect(page).toHaveURL(/\/runs\//);
  await expect(page.getByTestId("step-code").locator(".badge")).toHaveText("completed", { timeout: 30_000 });
  await expect(page.locator("h1 + .badge, .toolbar > .badge").first()).toHaveText("completed");
  await shot(page, "run");

  // The run shows in the list, and the audit chain verifies.
  await page.getByRole("link", { name: "Runs" }).click();
  await expect(page.getByRole("cell", { name: /Smoke flow/ })).toBeVisible();
  await page.getByRole("link", { name: "Audit log" }).click();
  await expect(page.getByText("workflow.publish")).toBeVisible();
  await page.getByRole("button", { name: "Verify chain" }).click();
  await expect(page.getByTestId("audit-verdict")).toContainText("intact");

  await page.getByRole("link", { name: "Approvals" }).click();
  await expect(page.getByText("Nothing waiting for you.")).toBeVisible();
});
