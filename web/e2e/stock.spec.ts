import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// A shop's stock (docs/stock.md): the Stock page before the catalogue
// keeps stock, and with items, low ones first, every change listed, and
// the dialogs to add an item and record stock. serve.sh runs no Xenibase,
// so Keep my stock is refused as tables are; the page with items is
// served from fixtures.
test.use({ bypassCSP: true }); // axe is injected into the page

const H = { "X-Taskiem-Request": "1" };

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: "Mama Put Stores",
      email: `ada-${Date.now()}@stock.test`,
      name: "Ada",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

const stock = {
  keeps_stock: true,
  catalogue: "products",
  low: 1,
  published: false,
  items: [
    {
      id: "1",
      name: "Indomie",
      available: false,
      stock: 4,
      unit: "carton",
      cost_kobo: 850000,
      reorder_level: 10,
      target_level: 50,
      supplier: "Dangote Depot",
      low: true,
      suggest: 46,
    },
    {
      id: "2",
      name: "Rice",
      available: true,
      stock: 30,
      unit: "bag",
      price_kobo: 4500000,
      reorder_level: 5,
      low: false,
      waiting: 1,
    },
  ],
  stuck: [
    {
      id: "m9",
      item_id: "2",
      item: "Rice",
      reason: "sale",
      status: "pending",
      before: null,
      after: null,
      change: null,
      by: "user:ada",
      error: "cut off",
      created_at: "2026-10-29T08:00:00Z",
      done_at: null,
    },
  ],
};

const movements = {
  movements: [
    {
      id: "m2",
      item_id: "1",
      item: "Indomie",
      reason: "adjustment",
      status: "done",
      before: 50,
      after: 4,
      change: -46,
      note: "water damage",
      by: "user:ada",
      created_at: "2026-10-29T09:00:00Z",
      done_at: "2026-10-29T09:00:01Z",
    },
    {
      id: "m1",
      item_id: "1",
      item: "Indomie",
      reason: "received",
      status: "done",
      before: 0,
      after: 50,
      change: 50,
      by: "user:ada",
      created_at: "2026-10-28T09:00:00Z",
      done_at: "2026-10-28T09:00:01Z",
    },
    {
      id: "m0",
      item_id: "2",
      item: "Rice",
      reason: "outside",
      status: "done",
      before: 0,
      after: 30,
      change: 30,
      note: "First count seen in the table",
      by: "table",
      created_at: "2026-10-28T08:00:00Z",
      done_at: "2026-10-28T08:00:00Z",
    },
  ],
};

test("the Stock page before stock is kept, and with items", async ({
  page,
}) => {
  await signUp(page);
  await page.goto("/storefront/stock");
  await expect(
    page.getByRole("heading", { name: "Stock", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Keep your stock here" }),
  ).toBeVisible();
  await expect(
    page.getByText("Taskiem makes a products table for your items"),
  ).toBeVisible();
  await axeBoth(page, "/storefront/stock, not kept");
  // No Xenibase here: refused as making a table is.
  await page.getByRole("button", { name: "Keep my stock" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "data tables are not available",
  );

  // With items (from fixtures).
  await page.route("**/v1/stock", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(stock),
    }),
  );
  await page.route("**/v1/stock/movements?**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(movements),
    }),
  );
  await page.goto("/storefront/stock");
  const low = page.getByTestId("low-item");
  await expect(low).toHaveCount(1);
  await expect(low).toContainText("Indomie");
  await expect(low).toContainText("4 cartons");
  await expect(low).toContainText(
    "reorder at 10 · up to 50 · cost ₦8,500 · Dangote Depot · order 46 cartons",
  );
  await expect(page.getByTestId("stock-item")).toHaveCount(2);
  await expect(
    page.getByTestId("stock-item").filter({ hasText: "Rice" }),
  ).toContainText("1 change on its way");
  await expect(
    page
      .getByRole("status")
      .filter({ hasText: "not reached your products table" }),
  ).toBeVisible();
  await expect(page.getByTestId("movement")).toHaveCount(3);
  await expect(page.getByTestId("movement").first()).toContainText(
    "Indomie: adjusted -46",
  );
  await expect(page.getByTestId("movement").first()).toContainText(
    "water damage",
  );
  await expect(page.getByTestId("movement").last()).toContainText(
    "Changed in the table",
  );
  await axeBoth(page, "/storefront/stock, with items");

  // Add an item.
  await page.getByRole("button", { name: "Add items" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Name").fill("Garri");
  await dialog.getByLabel("How many you have").fill("2.5");
  await expect(dialog).toContainText("whole number");
  await dialog.getByLabel("How many you have").fill("20");
  await dialog.getByLabel("Unit").fill("bag");
  await axeBoth(page, "add an item");
  await dialog.getByRole("button", { name: "Cancel" }).click();

  // Record stock: an adjustment needs a reason, and shows the count after.
  await page
    .getByRole("button", { name: "Record stock of Indomie" })
    .first()
    .click();
  await expect(dialog).toContainText("Now: 4 cartons.");
  await dialog.getByLabel("Some were spoilt, lost or found").check();
  await dialog.getByLabel("How many").fill("1");
  await expect(dialog).toContainText("Say why");
  await dialog.getByLabel("Why").fill("spoilt");
  await expect(dialog).toContainText("The count goes from 4 → 3 cartons.");
  await axeBoth(page, "record stock");
  let sent: unknown = null;
  await page.route("**/v1/stock/movements", async (route) => {
    sent = route.request().postDataJSON();
    expect(route.request().headers()["idempotency-key"]).toBeTruthy();
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ movement: movements.movements[0] }),
    });
  });
  await dialog.getByRole("button", { name: "Record" }).click();
  await expect(dialog).toBeHidden();
  expect(sent).toEqual({
    item_id: "1",
    reason: "adjustment",
    note: "spoilt",
    change: -1,
  });
});
