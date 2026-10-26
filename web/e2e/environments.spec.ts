import { expect, test } from "@playwright/test";
import { addStep, doneWithStep, newWorkflow } from "./builder";

test("a gated environment takes a version by promotion", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();

  // uat takes versions only by promotion from dev.
  await page.getByRole("link", { name: /Secrets/ }).click();
  await page.getByLabel("New environment").fill("uat");
  await page.getByLabel("Takes versions").selectOption("dev");
  await page.getByRole("button", { name: "Add", exact: true }).click();
  await expect(page.getByLabel("Gate for uat")).toHaveValue("dev");

  await page.getByRole("link", { name: "Workflows" }).click();
  await newWorkflow(page, "Promoted flow");
  await addStep(page, "change the data", "Change the data");
  await doneWithStep(page);
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/is published; new runs use it/)).toBeVisible();
  await page.getByRole("tab", { name: "Settings" }).click();

  const deployments = page.getByLabel("Deployments");
  await expect(deployments).toContainText("uat not deployed");
  await deployments.getByRole("button", { name: "Promote v2 from dev" }).click();
  await expect(deployments).toContainText("uat v2");
  await expect(deployments.getByRole("button", { name: /Promote/ })).toHaveCount(0);
});
