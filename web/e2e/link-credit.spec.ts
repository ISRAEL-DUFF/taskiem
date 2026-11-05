import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Statements and terms between linked businesses (business links L5,
// docs/spec/business-links.md §10): the seller gives a linked buyer 14
// days and a ₦500,000 limit and invoices it; each side's statement shows
// the same amount, with the terms, at a phone's width, passing axe in
// both themes. api/link_credit_test.go covers limits and reminders.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@credit.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
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

test("a statement on each side, with the terms", async ({ browser }) => {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Lekki Flour ${stamp}`);
  const buyer = await (await browser.newContext()).newPage();
  await signUp(buyer, `Musa Bakery ${stamp}`);
  expect((await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } })).status()).toBe(200);
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`lekki flour ${stamp}`)}`, { headers: H })).json();
  expect((await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } })).status()).toBe(201);
  const link = (await (await seller.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  expect((await seller.request.post(`/v1/links/${link}/accept`, { headers: H })).status()).toBe(200);
  const buyerID = (await (await buyer.request.get("/v1/me", { headers: H })).json()).tenant_id as string;

  // 14 days and ₦500,000; an invoice of ₦120,000, due in 14 days.
  const tr = await seller.request.put(`/v1/links/${link}/trade`, { headers: H, data: { terms_days: 14, credit_limit_kobo: 50_000_000 } });
  expect(tr.status(), await tr.text()).toBe(200);
  const inv = await seller.request.post("/v1/invoices", {
    headers: { ...H, "Idempotency-Key": `inv-${stamp}` },
    data: { customer: { name: "Musa Bakery", key: `link:${buyerID}` }, lines: [{ description: "Flour", quantity: 10, unit_kobo: 1_200_000 }] },
  });
  expect(inv.status(), await inv.text()).toBe(201);
  const x = await inv.json();
  expect(x.due_on).toBeTruthy();
  expect((await seller.request.post(`/v1/invoices/${x.id}/send`, { headers: { ...H, "Idempotency-Key": `send-${stamp}` } })).status()).toBe(200);

  await seller.goto(`/links/${link}/statement`);
  const theirs = seller.getByTestId("statement-invoice");
  await expect(theirs).toContainText("₦120,000");
  await expect(theirs).toContainText("14 days to pay");
  await expect(theirs).toContainText("₦500,000");
  await expect(theirs).toContainText("₦380,000");
  await expect(seller.getByTestId("statement-doc")).toContainText(x.number);
  await axeBoth(seller, "the seller's statement");

  // The buyer's: the same amount, once the bill is mirrored.
  await expect(async () => {
    await buyer.goto(`/links/${link}/statement`);
    await expect(buyer.getByTestId("statement-bill")).toContainText("₦120,000", { timeout: 2_000 });
  }).toPass({ timeout: 30_000 });
  await expect(buyer.getByTestId("statement-bill")).toContainText("14 days to pay");
  await expect(buyer.getByTestId("statement-doc")).toContainText(x.number);
  await axeBoth(buyer, "the buyer's statement");
});
