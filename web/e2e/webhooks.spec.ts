import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";
import { newWorkflow } from "./builder";

// axe is injected into the page, which the Content Security Policy would refuse.
test.use({ bypassCSP: true });

const signIn = async (page: Page) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
};

test("a test call shows what a sender sends before publishing", async ({ page }) => {
  await signIn(page);
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Workflows" }).click();
  await newWorkflow(page, "Shop orders by webhook", "Another app tells it");
  await page.getByRole("tab", { name: "Settings" }).click();
  await page.getByRole("combobox", { name: "Authentication" }).selectOption("bearer");
  await page.getByRole("checkbox", { name: "PUT" }).check();

  const card = page.getByLabel("Test call");
  await card.getByRole("button", { name: "Listen for a test call" }).click();
  const address = card.locator("pre").first();
  await expect(address).toHaveText(/\/hooks\/[0-9a-f-]+\/_listen\/[0-9a-f-]+\/[0-9a-f]+$/);
  const vs = await axe(page);
  expect(vs, describe("webhook settings", vs)).toEqual([]);

  // The other system calls: a PUT the webhook now takes, with a token the
  // environment has no secret for yet.
  const resp = await page.request.put((await address.textContent()) ?? "", {
    data: { order: "A1", total: 500 },
    headers: { Authorization: "Bearer guess", "X-Shop": "lagos-1" },
  });
  expect(resp.status()).toBe(200);

  await expect(card.getByText("PUT", { exact: true })).toBeVisible({ timeout: 10_000 });
  await expect(card.getByText(/✓ Method: would be taken/)).toBeVisible();
  await expect(card.getByText(/✗ Authentication: prod has no secret webhook_/)).toBeVisible();
  await expect(card.getByText(/"order": "A1"/)).toBeVisible();
  await expect(card.getByText(/"x-shop": "lagos-1"/)).toBeVisible();
  await expect(card.getByText("guess")).toHaveCount(0);

  // The body becomes the inputs schema.
  await card.getByRole("button", { name: "Use as the inputs schema" }).click();
  await expect(page.getByLabel("Inputs schema")).toHaveValue(/"total": \{\s*"type": "integer"/);
});
