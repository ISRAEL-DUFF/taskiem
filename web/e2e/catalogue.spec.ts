import { expect, test } from "@playwright/test";

// serve.sh publishes p_e2e_ledger 1.0.0 in the catalogue (seed-catalogue.sh).
test("browse the connector catalogue and install a connector with consent", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("link", { name: "Connector catalogue" }).click();
  await expect(page.getByRole("heading", { name: "Connector catalogue", exact: true })).toBeVisible();

  const card = page.getByRole("article", { name: "Example ledger" });
  await expect(card).toContainText("by E2E Ledgerworks");
  await expect(card).toContainText("create_payment (reconcilable write)");
  await card.getByRole("button", { name: "Install p_e2e_ledger@1" }).click();

  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("ledger.example.com");
  await expect(dialog).toContainText("create_payment: changes things; checked before any retry");
  const install = dialog.getByRole("button", { name: "Install", exact: true });
  await expect(install).toBeDisabled();
  await dialog.getByLabel("I agree to these hosts and changes for this organisation").check();
  await install.click();

  await expect(page.getByRole("heading", { name: "Installed" })).toBeVisible();
  await expect(page.getByRole("cell", { name: "p_e2e_ledger@1" })).toBeVisible();
  await expect(card).toContainText("installed 1.0.0");
});
