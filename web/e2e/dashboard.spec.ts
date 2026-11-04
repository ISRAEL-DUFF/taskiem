import { expect, test } from "@playwright/test";

test("the dashboard shows run health", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("link", { name: "Dashboard" }).click();
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  await expect(page.getByLabel("Success rate", { exact: true })).toBeVisible();
  await expect(page.getByRole("img", { name: "Runs per day" })).toBeVisible();
  await page.getByLabel("Period").selectOption("30");
  await expect(page.getByRole("heading", { name: "Failing connectors" })).toBeVisible();
});
