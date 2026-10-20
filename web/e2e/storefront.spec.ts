import { expect, test } from "@playwright/test";
import { axe, describe } from "./a11y";

// axe is injected into the page, which the Content Security Policy would refuse.
test.use({ bypassCSP: true });

test("an owner drafts a storefront", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("link", { name: "Storefront" }).click();
  await expect(page.getByRole("heading", { name: "Storefront", exact: true })).toBeVisible();
  await expect(page.getByText("You have no storefront yet.")).toBeVisible();

  // The address follows the name until one is typed.
  await page.getByLabel("Business name").fill("Mama Put Kitchen");
  await expect(page.getByLabel("Page address")).toHaveValue("mama-put-kitchen");
  await page.getByLabel("Tagline").fill("Hot jollof and swallow");
  await page.getByLabel("Phone").fill("0803 123 4567");
  await page.getByRole("radio", { name: /Bold/ }).check();
  await page.getByRole("radio", { name: "blue" }).check();

  // Hours: Monday open, Tuesday copies it.
  await page.getByRole("checkbox", { name: "Monday open" }).check();
  await page.getByLabel("Monday opens").fill("08:00");
  await page.getByLabel("Monday closes").fill("18:30");
  await page.getByRole("button", { name: "Tuesday: same as Monday" }).click();
  await expect(page.getByLabel("Tuesday closes")).toHaveValue("18:30");

  const vs = await axe(page);
  expect(vs, describe("/storefront", vs)).toEqual([]);

  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Draft saved." })).toBeVisible();
  await expect(page.getByText("Not published yet")).toBeVisible();

  // It comes back as saved.
  await page.reload();
  await expect(page.getByLabel("Business name")).toHaveValue("Mama Put Kitchen");
  await expect(page.getByLabel("Tuesday opens")).toHaveValue("08:00");
  await expect(page.getByRole("radio", { name: /Bold/ })).toBeChecked();
});
