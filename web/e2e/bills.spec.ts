import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Bills (business links L1, docs/spec/business-links.md §6): Dangote and
// Ada are linked; Dangote's New invoice offers Ada as a linked customer
// and sends it with no phone or email; it arrives in Ada's Bills, with
// what is left to pay first, at a phone's width, passing axe in both themes.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@bills.test`, name: tenant, password: "correct horse battery" },
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

test("a linked supplier's invoice arrives as a bill", async ({ browser }) => {
  const stamp = letters();
  const sellerName = `Dangote Depot ${stamp}`;
  const buyerName = `Ada Kitchen ${stamp}`;
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, sellerName);
  const buyer = await (await browser.newContext()).newPage();
  await signUp(buyer, buyerName);

  // Linked: Ada asks Dangote as a supplier; Dangote accepts.
  expect((await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } })).status()).toBe(200);
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`, { headers: H })).json();
  const asked = await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } });
  expect(asked.status(), await asked.text()).toBe(201);
  const pending = await (await seller.request.get("/v1/links", { headers: H })).json();
  expect((await seller.request.post(`/v1/links/${pending.links[0].id}/accept`, { headers: H })).status()).toBe(200);

  // Dangote invoices Ada as a linked business: no phone or email asked.
  await seller.goto("/invoices/new");
  await seller.getByLabel("Who is it for?").selectOption({ label: `${buyerName} (linked)` });
  await expect(seller.getByLabel("Phone number")).toHaveCount(0);
  await seller.getByLabel("Amount (₦)").fill("25,000");
  await expect(seller.getByText(`Sending puts it in ${buyerName}'s bills on Taskiem.`)).toBeVisible();
  await seller.getByRole("button", { name: "Save and send" }).click();
  await expect(seller).toHaveURL(/\/invoices\/INV-/);

  // It arrives in Ada's bills within seconds.
  await expect(async () => {
    await buyer.goto("/bills");
    await expect(buyer.getByTestId("bill")).toHaveCount(1, { timeout: 1000 });
  }).toPass({ timeout: 20_000 });
  await expect(buyer.getByTestId("bills-hero")).toContainText("₦25,000");
  const row = buyer.getByTestId("bill");
  await expect(row).toContainText(sellerName);
  await expect(row).toContainText("To pay");
  await axeBoth(buyer, "/bills");
  expect(await buyer.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await row.click();
  await expect(buyer.getByTestId("bill-hero")).toContainText("₦25,000");
  await expect(buyer.getByTestId("bill-hero")).toContainText(sellerName);
  await axeBoth(buyer, "a bill");
  expect(await buyer.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);

  // Ada queries it; Dangote sees the query on the invoice and settles it.
  await buyer.getByRole("button", { name: "Query this bill" }).click();
  const sheet = buyer.getByRole("dialog");
  await sheet.getByLabel("What is wrong").fill("We got 4 cartons, not 5");
  await axeBoth(buyer, "the query sheet");
  await sheet.getByRole("button", { name: "Send the query" }).click();
  await expect(sheet).toBeHidden();
  await expect(buyer.getByTestId("bill-queried")).toBeVisible();
  await expect(buyer.getByTestId("bill-hero")).toContainText("Queried");
  await seller.reload();
  await expect(seller.getByTestId("invoice-query")).toBeVisible();
  await axeBoth(seller, "a queried invoice");
  await seller.getByLabel("What you tell them").fill("A credit note is coming.");
  await seller.getByRole("button", { name: "Settle the query" }).click();
  await expect(seller.getByTestId("invoice-query")).toBeHidden();
  await buyer.reload();
  await expect(buyer.getByTestId("bill-queried")).toHaveCount(0);
  await expect(buyer.getByTestId("bill-hero")).toContainText("To pay");
});
