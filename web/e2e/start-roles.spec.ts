import { expect, test } from "@playwright/test";
import { axe } from "./a11y";
import { addStep, doneWithStep, newWorkflow } from "./builder";

// axe is injected into the page, which the Content Security Policy would refuse.
test.use({ bypassCSP: true });

// Who can start it (decision 0070): picked from the business's roles in
// Settings, published with the workflow, and kept.
test("an owner says who can start a workflow, and it is published with it", async ({ page }) => {
  await page.goto("/login?with=email");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Workflows" }).click();

  await newWorkflow(page, "Count the stock");
  await addStep(page, "change the data", "Change the data");
  await doneWithStep(page);
  await page.getByRole("tab", { name: "Settings" }).click();
  const who = page.getByTestId("start-roles");
  await expect(who).toContainText("Everyone who can start runs");
  // Owners always may, so they are not offered.
  await expect(who.getByRole("checkbox", { name: /^Owner/ })).toHaveCount(0);
  await who.getByRole("checkbox", { name: /^operator/ }).check();
  await expect(who).toContainText("Only owners and operator, however they start it.");
  expect(await axe(page)).toEqual([]);

  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/is published; new runs use it/)).toBeVisible();
  await page.reload();
  await page.getByRole("tab", { name: "Settings" }).click();
  await expect(page.getByTestId("start-roles").getByRole("checkbox", { name: /^operator/ })).toBeChecked();
});
