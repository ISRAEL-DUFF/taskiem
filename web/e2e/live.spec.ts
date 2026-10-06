import { expect, test } from "@playwright/test";

test("the run canvas lights up as the run goes", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();

  await page.getByRole("button", { name: "New workflow" }).click();
  await page.getByLabel("Name").fill("Live flow");
  await page.getByRole("button", { name: "Create" }).click();
  await page.getByTestId("node-start").click();
  await page.getByRole("button", { name: "+ wait" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/is published; new runs use it/)).toBeVisible();
  await page.getByRole("button", { name: "Start run" }).click();
  await page.getByRole("button", { name: "Start", exact: true }).click();
  await expect(page).toHaveURL(/\/runs\//);

  // Streamed: the first step completes and the wait lights up.
  const canvas = page.getByLabel("Run canvas");
  await expect(canvas.getByTestId("node-start")).toHaveAttribute("data-status", "completed", { timeout: 30_000 });
  await expect(canvas.getByTestId("node-wait")).toHaveAttribute("data-status", "waiting");
  await expect(page.getByText("live", { exact: true })).toBeVisible();

  page.once("dialog", (d) => void d.accept());
  await page.getByRole("button", { name: "Cancel run" }).click();
  await expect(page.locator(".toolbar > .badge").first()).toHaveText("cancelled");
  await expect(canvas.getByTestId("node-wait")).toHaveAttribute("data-status", "cancelled");
  await expect(page.getByText("live", { exact: true })).toHaveCount(0);
});
