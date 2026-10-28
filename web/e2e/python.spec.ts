import { expect, test } from "@playwright/test";
import { addStep, newWorkflow } from "./builder";

test("a Python code step runs", async ({ page }) => {
  test.setTimeout(90_000); // the worker may compile CPython first
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Workflows" }).click();
  await newWorkflow(page, "Python flow");
  await addStep(page, "code", "Run code");
  await page.getByLabel("Language").selectOption("python");
  await page.getByLabel("Source").fill('from decimal import Decimal\n\ndef main(input, host):\n    print("summing")\n    return {"total": Decimal("0.10") + Decimal("0.20")}\n');
  await page.getByTestId("step-window").getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/is published; new runs use it/)).toBeVisible({ timeout: 60_000 });
  await page.getByRole("button", { name: "Start run" }).click();
  await page.getByRole("button", { name: "Start", exact: true }).click();
  await expect(page).toHaveURL(/\/runs\//);
  await page.getByRole("tab", { name: "Timeline" }).click();
  await expect(page.getByTestId("step-code").locator(".badge").first()).toHaveText("completed", { timeout: 60_000 });
  await expect(page.getByTestId("step-code")).toContainText('"0.30"');
});
