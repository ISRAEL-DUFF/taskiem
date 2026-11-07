import { expect, test, type Browser, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Chat tools, phase 2 (docs/spec/chat-tools.md): from chat, an owner
// changes a price, records stock in, accepts an order and adds a service.
// The tools' own API calls are answered here (they have tests of their
// own, and the server's diary lines for them are tested in
// api/chat_diary_test.go): here, what each form sends, and what the person
// is told.
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
    { id: "s3", name: "Garri, Ijebu", available: true, stock: null, unit: "bag", price_kobo: 3_200_000 },
  ];
  const remember = async (route: Route) => {
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
  test(`from chat on ${size}: a price, stock in, an order and a service`, async ({ browser }) => {
    test.setTimeout(180_000);
    const { owner, seller, link } = await linked(browser, viewport);
    const sent = await shop(owner);
    await owner.goto(`/chat?with=${link}`);
    const pane = owner.getByTestId("chat-pane");
    const sheet = owner.getByTestId("tool-sheet");
    const saved = owner.locator(".toast", { hasText: "The change is in your Taskiem chat" });
    const tool = async (name: string) => {
      await pane.getByRole("button", { name: "Open tools" }).click();
      await owner.getByRole("region", { name: "Tools" }).getByRole("button", { name: new RegExp(`^${name}`) }).click();
      await expect(sheet).toBeVisible();
    };

    // A price changed.
    await tool("Products");
    await sheet.getByRole("button", { name: /Rice, 50kg bag/ }).click();
    await expect(sheet.getByRole("heading", { name: "Rice, 50kg bag" })).toBeVisible();
    await expect(sheet.getByRole("switch", { name: "Show on your storefront" })).toHaveAttribute("aria-checked", "true");
    await expect(sheet.getByRole("button", { name: "Add a photo" })).toBeVisible();
    await axeBoth(owner, "a product's form");
    // Esc steps back to the list, then closes.
    await owner.keyboard.press("Escape");
    await expect(sheet.getByRole("button", { name: /Rice, 50kg bag/ })).toBeVisible();
    await sheet.getByRole("button", { name: /Rice, 50kg bag/ }).click();
    await sheet.getByLabel("Price in naira").fill("78,000");
    await sheet.getByRole("switch", { name: "Show on your storefront" }).click();
    await sheet.getByRole("button", { name: "Save" }).click();
    await expect(owner.getByRole("status").filter({ hasText: "Saved" })).toBeVisible();
    await expect(sheet).toHaveCount(0, { timeout: 5000 });
    await expect(saved).toBeVisible();
    // The price and whether it shows; no count was touched, so none is sent.
    expect(sent.find((x) => x.method === "PATCH")?.body).toEqual({ price_kobo: 7_800_000, available: false });
    expect(sent.filter((x) => x.path === "/v1/stock/movements")).toEqual([]);

    // Stock in.
    await tool("Stock");
    await sheet.getByRole("button", { name: /Rice, 25kg bag/ }).click();
    await sheet.getByRole("button", { name: "One more" }).click();
    await sheet.getByRole("button", { name: "Record stock in" }).click();
    await expect(sheet).toHaveCount(0, { timeout: 5000 });
    expect(sent.find((x) => x.path === "/v1/stock/movements")?.body).toMatchObject({ item_id: "s2", reason: "received", change: 2 });

    // An order accepted.
    await tool("Orders");
    await sheet.getByRole("button", { name: /AD-7Q4K2P/ }).click();
    await expect(sheet).toContainText("20 × Rice, 25kg bag");
    await expect(sheet.getByRole("img", { name: "Placed: step 1 of 4" })).toBeVisible();
    await sheet.getByRole("button", { name: "Accept" }).click();
    await expect(sheet).toHaveCount(0, { timeout: 5000 });
    expect(sent.find((x) => x.path === "/v1/trade-orders/o1/accept")).toBeTruthy();

    // A service added, with how long it takes and its price.
    await tool("Services");
    await sheet.getByRole("button", { name: "Add service" }).click();
    await sheet.getByLabel("Name").fill("Rice milling, per bag");
    await sheet.getByLabel("Price in naira").fill("2,500");
    await axeBoth(owner, "adding a service");
    await sheet.getByRole("button", { name: "Add service" }).click();
    await expect(sheet).toHaveCount(0, { timeout: 5000 });
    expect(sent.find((x) => x.path === "/v1/tables/svc/rows")?.body).toMatchObject({ row: { name: "Rice milling, per bag" } });

    // Nothing of it reaches the other business.
    const theirs = (await (await seller.request.get(`/v1/link-chats/${link}/messages`, { headers: H })).json()).messages as { kind: string }[];
    expect(theirs.filter((m) => m.kind === "diary")).toEqual([]);
    expect(await owner.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewport.width);
  });
}

test("in the Taskiem chat, a change is a diary line there", async ({ browser }) => {
  const { owner } = await linked(browser, { width: 390, height: 844 });
  const sent = await shop(owner);
  await owner.goto("/chat?with=taskiem");
  await owner.getByRole("button", { name: "Open tools" }).click();
  await owner.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Stock/ }).first().click();
  const sheet = owner.getByTestId("tool-sheet");
  await sheet.getByRole("button", { name: /Rice, 25kg bag/ }).click();
  await sheet.getByRole("button", { name: "One more" }).click();
  await sheet.getByRole("button", { name: "Record stock in" }).click();
  await expect(sheet).toHaveCount(0, { timeout: 5000 });
  expect(sent.find((x) => x.path === "/v1/stock/movements")?.body).toMatchObject({ reason: "received", change: 2 });
  // A product with no count yet: its price saved, no count of 0 recorded;
  // the stock tool offers it for its first goods in.
  await owner.getByRole("button", { name: "Open tools" }).click();
  await owner.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Products/ }).first().click();
  await sheet.getByRole("button", { name: /Garri, Ijebu/ }).click();
  await expect(sheet).toContainText("No count yet: set one");
  await sheet.getByLabel("Price in naira").fill("33,000");
  await sheet.getByRole("button", { name: "Save" }).click();
  await expect(sheet).toHaveCount(0, { timeout: 5000 });
  expect(sent.filter((x) => x.path === "/v1/stock/movements")).toHaveLength(1);
  await owner.getByRole("button", { name: "Open tools" }).click();
  await owner.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Stock/ }).first().click();
  await expect(sheet.getByRole("button", { name: /Garri, Ijebu/ })).toContainText("No count");
  await sheet.getByRole("button", { name: "Close" }).click();

  // A line the server keeps shows as a diary line, its new value in bold.
  const r = await owner.request.post("/v1/chat-diary", { headers: H, data: { where: "taskiem", text: "Price of Rice: ₦76,000 → ₦78,000" } });
  expect(r.status()).toBe(201);
  await owner.reload();
  const line = owner.getByTestId("chat-diary").last();
  await expect(line).toContainText("Price of Rice: ₦76,000 → ₦78,000");
  await expect(line.locator("b")).toHaveText("₦78,000");
  await axeBoth(owner, "a diary line in the Taskiem chat");
});

test("typed words open the form filled in, and saving stays a tap", async ({ browser }) => {
  const { owner, link } = await linked(browser, { width: 390, height: 844 });
  const sent = await shop(owner);
  await owner.goto(`/chat?with=${link}`);
  const pane = owner.getByTestId("chat-pane");
  const box = pane.getByLabel("Message");
  await box.fill("change the price of rice 50kg to 80k");
  const chip = pane.getByTestId("request-chip");
  await expect(chip).toContainText("Change the price of Rice, 50kg bag to ₦80,000");
  await axeBoth(owner, "a typed request");
  // Nothing is saved by the words alone.
  expect(sent.filter((x) => x.method === "PATCH")).toEqual([]);
  await chip.getByRole("button").click();
  const sheet = owner.getByTestId("tool-sheet");
  await expect(sheet.getByLabel("Price in naira")).toHaveValue("80,000");
  await expect(box).toHaveValue("");
  await sheet.getByRole("button", { name: "Save" }).click();
  await expect(sheet).toHaveCount(0, { timeout: 5000 });
  expect(sent.find((x) => x.method === "PATCH")?.body).toEqual({ price_kobo: 8_000_000 });

  // Ordinary words offer nothing.
  await box.fill("How much is rice 50kg this week?");
  await expect(pane.getByTestId("request-chip")).toHaveCount(0);
});

test("a tagged product is a live card; our own services can be tagged", async ({ browser }) => {
  const { owner, link } = await linked(browser, { width: 390, height: 844 });
  await shop(owner);
  await owner.route(`**/v1/link-chats/${link}/items`, (r) =>
    r.fulfill(json({ whose: "theirs", items: [{ item_id: "i2", name: "Rice, 25kg bag", unit: "bag", price_kobo: 4_300_000, min_quantity: 5, available: true }] })),
  );
  await owner.route("**/v1/storefront/services", (r) =>
    r.fulfill(json({ items: [{ id: "sv1", name: "Rice milling", price_kobo: 250_000, duration_minutes: 30, available: true }], availability: {}, synced_at: null, error: null })),
  );
  const at = new Date().toISOString();
  await owner.route(`**/v1/link-chats/${link}/messages**`, (r) =>
    r.request().method() === "GET"
      ? r.fulfill(
          json({
            messages: [
              {
                id: "m1",
                link,
                mine: true,
                kind: "text",
                body: "@Rice, 25kg bag is almost out here, can you send 20?",
                mentions: [{ kind: "item", name: "Rice, 25kg bag", item_id: "i2", unit: "bag", price_kobo: 4_100_000 }],
                created_at: at,
              },
            ],
          }),
        )
      : r.continue(),
  );
  await owner.goto(`/chat?with=${link}`);
  const pane = owner.getByTestId("chat-pane");
  const card = pane.getByRole("article", { name: "Product: Rice, 25kg bag" });
  // What is true now: today's price (and what it was), our own stock, Order.
  await expect(card).toContainText("₦43,000 a bag · min 5 (was ₦41,000)");
  await expect(card).toContainText("Your stock: 4 bags left");
  await expect(card.getByRole("link", { name: "Order Rice, 25kg bag" })).toHaveAttribute("href", `/storefront/suppliers/${link}/prices`);
  await axeBoth(owner, "a live card");

  // Our own service, from the picker's Services.
  const box = pane.getByLabel("Message");
  await box.fill("We can also do @mill");
  const picker = owner.getByRole("region", { name: "Tag something" });
  await expect(picker.getByRole("group", { name: "Services" })).toContainText("₦2,500 · 30 min");
  await box.press("Enter");
  await expect(box).toHaveValue("We can also do @Rice milling ");
});

test("the sound for new orders and payments can be switched off", async ({ browser }) => {
  const { owner } = await linked(browser, { width: 1440, height: 900 });
  await owner.goto("/chat");
  await owner.getByRole("button", { name: "More about the chats" }).click();
  const sw = owner.getByRole("button", { name: /Sound for new orders and payments/ });
  await expect(sw).toHaveAttribute("aria-pressed", "true");
  await sw.click();
  await owner.getByRole("button", { name: "More about the chats" }).click();
  await expect(owner.getByRole("button", { name: "Sound for new orders and payments: off" })).toHaveAttribute("aria-pressed", "false");
});
