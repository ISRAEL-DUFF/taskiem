import { expect, test, type Browser, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Sizes and colours in trade (docs/spec/chat-categories-and-variants.md,
// phase 4): a retailer orders a trainer from its supplier by size, in one
// sheet: 36 pairs in 7 sizes at the list's price (one size dearer), then 50
// pairs in mixed sizes at the 50-pair price, counted across sizes; a new
// product of its own is made for them, its sizes matched by the server
// (api/trade_sizes_test.go). The supplier's side is answered here.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@sizes.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function linked(browser: Browser, viewport: { width: number; height: number }) {
  const stamp = letters();
  const supplier = await (await browser.newContext()).newPage();
  await signUp(supplier, `Zuri Wholesale ${stamp}`);
  const buyer = await (await browser.newContext({ viewport })).newPage();
  await signUp(buyer, `Lekki Kicks ${stamp}`);
  await supplier.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`zuri wholesale ${stamp}`)}`, { headers: H })).json();
  await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } });
  const id = (await (await supplier.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  await supplier.request.post(`/v1/links/${id}/accept`, { headers: H });
  const conv = (await (await buyer.request.get("/v1/link-chats", { headers: H })).json()).conversations[0];
  await buyer.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });
  return { buyer, link: conv.link as string, name: conv.name as string };
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(300);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

async function openOrder(page: Page) {
  const pane = page.getByTestId("chat-pane");
  await pane.getByRole("button", { name: "Open tools" }).click();
  await page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Order/ }).first().click();
}

const SIZES = ["40", "41", "42", "43", "44", "45", "46"];

async function trade(page: Page, link: string, name: string) {
  const sent: { path: string; body: unknown }[] = [];
  const remember = async (route: Route) => {
    const r = route.request();
    sent.push({ path: new URL(r.url()).pathname, body: r.postData() ? r.postDataJSON() : undefined });
  };
  const trainer = {
    item_id: "am", name: "Air Max 90", unit: "pair", price_kobo: 4_000_000, min_quantity: 6, available: true,
    breaks: [{ min_quantity: 50, price_kobo: 3_600_000 }],
    choices: [{ name: "Size", values: SIZES }, { name: "Colour", values: ["Black"] }],
    sizes: SIZES.map((z) => ({
      item_id: `s${z}`, name: `Air Max 90 – ${z}, Black`, variant: { Size: z, Colour: "Black" }, available: true,
      ...(z === "46" ? { price_kobo: 4_400_000 } : {}),
    })),
  };
  const now = new Date().toISOString();
  await page.route(`**/v1/link-chats/${link}/items`, (r) => r.fulfill(json({ items: [trainer], whose: "theirs" })));
  await page.route("**/v1/supplier-prices?*", (r) => r.fulfill(json({ suppliers: [{ link, business: "sup", name, items: [trainer], updated_at: now }] })));
  await page.route(`**/v1/item-links/${link}`, (r) => r.fulfill(json({ item_links: [] })));
  await page.route("**/v1/stock", (r) => r.fulfill(json({ keeps_stock: true, catalogue: "products", items: [] })));
  await page.route("**/v1/stock/suppliers", (r) =>
    r.fulfill(json({ suppliers: [{ id: "sup", name, phone: "0803 123 4567", reached: "link", link_id: link, owed_kobo: 0, open: 0, invited_at: null, created_at: now, retired_at: null }] })),
  );
  await page.route("**/v1/stock/purchases", (r) => r.fulfill(json({ purchases: [] })));
  await page.route("**/v1/stock/items", async (r) => (await remember(r), r.fulfill(json({ item: { id: "mine" } }, 201))));
  const purchase = { id: "u2", reference: "PO-0050", supplier_id: "sup", supplier: name, link_id: link, lines: [], total_kobo: 0, status: "draft",
    drafted_by: "me", approved_by: null, sent_at: null, confirmed_at: null, confirmed_how: null, counter: null, counter_at: null, received_at: null,
    cancelled_at: null, received_kobo: 0, paid_kobo: 0, owed_kobo: 0, created_at: now, seller_status: "" };
  await page.route(`**/v1/supplier-prices/${link}/order`, async (r) => (await remember(r), r.fulfill(json({ purchase }, 201))));
  await page.route("**/v1/stock/purchases/u2/send", async (r) => (await remember(r), r.fulfill(json({ purchase: { ...purchase, status: "sent" } }))));
  return sent;
}

for (const viewport of [
  { width: 390, height: 844 },
  { width: 360, height: 780 },
]) {
  test(`a retailer orders a trainer by size, and gets the 50-pair price across sizes, at ${viewport.width}`, async ({ browser }) => {
    test.setTimeout(120_000);
    const { buyer, link, name } = await linked(browser, viewport);
    const sent = await trade(buyer, link, name);
    await buyer.goto(`/chat?with=${link}`);
    await openOrder(buyer);
    const order = buyer.getByTestId("order-sheet");
    await expect(order.getByTestId("order-row")).toHaveCount(1);
    await order.getByRole("button", { name: "Choose sizes of Air Max 90" }).click();
    const grid = order.getByTestId("order-sizes");
    const fill = async (n: Record<string, number>) => {
      for (const [z, q] of Object.entries(n)) await grid.getByRole("textbox", { name: `${z}, Black: how many` }).fill(String(q));
    };
    // Under its minimum in all: not yet.
    await fill({ "42": 4 });
    await expect(order.getByTestId("order-bag")).toContainText("Air Max 90: at least 6 in all");
    await expect(order.getByRole("button", { name: "Review order" })).toBeDisabled();
    // 36 pairs in 7 sizes: the list's price, 46 dearer.
    await fill({ "40": 4, "41": 5, "42": 6, "43": 6, "44": 6, "45": 5, "46": 4 });
    await expect(order.getByRole("button", { name: /36 pairs in 7 sizes and colours/ })).toBeVisible();
    await expect(order.getByTestId("order-bag")).toContainText("₦1,456,000");
    // 50 pairs in mixed sizes: the 50-pair price for those at the product's.
    await fill({ "42": 20 });
    await expect(order.getByRole("button", { name: /50 pairs in 7 sizes and colours/ })).toBeVisible();
    await expect(order.getByTestId("order-bag")).toContainText("₦1,832,000");
    expect(await buyer.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await axeBoth(buyer, "ordering a trainer by size");

    // Review: a line a size; one question for the product, a new one of ours.
    await order.getByRole("button", { name: "Review order" }).click();
    await expect(order).toContainText("20 × Air Max 90 – 42, Black");
    await expect(order.getByRole("combobox", { name: /^Air Max 90 Its sizes and colours go into yours/ })).toHaveValue("new");
    await axeBoth(buyer, "reviewing an order by size");
    await order.getByRole("button", { name: "Place order" }).click();
    await expect(order).toHaveCount(0, { timeout: 5000 });
    expect(sent.find((x) => x.path === "/v1/stock/items")?.body).toMatchObject({ name: "Air Max 90", unit: "pair", quantity: 0 });
    const body = sent.find((x) => x.path.endsWith("/order"))?.body as { lines: { seller_item_id: string; quantity: number; item_id?: string }[] };
    expect(body.lines).toHaveLength(7);
    expect(body.lines.every((l) => l.item_id === "mine")).toBe(true);
    expect(body.lines.find((l) => l.seller_item_id === "s42")?.quantity).toBe(20);
  });
}
