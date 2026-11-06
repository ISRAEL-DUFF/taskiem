import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Chat tools, phase 2 (docs/spec/chat-tools.md): from chat, an owner
// changes a price, records stock in, accepts an order and adds a service,
// and each change is told as a diary line where the tool was opened. The
// tools' own API calls are answered here (they have tests of their own);
// the diary lines are the server's.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@work.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function linked(browser: Browser, viewport: { width: number; height: number }) {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Rivers Foods ${stamp}`);
  const owner = await (await browser.newContext({ viewport })).newPage();
  await signUp(owner, `Bayo Rice ${stamp}`);
  await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (await owner.request.get(`/v1/links/search?q=${encodeURIComponent(`rivers foods ${stamp}`)}`, { headers: H })).json();
  await owner.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "customer" } });
  const id = (await (await seller.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  await seller.request.post(`/v1/links/${id}/accept`, { headers: H });
  const link = (await (await owner.request.get("/v1/link-chats", { headers: H })).json()).conversations[0].link as string;
  await owner.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });
  return { owner, seller, link };
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

/** The owner's own APIs the tools write, answered and remembered. */
async function shop(page: Page) {
  const sent: { method: string; path: string; body: unknown }[] = [];
  const items = [
    { id: "s1", name: "Rice, 50kg bag", available: true, stock: 42, unit: "bag", price_kobo: 7_600_000 },
    { id: "s2", name: "Rice, 25kg bag", available: true, stock: 4, low: true, unit: "bag", price_kobo: 4_100_000 },
  ];
  const remember = async (route: import("@playwright/test").Route) => {
    const r = route.request();
    sent.push({ method: r.method(), path: new URL(r.url()).pathname, body: r.postData() ? r.postDataJSON() : undefined });
  };
  await page.route("**/v1/stock", (r) => r.fulfill(json({ keeps_stock: true, catalogue: "products", items })));
  await page.route("**/v1/stock/items/**", async (r) => (await remember(r), r.fulfill(json({ item: items[0] }))));
  await page.route("**/v1/stock/movements", async (r) => (await remember(r), r.fulfill(json({ movement: { id: "m1", status: "done" } }))));
  await page.route("**/v1/trade-orders", (r) =>
    r.fulfill(json({ orders: [{ id: "o1", order_reference: "AD-7Q4K2P", buyer_name: "Rivers Foods", total_kobo: 82_000_000, status: "received", lines: [{ item: "Rice, 25kg bag", quantity: 20, price_kobo: 4_100_000 }] }] })),
  );
  await page.route("**/v1/trade-orders/o1/accept", async (r) => (await remember(r), r.fulfill(json({ order: {}, short: [] }))));
  await page.route("**/v1/storefront/services", (r) => r.fulfill(json({ items: [], availability: {}, synced_at: null, error: null })));
  await page.route("**/v1/storefront", (r) => r.fulfill(json({ services_table: "svc", services_columns: { name: "name", price: "price", duration: "minutes" } })));
  await page.route("**/v1/tables/svc/rows", async (r) => (await remember(r), r.fulfill(json({ row: { id: 1 } }, 201))));
  await page.route("**/v1/storefront/catalogue/refresh", (r) => r.fulfill(json({})));
  return sent;
}

for (const [size, viewport] of [
  ["a phone", { width: 390, height: 844 }],
  ["a computer", { width: 1440, height: 900 }],
] as const) {
  test(`from chat on ${size}: a price, stock in, an order and a service, each a diary line`, async ({ browser }) => {
    test.setTimeout(180_000);
    const { owner, seller, link } = await linked(browser, viewport);
    const sent = await shop(owner);
    await owner.goto(`/chat?with=${link}`);
    const pane = owner.getByTestId("chat-pane");
    const sheet = owner.getByTestId("tool-sheet");
    const diary = pane.getByTestId("chat-diary");
    const tool = async (name: string) => {
      await pane.getByRole("button", { name: "Open tools" }).click();
      await owner.getByRole("region", { name: "Tools" }).getByRole("button", { name: new RegExp(`^${name}`) }).click();
      await expect(sheet).toBeVisible();
    };

    // A price changed.
    await tool("Products");
    await sheet.getByRole("button", { name: /Rice, 50kg bag/ }).click();
    await expect(sheet.getByRole("heading", { name: "Rice, 50kg bag" })).toBeVisible();
    await axeBoth(owner, "a product's form");
    await sheet.getByLabel("Price in naira").fill("78,000");
    await sheet.getByRole("button", { name: "Save" }).click();
    await expect(owner.getByRole("status").filter({ hasText: "Saved" })).toBeVisible();
    await expect(sheet).toHaveCount(0, { timeout: 5000 });
    await expect(diary.last()).toContainText("Price of Rice, 50kg bag: ₦76,000 → ₦78,000");
    expect(sent.find((x) => x.method === "PATCH")?.body).toEqual({ price_kobo: 7_800_000 });

    // Stock in.
    await tool("Stock");
    await sheet.getByRole("button", { name: /Rice, 25kg bag/ }).click();
    await sheet.getByRole("button", { name: "One more" }).click();
    await sheet.getByRole("button", { name: "Record stock in" }).click();
    await expect(sheet).toHaveCount(0, { timeout: 5000 });
    await expect(pane).toContainText("Stock in: Rice, 25kg bag +2 bags (now 6 bags)");
    expect(sent.find((x) => x.path === "/v1/stock/movements")?.body).toMatchObject({ item_id: "s2", reason: "received", change: 2 });

    // An order accepted.
    await tool("Orders");
    await sheet.getByRole("button", { name: /AD-7Q4K2P/ }).click();
    await expect(sheet).toContainText("20 × Rice, 25kg bag");
    await sheet.getByRole("button", { name: "Accept" }).click();
    await expect(sheet).toHaveCount(0, { timeout: 5000 });
    await expect(pane).toContainText("Accepted order AD-7Q4K2P from Rivers Foods");

    // A service added, with how long it takes and its price.
    await tool("Services");
    await sheet.getByRole("button", { name: "Add service" }).click();
    await sheet.getByLabel("Name").fill("Rice milling, per bag");
    await sheet.getByLabel("Price in naira").fill("2,500");
    await axeBoth(owner, "adding a service");
    await sheet.getByRole("button", { name: "Add service" }).click();
    await expect(sheet).toHaveCount(0, { timeout: 5000 });
    await expect(pane).toContainText("New service: Rice milling, per bag, 30 min, ₦2,500");
    expect(sent.find((x) => x.path === "/v1/tables/svc/rows")?.body).toMatchObject({ row: { name: "Rice milling, per bag" } });

    // The changes are ours alone: the other business sees none of them.
    const theirs = (await (await seller.request.get(`/v1/link-chats/${link}/messages`, { headers: H })).json()).messages as { kind: string }[];
    expect(theirs.filter((m) => m.kind === "diary")).toEqual([]);
    await axeBoth(owner, "diary lines in a conversation");
    expect(await owner.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewport.width);
  });
}

test("in the Taskiem chat, a change is a diary line there", async ({ browser }) => {
  const { owner } = await linked(browser, { width: 390, height: 844 });
  const sent = await shop(owner);
  await owner.goto("/chat?with=taskiem");
  await owner.getByRole("button", { name: "Open tools" }).click();
  await owner.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Products/ }).click();
  const sheet = owner.getByTestId("tool-sheet");
  await sheet.getByRole("button", { name: /Rice, 25kg bag/ }).click();
  await sheet.getByRole("button", { name: "One more" }).click();
  await sheet.getByRole("button", { name: "Save" }).click();
  await expect(owner.getByTestId("chat-diary")).toContainText("Stock of Rice, 25kg bag counted: 4 bags → 5 bags");
  expect(sent.find((x) => x.path === "/v1/stock/movements")?.body).toMatchObject({ reason: "count", count: 5 });
  // Kept: still there after a reload.
  await owner.reload();
  await expect(owner.getByTestId("chat-diary")).toContainText("counted: 4 bags → 5 bags");
});
