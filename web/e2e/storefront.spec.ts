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

test("an owner sets up the question form and finds the orders", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("link", { name: "Storefront" }).click();
  // The draft from the test above has the forms.
  const forms = page.getByRole("region", { name: "Forms" });
  const questions = forms.getByRole("article", { name: "Questions" });
  await expect(questions.getByText("No workflow set up yet.")).toBeVisible();
  await expect(forms.getByRole("article", { name: "Orders" }).getByRole("button", { name: "Set up orders" })).toBeVisible();

  // Set up adds the default workflow and publishes it to prod.
  await questions.getByRole("button", { name: "Set up questions" }).click();
  await expect(questions.getByRole("link", { name: "Open its workflow for questions" })).toBeVisible();
  await questions.getByRole("switch", { name: "Show this form on your page" }).check();
  await questions.getByLabel("Button label").fill("Ask us anything");
  await questions.getByRole("button", { name: "Save questions" }).click();
  await expect(questions.getByRole("status")).toHaveText("Saved. It shows on your page once you publish it.");
  await expect(questions.getByText("On", { exact: true })).toBeVisible();

  // An order offers payment, the Paystack connection and how it is collected.
  const orders = forms.getByRole("article", { name: "Orders" });
  await orders.getByRole("button", { name: "Set up orders" }).click();
  await expect(orders.getByRole("radio", { name: /Customer pays after you accept/ })).toBeChecked();
  await orders.getByRole("checkbox", { name: "Delivery" }).check();
  await orders.getByLabel("Delivery note").fill("Yaba and Surulere only");
  await orders.getByRole("radio", { name: /No online payment/ }).check();
  await expect(orders.getByLabel("Paystack connection")).toHaveCount(0);

  const vs = await axe(page);
  expect(vs, describe("/storefront (forms)", vs)).toEqual([]);

  // It comes back as saved.
  await page.reload();
  await expect(questions.getByLabel("Button label")).toHaveValue("Ask us anything");
  await expect(questions.getByRole("switch", { name: "Show this form on your page" })).toBeChecked();

  await page.getByRole("navigation", { name: "Storefront" }).getByRole("link", { name: "Orders" }).click();
  await expect(page.getByRole("heading", { name: "Orders", exact: true })).toBeVisible();
  await expect(page.getByText("No orders yet.")).toBeVisible();
  await page.getByLabel("Show").selectOption("All");
  await expect(page).toHaveURL(/status=all/);
});
