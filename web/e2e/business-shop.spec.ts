import { expect, test, type Browser, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// A business's shop (docs/spec/mobile-app-look.md, "The business shop";
// BusinessShop.tsx): a linked supplier's prices for the link as the
// person's shop draws a storefront, from the conversation with them. + goes
// to their minimum; an item shows their quantity prices; a product's sizes
// are typed in the Order sheet's grid; the bag goes to the Order sheet's own
// review and is placed as a purchase order; List opens the plain list on
// the same bag; Ask about this begins the question in the conversation's
// box; the hearts are kept in this browser by business and supplier. The
// supplier's side is answered here (the order itself is tested in
// api/business_shop_test.go and chat-buyer.spec.ts).
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
const SHOTS = process.env.SHOP_SHOTS;

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@shop.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

/** A buyer linked to a supplier, with chat tools on. */
async function linked(browser: Browser, viewport: { width: number; height: number }) {
  const stamp = letters();
  const supplier = await (await browser.newContext()).newPage();
  await signUp(supplier, `Bayo Rice ${stamp}`);
  const buyer = await (await browser.newContext({ viewport })).newPage();
  await signUp(buyer, `Rivers Foods ${stamp}`);
  await supplier.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`bayo rice ${stamp}`)}`, { headers: H })).json();
  await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } });
  const id = (await (await supplier.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  await supplier.request.post(`/v1/links/${id}/accept`, { headers: H });
  const conv = (await (await buyer.request.get("/v1/link-chats", { headers: H })).json()).conversations[0];
  await buyer.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });
  return { buyer, link: conv.link as string, name: conv.name as string, business: conv.business as string };
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

/** Pictures of a view, light and dark, for a person to look at. */
async function shots(page: Page, name: string) {
  if (!SHOTS) return;
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(400);
    await page.screenshot({ path: `${SHOTS}/web-${name}-${scheme}.png` });
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

const PHOTO = `<svg xmlns="http://www.w3.org/2000/svg" width="320" height="320"><rect width="320" height="320" fill="#d9c7a3"/><ellipse cx="160" cy="190" rx="110" ry="70" fill="#f4ecd8"/><ellipse cx="160" cy="170" rx="90" ry="50" fill="#fffaf0"/></svg>`;

/** The supplier's prices (a list for this link: quantity prices, a
 * minimum, sizes), the buyer's stock and suppliers, answered. */
async function trade(page: Page, link: string, name: string) {
  const sent: { method: string; path: string; body: unknown }[] = [];
  const remember = async (route: Route) => {
    const r = route.request();
    sent.push({ method: r.method(), path: new URL(r.url()).pathname, body: r.postData() ? r.postDataJSON() : undefined });
  };
  const prices = [
    {
      item_id: "rice",
      name: "Ofada rice, 50kg",
      unit: "bag",
      category: "Grains",
      description: "Stone-free local rice, bagged at the mill.",
      price_kobo: 4_500_000,
      min_quantity: 2,
      available: true,
      breaks: [
        { min_quantity: 10, price_kobo: 4_000_000 },
        { min_quantity: 50, price_kobo: 3_880_000 },
      ],
      photo: { "320": "rice.jpg" },
    },
    { item_id: "beans", name: "Oloyin beans", unit: "bag", category: "Grains", price_kobo: 3_000_000, min_quantity: 1, available: true },
    { item_id: "gari", name: "Ijebu gari", unit: "bag", category: "Flour", price_kobo: 2_000_000, min_quantity: 1, available: false, left: 0 },
    {
      item_id: "shoe",
      name: "Canvas sneaker",
      unit: "pair",
      category: "Shoes",
      price_kobo: 800_000,
      min_quantity: 6,
      available: true,
      breaks: [{ min_quantity: 50, price_kobo: 700_000 }],
      choices: [{ name: "Size", values: ["42", "43"] }],
      sizes: [
        { item_id: "s42", name: "Canvas sneaker – 42", variant: { Size: "42" }, available: true },
        { item_id: "s43", name: "Canvas sneaker – 43", variant: { Size: "43" }, available: true },
      ],
    },
  ];
  const now = new Date().toISOString();
  await page.route(`**/v1/link-chats/${link}/items`, (r) => r.fulfill(json({ items: prices, whose: "theirs" })));
  await page.route(`**/v1/link-chats/${link}/photo?*`, (r) => r.fulfill({ status: 200, contentType: "image/svg+xml", body: PHOTO }));
  await page.route("**/v1/supplier-prices?*", (r) => r.fulfill(json({ suppliers: [{ link, business: "sup", name: "Wholesale", items: prices, updated_at: now }] })));
  await page.route(`**/v1/item-links/${link}`, (r) => r.fulfill(json({ item_links: [] })));
  await page.route("**/v1/stock/stock-items", (r) => r.fulfill(json({ keeps_stock: true, value_kobo: 0, low: 0, items: [] })));
  await page.route("**/v1/money/labels**", (r) => (r.request().method() === "GET" ? r.fulfill(json({ labels: [], suggestions: [] })) : r.fallback()));
  await page.route("**/v1/things**", (r) => r.fulfill(json({ things: [] })));
  await page.route("**/v1/stock", (r) => r.fulfill(json({ keeps_stock: true, catalogue: "products", items: [] })));
  await page.route("**/v1/stock/suppliers", (r) =>
    r.fulfill(json({ suppliers: [{ id: "sup", name, phone: "", reached: "link", link_id: link, owed_kobo: 0, open: 0, invited_at: null, created_at: now, retired_at: null }] })),
  );
  await page.route("**/v1/stock/items", async (r) => (await remember(r), r.fulfill(json({ item: { id: "s9" } }, 201))));
  const purchase = { id: "u2", reference: "PO-0099", supplier_id: "sup", supplier: name, link_id: link, lines: [], total_kobo: 0, status: "draft", created_at: now };
  await page.route(`**/v1/supplier-prices/${link}/order`, async (r) => (await remember(r), r.fulfill(json({ purchase }, 201))));
  await page.route("**/v1/stock/purchases/u2/send", async (r) => (await remember(r), r.fulfill(json({ purchase: { ...purchase, status: "sent" } }))));
  await page.route("**/v1/stock/purchases", (r) => r.fulfill(json({ purchases: [] })));
  return sent;
}

for (const [size, viewport] of [
  ["a phone", { width: 390, height: 844 }],
  ["a computer", { width: 1440, height: 900 }],
] as const) {
  test(`their shop on ${size}: + to their minimum, quantity prices, sizes in the grid, the bag to the review, placed as a purchase order`, async ({ browser }) => {
    test.setTimeout(180_000);
    const { buyer, link, name, business } = await linked(browser, viewport);
    const sent = await trade(buyer, link, name);
    await buyer.goto(`/chat?with=${link}`);
    const pane = buyer.getByTestId("chat-pane");
    const shop = buyer.getByTestId("shop");
    const phone = size === "a phone";

    // Their shop, from the conversation's head.
    await expect(pane.getByRole("button", { name: "Their shop" })).toBeVisible();
    await pane.getByRole("button", { name: "Their shop" }).click();
    await expect(shop.getByRole("heading", { name })).toBeVisible();
    await expect(shop).toContainText("Your prices · updated today");
    await expect(shop.getByRole("button", { name: /^Ofada rice, 50kg/ }).first()).toBeVisible();
    await expect(shop.getByTestId("shop-item-gari")).toContainText("Sold out");
    for (const chip of ["All", "Saved", "Grains", "Flour", "Shoes"]) await expect(shop.getByRole("group", { name: "Show" }).getByRole("button", { name: chip })).toBeVisible();
    expect(await buyer.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

    // + from nothing: their minimum, 2 bags; the bar at their price.
    await shop.getByRole("button", { name: "Add Ofada rice, 50kg to the bag" }).click();
    await expect(shop.getByTestId("shop-bag-bar")).toContainText("2 items · ₦90,000");
    // The heart: kept by the business we act for and the supplier.
    await shop.getByRole("button", { name: "Save Oloyin beans" }).click();
    const me = await (await buyer.request.get("/v1/me", { headers: H })).json();
    const kept = await buyer.evaluate(() => JSON.parse(localStorage.getItem("taskiem-shop-saved") ?? "{}"));
    expect(kept[`business:${me.tenant_id}:${business}`]).toEqual(["beans"]);
    await axeBoth(buyer, "their shop");
    if (phone) await shots(buyer, "shop");

    // An item: its quantity prices, the reached one marked; 10 bags at the 10-bag price.
    await shop.getByTestId("shop-item-rice").getByRole("button", { name: /^Ofada rice, 50kg/ }).click();
    await expect(shop.getByRole("heading", { name: "Ofada rice, 50kg" })).toBeVisible();
    const rows = shop.locator(".shop-qty-rows li");
    await expect(rows).toHaveText([/^2 to 9 bags.*₦45,000 each$/, /^10 to 49 bags₦40,000 each$/, /^50 bags and more₦38,800 each$/]);
    await expect(shop).toContainText("At least 2 bags an order.");
    for (let i = 0; i < 7; i++) await shop.getByRole("button", { name: "One more Ofada rice, 50kg" }).click();
    await expect(shop.getByTestId("shop-add")).toHaveText("Add to bag · ₦320,000");
    await expect(rows.nth(1)).toContainText("Your price");
    await axeBoth(buyer, "an item of their shop, with quantity prices");
    if (phone) await shots(buyer, "item");
    await shop.getByTestId("shop-add").click();
    await expect(shop.getByTestId("shop-bag-button").first()).toHaveAccessibleName("Your bag, 10 items");

    // A product sold in sizes: how many of each, typed in the Order sheet's grid.
    await shop.getByRole("button", { name: "Back to the shop" }).click();
    await expect(shop.getByRole("heading", { name: "Recently viewed" })).toBeVisible();
    await shop.getByRole("button", { name: /^Choose a size of Canvas sneaker/ }).click();
    await expect(shop).toContainText("Its sizes count together: 50 pairs across sizes get the 50-pair price.");
    await expect(shop.getByTestId("shop-view-bag")).toHaveText(/^View bag/);
    await shop.getByLabel("42: how many").fill("3");
    await axeBoth(buyer, "a product's sizes in their shop");
    if (phone) await shots(buyer, "sizes");
    await shop.getByTestId("shop-view-bag").click();

    // The bag: at their quantity prices; the sneakers short of their minimum, said.
    await expect(shop.getByRole("heading", { name: "Your bag" })).toBeVisible();
    await expect(shop).toContainText("Canvas sneaker: at least 6 in all, its sizes together.");
    await expect(shop.getByTestId("shop-checkout")).toBeDisabled();
    for (let i = 0; i < 3; i++) await shop.getByRole("button", { name: "One more Canvas sneaker" }).click();
    await expect(shop.getByTestId("shop-checkout")).toBeEnabled();
    await expect(shop.getByTestId("shop-checkout")).toHaveText("Review order · ₦448,000");
    await expect(shop.getByRole("list", { name: "Your bag" })).toContainText("₦40,000 each · quantity price");
    await axeBoth(buyer, "the bag of their shop");
    if (phone) await shots(buyer, "bag");

    // Review order: the Order sheet's own review; Place order drafts and sends.
    await shop.getByTestId("shop-checkout").click();
    const order = buyer.getByTestId("order-sheet");
    await expect(order.getByRole("heading", { name: "Review your order" })).toBeVisible();
    await expect(order).toContainText("10 × Ofada rice, 50kg");
    await expect(order).toContainText("6 × Canvas sneaker – 42");
    await expect(order.getByRole("button", { name: "Make an offer" })).toBeVisible();
    await expect(order.getByRole("button", { name: "Ask for a quote" })).toBeVisible();
    if (phone) await shots(buyer, "review");
    await order.getByLabel("A note for them (optional)").fill("Before Friday");
    await order.getByRole("button", { name: "Place order" }).click();
    await expect(buyer.getByRole("status").filter({ hasText: `Order sent to ${name}` })).toBeVisible();
    await expect(order).toHaveCount(0, { timeout: 5000 });
    // Placed: the shop closes on the conversation, the bag empty.
    await expect(shop).toHaveCount(0);
    const body = sent.find((x) => x.path.endsWith("/order"))?.body as { lines: { seller_item_id: string; quantity: number }[]; note: string };
    expect(body.lines.map((l) => [l.seller_item_id, l.quantity])).toEqual([
      ["rice", 10],
      ["s42", 6],
    ]);
    expect(body.note).toBe("Before Friday");
    expect(sent.some((x) => x.path === "/v1/stock/purchases/u2/send")).toBe(true);
  });
}

test("their shop's List is the plain list on the same bag; Ask about this begins the question in the conversation", async ({ browser }) => {
  test.setTimeout(120_000);
  const { buyer, link, name } = await linked(browser, { width: 390, height: 844 });
  await trade(buyer, link, name);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const shop = buyer.getByTestId("shop");
  // From the tools panel's Their shop.
  await pane.getByRole("button", { name: "Open tools" }).click();
  await buyer.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Their shop/ }).first().click();
  await shop.getByRole("button", { name: "Add Oloyin beans to the bag" }).click();
  await shop.getByRole("button", { name: "List view: their prices as a list" }).click();
  const order = buyer.getByTestId("order-sheet");
  await expect(order.getByRole("heading", { name: `Order from ${name}` })).toBeVisible();
  await expect(order.getByRole("group", { name: "How many Oloyin beans" })).toContainText("1");
  await order.getByRole("button", { name: "One more Ofada rice, 50kg" }).click();
  await expect(order.getByTestId("order-bag")).toContainText("2 items in your order");
  await shots(buyer, "list");
  await axeBoth(buyer, "their shop's list");
  await order.getByRole("button", { name: "Close" }).click();
  await expect(order).toHaveCount(0);
  // What the list changed is the shop's bag.
  await expect(shop.getByTestId("shop-bag-bar")).toContainText("3 items · ₦120,000");
  // Ask about this: back in the conversation, the question in its box.
  await shop.getByTestId("shop-item-beans").getByRole("button", { name: /^Oloyin beans/ }).click();
  await shop.getByRole("button", { name: /^Ask about this/ }).click();
  await expect(shop).toHaveCount(0);
  await expect(pane.getByLabel("Message")).toHaveValue("About Oloyin beans, ₦30,000: is it still available?");
});
