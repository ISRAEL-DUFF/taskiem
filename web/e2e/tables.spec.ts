import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// A table Taskiem keeps (docs/data-tables.md#kept-tables), seeded by
// serve.sh: its page reads, searches and exports, says why nothing can be
// changed, and offers no control that would change it. axe is injected,
// which the console's Content Security Policy would refuse.
test.use({ bypassCSP: true });

const KEPT = "0192e2e0-0000-7000-8000-000000000001";

async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

for (const scheme of ["light", "dark"] as const) {
  test(`a kept table is read-only, and passes axe in the ${scheme} theme`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await signIn(page);
    await page.goto("/tables");
    await expect(page.getByRole("link", { name: "Money log" })).toBeVisible();
    await expect(page.getByText("kept by Taskiem")).toBeVisible();
    await page.goto(`/tables/${KEPT}`);
    await expect(page.getByRole("heading", { level: 1, name: "Money log" })).toBeVisible();
    await expect(page.locator(".skeleton")).toHaveCount(0);
    await expect(page.getByRole("note")).toContainText("This table is kept by Taskiem: rows are added by Taskiem and cannot be changed or deleted. Corrections are new rows.");
    // The organisation has no project of its own: no note about editing it there.
    await expect(page.getByRole("note")).not.toContainText("outside Taskiem");
    // The owner may edit tables, but not this one.
    for (const name of ["Add a row", "Add a column", "Delete table", "Import", "Drop", "Edit", "Delete"]) {
      await expect(page.getByRole("button", { name, exact: true })).toHaveCount(0);
    }
    await expect(page.getByLabel("Import CSV")).toHaveCount(0);
    // Reading stays.
    await expect(page.getByRole("link", { name: "Export CSV" })).toBeVisible();
    await expect(page.getByLabel("Search rows")).toBeVisible();
    await expect(page.getByText("only read it")).toBeVisible();
    const vs = await axe(page);
    expect(vs, describe(`/tables/${KEPT}`, vs)).toEqual([]);
  });
}
