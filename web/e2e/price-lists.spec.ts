import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Price lists between linked businesses (business links L4,
// docs/spec/business-links.md §9): the seller's list built from its stock
// items and shared; the buyer's view of a supplier's prices, ordered from
// with its own item said once. serve.sh runs no Xenibase, so no business
// keeps stock: stock and the lists are served from fixtures, at a phone's
// width, passing axe in both themes. api/price_lists_test.go covers the
// rest against the real API.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `p-${Date.now()}-${Math.random().toString(36).slice(2, 8)}@prices.test`, name: "Musa", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
}

const stock = {
  keeps_stock: true,
  catalogue: "products",
  items: [
    { id: "r1", name: "Rice, 50 kg", available: true, stock: 40, unit: "bag", price_kobo: 4_500_000 },
    { id: "g1", name: "Garri", available: true, stock: 10, unit: "bag", price_kobo: 1_500_000 },
  ],
};

test("a seller's price list, its items and who sees it", async ({ page }) => {
  await signUp(page, "Dangote Depot");
  const list = { id: "pl1", name: "Trade prices", items: [] as unknown[], links: [] as string[], updated_at: "2026-10-31T09:00:00Z" };
  let saved: unknown;
  await page.route("**/v1/stock", (route) => route.fulfill(json(stock)));
  await page.route("**/v1/links", (route) =>
    route.fulfill(json({ links: [{ id: "l1", business: "b1", name: "Ada's Kitchen", state: "active", role: "customer", asked_by_us: false, requested_at: "2026-10-30T09:00:00Z", expires_at: "2026-11-30T09:00:00Z" }], sent: [] })),
  );
  await page.route("**/v1/price-lists**", async (route) => {
    const req = route.request();
    if (req.method() === "PUT" && req.url().endsWith("/items")) {
      saved = req.postDataJSON();
      list.items = (saved as { items: { item_id: string; price_kobo: number; min_quantity: number }[] }).items.map((i) => ({ ...i, name: "Rice, 50 kg", unit: "bag", available: true }));
    }
    if (req.method() === "PUT" && req.url().endsWith("/links")) list.links = (req.postDataJSON() as { links: string[] }).links;
    return route.fulfill(json({ lists: [list] }));
  });

  await page.goto("/storefront/price-lists");
  await expect(page.getByTestId("price-list")).toContainText("Trade prices");
  await axeBoth(page, "/storefront/price-lists");

  // Rice at ₦42,000, at least 5.
  await page.getByRole("button", { name: /^Items/ }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Rice, 50 kg (bag)").check();
  await dialog.getByLabel("Price (₦)").fill("42,000");
  await dialog.getByLabel("At least").fill("5");
  await axeBoth(page, "a price list's items");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toHaveCount(0);
  expect(saved).toEqual({ items: [{ item_id: "r1", price_kobo: 4_200_000, min_quantity: 5, available: true }] });

  // Shared with Ada's Kitchen.
  await page.getByRole("button", { name: /^Share/ }).click();
  await page.getByRole("dialog").getByLabel("Ada's Kitchen").check();
  await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("price-list")).toContainText("shared with Ada's Kitchen");
  await axeBoth(page, "a shared price list");
});

test("a supplier's prices, ordered from", async ({ page }) => {
  await signUp(page, "Ada's Kitchen");
  let ordered: unknown;
  await page.route("**/v1/stock", (route) => route.fulfill(json({ ...stock, items: [{ id: "a1", name: "Rice", available: true, stock: 3, unit: "bag" }] })));
  await page.route("**/v1/supplier-prices?**", (route) =>
    route.fulfill(
      json({
        suppliers: [
          {
            link: "l1",
            business: "Dangote Stores",
            name: "Trade prices",
            updated_at: "2026-10-31T09:00:00Z",
            items: [
              { item_id: "r1", name: "Rice, 50 kg", unit: "bag", price_kobo: 4_200_000, min_quantity: 5, available: true },
              { item_id: "g1", name: "Garri", unit: "bag", price_kobo: 1_400_000, min_quantity: 1, available: false },
            ],
          },
        ],
      }),
    ),
  );
  await page.route("**/v1/item-links/l1", (route) => route.fulfill(json({ item_links: [] })));
  await page.route("**/v1/supplier-prices/l1/order", async (route) => {
    ordered = route.request().postDataJSON();
    return route.fulfill(json({ purchase: { id: "p1" } }, 201));
  });

  await page.goto("/storefront/suppliers/l1/prices");
  await expect(page.getByRole("heading", { name: "Dangote Stores's prices" })).toBeVisible();
  const rows = page.getByTestId("supplier-price");
  await expect(rows.first()).toContainText("₦42,000 a bag · at least 5");
  await expect(rows.nth(1)).toContainText("not available now");
  await axeBoth(page, "a supplier's prices");

  // Too few, then enough; which of ours it is, said once.
  await page.getByLabel("How many Rice, 50 kg").fill("2");
  await expect(rows.first()).toContainText("at least 5");
  await page.getByLabel("How many Rice, 50 kg").fill("10");
  await expect(page.getByRole("button", { name: "Draft the order" })).toBeDisabled();
  await rows.first().getByRole("combobox").selectOption("a1");
  await expect(page.getByText("Total ₦420,000")).toBeVisible();
  await axeBoth(page, "ordering from a supplier's prices");
  await page.getByRole("button", { name: "Draft the order" }).click();
  await expect(page).toHaveURL(/\/purchases\/p1$/);
  expect(ordered).toEqual({ lines: [{ seller_item_id: "r1", quantity: 10, item_id: "a1" }] });
});
