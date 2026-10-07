import { expect, test } from "@playwright/test";

test("encryption keys: rotate the tenant key, and a customer key is checked before use", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("link", { name: "Encryption keys" }).click();
  await expect(page.getByRole("heading", { name: "Encryption keys" })).toBeVisible();
  await expect(page.getByTestId("keys-summary")).toContainText("wrapped by Taskiem's key");

  // Rotation adds a version; re-wrapping runs in the background.
  page.once("dialog", (d) => void d.accept());
  await page.getByRole("button", { name: "Rotate tenant key" }).click();
  await expect(page.getByRole("status")).toContainText(/Version \d+ is current/);
  await expect(page.getByRole("cell", { name: "current" })).toBeVisible();

  // Bring your own key: nothing is saved unless the key works. A plain
  // http address is refused before any call.
  const byok = page.getByTestId("keys-byok");
  await expect(byok.getByRole("button", { name: "Verify and use this key" })).toBeDisabled();
  await byok.getByLabel("Address").fill("http://bao.example.com:8200");
  await byok.getByLabel("Key name").fill("taskiem");
  await byok.getByRole("textbox", { name: /^Token/ }).fill("s.not-a-real-token");
  await byok.getByRole("button", { name: "Verify and use this key" }).click();
  await expect(byok.getByRole("alert")).toContainText("https");
  await expect(page.getByTestId("keys-summary")).toContainText("wrapped by Taskiem's key");
});
