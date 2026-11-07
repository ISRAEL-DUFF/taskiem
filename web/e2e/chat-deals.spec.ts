import { expect, test, type Browser, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Chat tools, phase 7 (docs/spec/chat-tools.md §9): quantity prices on the
// buyer's Order sheet, an offer for the whole order, and the deal's cards
// (Accept, Counter, Decline, Keep this price for them) on both sides. What
// each step sends is checked here; the deal itself runs between two real
// businesses in api/trade_deals_test.go.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@deal.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

/** Two linked businesses; the one returned has chat tools on. */
async function linked(browser: Browser, viewport: { width: number; height: number }) {
  const stamp = letters();
  const other = await (await browser.newContext()).newPage();
  await signUp(other, `Bayo Rice ${stamp}`);
  const page = await (await browser.newContext({ viewport })).newPage();
  await signUp(page, `Rivers Foods ${stamp}`);
  await other.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (await page.request.get(`/v1/links/search?q=${encodeURIComponent(`bayo rice ${stamp}`)}`, { headers: H })).json();
  await page.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } });
  const id = (await (await other.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  await other.request.post(`/v1/links/${id}/accept`, { headers: H });
  const conv = (await (await page.request.get("/v1/link-chats", { headers: H })).json()).conversations[0];
  await page.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });
  return { page, link: conv.link as string, name: conv.name as string };
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

type Sent = { method: string; path: string; body: unknown };
const remembering = (sent: Sent[]) => async (route: Route) => {
  const r = route.request();
  sent.push({ method: r.method(), path: new URL(r.url()).pathname, body: r.postData() ? r.postDataJSON() : undefined });
};

async function openOrder(page: Page) {
  const pane = page.getByTestId("chat-pane");
  if ((page.viewportSize()?.width ?? 1440) >= 861) {
    await pane.getByTestId("chat-order").click();
    return;
  }
  await pane.getByRole("button", { name: "Open tools" }).click();
  await page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Order/ }).first().click();
}

for (const [size, viewport] of [
  ["a phone", { width: 360, height: 780 }],
  ["a computer", { width: 1440, height: 900 }],
] as const) {
  test(`a buyer on ${size}: quantity prices, and an offer for the whole order`, async ({ browser }) => {
    test.setTimeout(180_000);
    const { page, link, name } = await linked(browser, viewport);
    const sent: Sent[] = [];
    const remember = remembering(sent);
    const now = new Date().toISOString();
    const prices = [
      {
        item_id: "p2", name: "Rice, 25kg bag", unit: "bag", price_kobo: 4_100_000, min_quantity: 5, available: true,
        breaks: [{ min_quantity: 10, price_kobo: 3_950_000 }, { min_quantity: 50, price_kobo: 3_880_000 }],
      },
    ];
    const purchase = {
      id: "u2", reference: "PO-0043", supplier_id: "sup", supplier: name, link_id: link,
      lines: [{ item_id: "s1", item: "Rice, 25kg bag", unit: "bag", quantity: 10, cost_kobo: 3_500_000, received: 0 }],
      total_kobo: 35_000_000, status: "drafted", offer: true, created_at: now,
    };
    await page.route(`**/v1/link-chats/${link}/items`, (r) => r.fulfill(json({ items: prices, whose: "theirs" })));
    await page.route("**/v1/supplier-prices?*", (r) => r.fulfill(json({ suppliers: [{ link, business: "sup", name, items: prices, updated_at: now }] })));
    await page.route(`**/v1/item-links/${link}`, (r) => r.fulfill(json({ item_links: [] })));
    await page.route("**/v1/stock", (r) =>
      r.fulfill(json({ keeps_stock: true, catalogue: "products", items: [{ id: "s1", name: "Rice, 25kg bag", available: true, stock: 3, unit: "bag" }] })),
    );
    await page.route("**/v1/stock/suppliers", (r) =>
      r.fulfill(json({ suppliers: [{ id: "sup", name, phone: "0803 123 4567", reached: "link", link_id: link, owed_kobo: 0, open: 0, invited_at: null, created_at: now, retired_at: null }] })),
    );
    await page.route("**/v1/stock/purchases", (r) => r.fulfill(json({ purchases: [] })));
    await page.route(`**/v1/supplier-prices/${link}/order`, async (r) => (await remember(r), r.fulfill(json({ purchase }, 201))));
    await page.route("**/v1/stock/purchases/u2/send", async (r) => (await remember(r), r.fulfill(json({ purchase: { ...purchase, status: "sent" } }))));

    await page.goto(`/chat?with=${link}`);
    await openOrder(page);
    const order = page.getByTestId("order-sheet");
    // The quantity prices under the row; ten bags at the lower price.
    await expect(order.getByTestId("order-row")).toContainText("from 10 bags ₦39,500 · from 50 ₦38,800");
    const more = order.getByRole("button", { name: "One more Rice, 25kg bag" });
    for (let i = 0; i < 6; i++) await more.click(); // 5, then 6 … 10
    await expect(order.getByTestId("order-bag")).toContainText("₦395,000");

    // Review, then an offer: too high is refused, 350k sent.
    await order.getByRole("button", { name: "Review order" }).click();
    await expect(order).toContainText("at ₦39,500 for 10");
    await order.getByRole("button", { name: "Make an offer" }).click();
    const price = order.getByLabel("Your price for the whole order");
    await price.fill("400k");
    await expect(order.getByRole("alert")).toContainText("An offer is under their price, ₦395,000.");
    await expect(order.getByRole("button", { name: "Send offer" })).toBeDisabled();
    await price.fill("350k");
    await expect(order).toContainText("11% off their price");
    await expect(order.getByRole("button", { name: "Send offer" })).toBeEnabled();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await axeBoth(page, "an offer on the Order sheet");
    if (size === "a phone") {
      await page.waitForTimeout(700);
      await page.screenshot({ path: "test-results/deal-offer.png" });
    }
    await order.getByRole("button", { name: "Send offer" }).click();
    await expect(order).toContainText(`Offer sent to ${name}: ₦350,000`);
    const drafted = sent.find((x) => x.path.endsWith("/order"))?.body as { offer: boolean; total_kobo: number; lines: unknown[] };
    expect(drafted.offer).toBe(true);
    expect(drafted.total_kobo).toBe(35_000_000);
    expect(sent.some((x) => x.path === "/v1/stock/purchases/u2/send")).toBe(true);
  });
}

test("the deal's cards: the seller counters and accepts, keeping the price; the buyer counters back", async ({ browser }) => {
  test.setTimeout(180_000);
  const { page, link } = await linked(browser, { width: 390, height: 844 });
  const sent: Sent[] = [];
  const remember = remembering(sent);
  const at = new Date().toISOString();
  await page.route(`**/v1/link-chats/${link}/messages**`, (r) =>
    r.request().method() === "GET"
      ? r.fulfill(
          json({
            messages: [
              { id: "c1", link, mine: false, kind: "card", card: { doc: "order", event: "offered", reference: "P-7QX4KD", id: "o1", amount_kobo: 74_000_000, was_kobo: 79_000_000, note: "Regular buyer" }, created_at: at },
              { id: "c2", link, mine: false, kind: "card", card: { doc: "purchase", event: "change", reference: "PO-0044", id: "u4", amount_kobo: 77_000_000, was_kobo: 79_000_000 }, created_at: at },
            ],
          }),
        )
      : r.fallback(),
  );
  await page.route("**/v1/trade-orders/o1/change", async (r) => (await remember(r), r.fulfill(json({ order: {} }))));
  await page.route("**/v1/trade-orders/o1/accept", async (r) => (await remember(r), r.fulfill(json({ order: {} }))));
  await page.route("**/v1/stock/purchases/u4/counter", async (r) => (await remember(r), r.fulfill(json({ purchase: {} }))));
  await page.goto(`/chat?with=${link}`);
  const pane = page.getByTestId("chat-pane");
  const offer = pane.getByRole("article", { name: "Order · P-7QX4KD" });
  await expect(offer).toContainText("Offer");
  await expect(offer).toContainText("₦740,000 · was ₦790,000 · “Regular buyer”");
  await axeBoth(page, "a deal's cards");
  await page.waitForTimeout(700);
  await page.screenshot({ path: "test-results/deal-cards.png" });

  // The seller counters with a total: under their offer is refused.
  await offer.getByRole("button", { name: "Counter P-7QX4KD" }).click();
  const mine = offer.getByLabel("Your price for the whole order");
  await mine.fill("700k");
  await expect(offer.getByRole("alert")).toContainText("A counter is more than their ₦740,000.");
  await mine.fill("770k");
  await offer.getByLabel("A word with it (optional)").fill("Best I can do");
  await offer.getByRole("button", { name: "Send counter P-7QX4KD" }).click();
  await expect.poll(() => sent.find((x) => x.path.endsWith("/o1/change"))?.body).toEqual({ total_kobo: 77_000_000, note: "Best I can do" });

  // Accept, keeping the price for them 14 days.
  await offer.getByLabel("Keep this price for them").selectOption("14");
  await offer.getByRole("button", { name: "Accept ₦740,000 P-7QX4KD" }).click();
  await expect.poll(() => sent.find((x) => x.path.endsWith("/o1/accept"))?.body).toEqual({ keep_days: 14 });

  // The buyer's side: the seller's counter, countered back.
  const theirs = pane.getByRole("article", { name: "Order · PO-0044" });
  await expect(theirs).toContainText("Counter");
  await expect(theirs.getByLabel("Keep this price for them")).toHaveCount(0);
  await theirs.getByRole("button", { name: "Counter PO-0044" }).click();
  await theirs.getByLabel("Your counter").fill("760,000");
  await theirs.getByRole("button", { name: "Send counter PO-0044" }).click();
  await expect.poll(() => sent.find((x) => x.path.endsWith("/u4/counter"))?.body).toEqual({ total_kobo: 76_000_000 });
});

test("staff accepting under the lowest price: it waits for the owner", async ({ browser }) => {
  test.setTimeout(120_000);
  const { page, link } = await linked(browser, { width: 1440, height: 900 });
  const at = new Date().toISOString();
  await page.route(`**/v1/link-chats/${link}/messages**`, (r) =>
    r.request().method() === "GET"
      ? r.fulfill(json({ messages: [{ id: "c1", link, mine: false, kind: "card", card: { doc: "order", event: "countered", reference: "P-7QX4KD", id: "o1", amount_kobo: 76_000_000, was_kobo: 79_000_000 }, created_at: at }] }))
      : r.fallback(),
  );
  await page.route("**/v1/trade-orders/o1/accept", (r) => r.fulfill(json({ waiting: "owner", why: ["Rice at ₦38,000 is under its lowest price, ₦38,500"] }, 202)));
  await page.goto(`/chat?with=${link}`);
  const card = page.getByTestId("chat-pane").getByRole("article", { name: "Order · P-7QX4KD" });
  await card.getByRole("button", { name: "Accept ₦760,000 P-7QX4KD" }).click();
  await expect(page.getByText("Under your lowest price: P-7QX4KD waits for the owner's OK")).toBeVisible();
});

test("quantity prices on a list, and the largest discount staff can agree", async ({ browser }) => {
  test.setTimeout(120_000);
  const { page } = await linked(browser, { width: 1440, height: 900 });
  const sent: Sent[] = [];
  const remember = remembering(sent);
  const now = new Date().toISOString();
  const list = { id: "l1", name: "Trade", links: [], updated_at: now, items: [{ item_id: "s1", name: "Rice, 25kg bag", unit: "bag", price_kobo: 4_100_000, min_quantity: 5, available: true }] };
  let floors: { floors: Record<string, number>; max_discount_pct?: number } = { floors: { s1: 3_800_000 } };
  await page.route("**/v1/price-lists", (r) => r.fulfill(json({ lists: [list] })));
  await page.route("**/v1/price-lists/l1/items", async (r) => (await remember(r), r.fulfill(json({ list }))));
  await page.route("**/v1/stock", (r) =>
    r.fulfill(json({ keeps_stock: true, catalogue: "products", items: [{ id: "s1", name: "Rice, 25kg bag", available: true, stock: 40, unit: "bag", price_kobo: 4_300_000 }] })),
  );
  await page.route("**/v1/trade-floors", async (r) => {
    if (r.request().method() === "PUT") {
      await remember(r);
      floors = { ...floors, max_discount_pct: (r.request().postDataJSON() as { max_discount_pct: number }).max_discount_pct };
    }
    return r.fulfill(json(floors));
  });

  // Quantity prices: one that goes up is said, then saved.
  await page.goto("/storefront/price-lists");
  await expect(page.getByRole("region", { name: "Offers from buyers" })).toContainText("1 set");
  await page.getByRole("button", { name: "Items of Trade" }).click();
  const modal = page.getByRole("dialog");
  await modal.getByRole("button", { name: "+ Lower price for more of Rice, 25kg bag" }).click();
  const brk = modal.getByRole("group", { name: "Rice, 25kg bag: quantity price 1" });
  await brk.getByLabel("From").fill("10");
  await brk.getByLabel("Price (₦)").fill("42,000");
  await expect(modal.getByRole("alert")).toContainText("Each price is lower than the one before it.");
  await expect(modal.getByRole("button", { name: "Save" })).toBeDisabled();
  await brk.getByLabel("Price (₦)").fill("39,500");
  await axeBoth(page, "quantity prices");
  await modal.getByRole("button", { name: "Save" }).click();
  await expect.poll(() => (sent.find((x) => x.path.endsWith("/l1/items"))?.body as { items: { breaks: unknown }[] } | undefined)?.items[0]?.breaks).toEqual([
    { min_quantity: 10, price_kobo: 3_950_000 },
  ]);

  // The largest discount staff can agree.
  const offers = page.getByRole("region", { name: "Offers from buyers" });
  await offers.getByLabel("Largest discount staff can agree (%)").fill("95");
  await expect(offers.getByRole("alert")).toContainText("A whole number from 1 to 90.");
  await offers.getByLabel("Largest discount staff can agree (%)").fill("8");
  await offers.getByRole("button", { name: "Save" }).click();
  await expect.poll(() => sent.find((x) => x.path === "/v1/trade-floors")?.body).toEqual({ set_discount: true, max_discount_pct: 8 });
  await axeBoth(page, "offers from buyers");
});

test("the lowest price on a product, from chat: set, warned when over the trade price, never for staff", async ({ browser }) => {
  test.setTimeout(120_000);
  const { page, link } = await linked(browser, { width: 390, height: 844 });
  const sent: Sent[] = [];
  const remember = remembering(sent);
  const items = [{ id: "s1", name: "Rice, 50kg bag", available: true, stock: 42, unit: "bag", price_kobo: 7_600_000 }];
  await page.route("**/v1/stock", (r) => r.fulfill(json({ keeps_stock: true, catalogue: "products", items })));
  await page.route("**/v1/price-lists", (r) =>
    r.fulfill(json({ lists: [{ id: "pl1", name: "Trade", updated_at: "2026-11-07T08:00:00Z", links: [link],
      items: [{ item_id: "s1", name: "Rice, 50kg bag", unit: "bag", price_kobo: 7_200_000, min_quantity: 2, available: true }] }] })),
  );
  await page.route("**/v1/trade-floors", async (r) => {
    if (r.request().method() === "PUT") await remember(r);
    return r.fulfill(json({ floors: { s1: 6_900_000 } }));
  });
  await page.goto(`/chat?with=${link}`);
  const sheet = page.getByTestId("tool-sheet");
  await page.getByTestId("chat-pane").getByRole("button", { name: "Open tools" }).click();
  await page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Products/ }).click();
  await sheet.getByRole("button", { name: /Rice, 50kg bag/ }).click();
  const floor = sheet.getByLabel("Lowest price in naira");
  await expect(floor).toHaveValue("69,000");
  await expect(sheet).toContainText("Under it, an offer waits for your OK. Buyers never see it.");
  await floor.fill("73,000");
  await expect(sheet).toContainText("Higher than the trade price: every offer will wait for you.");
  await floor.fill("70,000");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await axeBoth(page, "the lowest price on a product");
  await sheet.getByRole("button", { name: "Save changes" }).click();
  await expect.poll(() => sent.find((x) => x.path === "/v1/trade-floors")?.body).toEqual({ item_id: "s1", floor_kobo: 7_000_000 });
});

test("a deal card tells the back and forth and when it lapses; a lapsed one has nothing to answer", async ({ browser }) => {
  test.setTimeout(120_000);
  const { page, link } = await linked(browser, { width: 390, height: 844 });
  const at = (h: number) => new Date(Date.now() + h * 3_600_000).toISOString();
  const deal = (until: string) => ({
    steps: [
      { by: "buyer", total_kobo: 74_000_000, at: at(-5) },
      { by: "seller", who: "Tunde", total_kobo: 77_000_000, at: at(-3) },
      { by: "buyer", total_kobo: 76_000_000, at: at(-1) },
    ],
    until,
  });
  await page.route(`**/v1/link-chats/${link}/messages**`, (r) =>
    r.request().method() === "GET"
      ? r.fulfill(json({ messages: [
          { id: "c1", link, mine: false, kind: "card", card: { doc: "order", event: "countered", reference: "P-OLD111", id: "o0", amount_kobo: 50_000_000, was_kobo: 55_000_000, deal: { steps: [{ by: "buyer", total_kobo: 50_000_000, at: at(-60) }], until: at(-12) } }, created_at: at(-60) },
          { id: "c2", link, mine: false, kind: "card", card: { doc: "order", event: "countered", reference: "P-7QX4KD", id: "o1", amount_kobo: 76_000_000, was_kobo: 79_000_000, deal: deal(at(46.5)) }, created_at: at(-1) },
        ] }))
      : r.fallback(),
  );
  await page.goto(`/chat?with=${link}`);
  const pane = page.getByTestId("chat-pane");
  const card = pane.getByRole("article", { name: "Order · P-7QX4KD" });
  const history = card.getByRole("list", { name: "How P-7QX4KD was agreed so far" });
  await expect(history.getByRole("listitem")).toHaveText(["They offered ₦740,000", "Tunde countered ₦770,000", "They countered ₦760,000"]);
  await expect(card).toContainText("Lapses in 46 hours");
  await expect(card.getByRole("button", { name: "Accept ₦760,000 P-7QX4KD" })).toBeVisible();
  const old = pane.getByRole("article", { name: "Order · P-OLD111" });
  await expect(old).toContainText("This offer lapsed: nothing to answer");
  await expect(old.getByTestId("deal-actions")).toHaveCount(0);
  await expect(old.getByText("Lapsed", { exact: true })).toBeVisible();
  await expect(old.getByRole("img", { name: /step/ })).toHaveCount(0);
  await axeBoth(page, "a deal card's history");
  for (const theme of ["light", "dark"]) {
    await page.evaluate((t) => (t === "dark" ? (document.documentElement.dataset.theme = "dark") : delete document.documentElement.dataset.theme), theme);
    await page.waitForTimeout(700);
    await card.screenshot({ path: `test-results/deal-history-${theme}.png` });
  }
});
