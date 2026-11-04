import { expect, test } from "@playwright/test";

test("set up an alert channel and rule", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("link", { name: "Alerts" }).click();
  await expect(page.getByRole("heading", { name: "Alerts", exact: true })).toBeVisible();

  await page.getByLabel("Channel name").fill("Ops inbox");
  await page.getByLabel("Recipients").fill("ops@e2e.test");
  await page.getByRole("button", { name: "Add channel" }).click();
  await expect(page.getByTestId("channel").filter({ hasText: "Ops inbox" })).toContainText("ops@e2e.test");

  await page.getByLabel("Rule name").fill("Prod failures");
  await page.getByLabel("When").selectOption("run_failed");
  await page.getByLabel("Environment").selectOption("prod");
  await page.getByLabel("Ops inbox").check();
  await page.getByRole("button", { name: "Add rule" }).click();
  await expect(page.getByTestId("rule").filter({ hasText: "Prod failures" })).toBeVisible();
  await expect(page.getByLabel("Rule Prod failures on")).toBeChecked();
});
