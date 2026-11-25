import { randomUUID } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Money › Transactions (docs/spec/transactions-page.md): every payment in
// and out, from the side menu and Money's own row, filtered and searched,
// with the totals for the filter, and one transaction on its own page; at
// a phone's width, in both themes.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } }); // axe is injected into the page

async function signIn(page: Page) {
  await page.goto("/login?with=email");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

async function enter(page: Page, data: Record<string, unknown>) {
  const made = await page.request.post("/v1/money/entries", {
    headers: { "X-Taskiem-Request": "1", "Idempotency-Key": randomUUID() },
    data,
  });
  expect(made.status(), await made.text()).toBe(201);
}

test("every payment in and out, filtered, searched and opened", async ({ page }) => {
  await signIn(page);
  // An account of this test's own, so its rows are this test's alone.
  const till = `till-${randomUUID().slice(0, 8)}`;
  await enter(page, { kind: "cash", direction: "in", amount_kobo: 1234500, description: "Transactions e2e sales", channel: till });
  await enter(page, { kind: "expense", amount_kobo: 345600, description: "Transactions e2e diesel", vendor: "Total", channel: till });

  // From Money's row, for the period Money shows.
  await page.goto("/money");
  await page.getByTestId("money-transactions").click();
  await expect(page).toHaveURL(/\/money\/transactions\?period=today$/);
  await expect(page.getByRole("heading", { level: 1, name: "Transactions" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Money" }).getByRole("link", { name: "Transactions" })).toHaveAttribute("aria-current", "page");
  await expect(page.getByTestId("tx-totals")).toContainText("Money in · Today");

  // Only this till: the two, and their totals.
  await page.getByRole("combobox", { name: "Where" }).selectOption(till);
  const rows = page.getByTestId("tx-row");
  await expect(rows).toHaveCount(2);
  await expect(page.getByTestId("tx-totals")).toContainText("₦12,345");
  await expect(page.getByTestId("tx-totals")).toContainText("₦3,456 out");
  await expect(page.getByTestId("tx-totals")).toContainText("2 transactions");
  await expect(page.getByTestId("tx-totals")).toContainText("Filtered");
  await expect(page.getByTestId("tx-csv").getByRole("link", { name: "Download the period" })).toHaveAttribute("href", /\/v1\/money\/exports\/log\?from=/);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await axeBoth(page, "/money/transactions");

  // Money out only; then a search by amount.
  await page.getByRole("combobox", { name: "In or out" }).selectOption("out");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("−₦3,456");
  await expect(page.getByTestId("tx-totals")).toContainText("Money out");
  await page.getByRole("combobox", { name: "In or out" }).selectOption("");
  await page.getByRole("searchbox").fill("12,345");
  await expect(page).toHaveURL(/q=12%2C345/);
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("Transactions e2e sales");
  await expect(rows.first()).toContainText("Not matched");

  // Its own page: the amount on the hero, then the details.
  await rows.first().click();
  await expect(page.getByRole("heading", { level: 1, name: "Transactions e2e sales" })).toBeVisible();
  await expect(page.getByTestId("tx-hero")).toContainText("+₦12,345");
  await expect(page.getByTestId("tx-hero")).toContainText("Money in");
  await expect(page.getByTestId("tx-details")).toContainText("Entered by hand");
  await expect(page.getByTestId("tx-counts")).toContainText("Not matched to an invoice or order yet.");
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await axeBoth(page, "/money/transactions/:id");
  await page.getByRole("link", { name: "Back to Transactions" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Transactions" })).toBeVisible();

  // Dates that cannot be a period are refused on the page.
  await page.goto("/money/transactions?period=custom&from=2026-10-05&to=2026-10-01");
  await expect(page.getByRole("alert")).toHaveText("The last day is on or after the first.");
  await expect(page.getByTestId("tx-csv")).toHaveCount(0);
});

test("the side menu has Transactions under Money", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await signIn(page);
  const menu = page.getByRole("navigation", { name: "Main" });
  await menu.getByRole("link", { name: "Transactions" }).click();
  await expect(page).toHaveURL(/\/money\/transactions$/);
  await expect(page.getByRole("group", { name: "Period" }).getByRole("button", { name: "This month" })).toHaveAttribute("aria-pressed", "true");
  await expect(menu.getByRole("link", { name: "Transactions" })).toHaveClass(/active/);
  await expect(menu.getByRole("link", { name: "Money", exact: true })).not.toHaveClass(/active/);
});
