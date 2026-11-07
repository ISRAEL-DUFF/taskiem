import { expect, test, type Browser, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Chat tools, phase 3 (docs/spec/chat-tools.md §5): in a supplier's
// conversation the buyer orders from their price list, asks for a quote,
// opens their orders and bills, pays through the supplier's checkout and
// orders again. The supplier's side and the money are answered here (each
// API has tests of its own, and the whole trade between two businesses runs
// in api/chat_trade_test.go): here, what each step sends, and what is shown.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@buy.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
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

/** The supplier's prices, the buyer's stock, orders and bills, answered. */
async function trade(page: Page, link: string, name: string) {
  const sent: { method: string; path: string; body: unknown }[] = [];
  const remember = async (route: Route) => {
    const r = route.request();
    sent.push({ method: r.method(), path: new URL(r.url()).pathname, body: r.postData() ? r.postDataJSON() : undefined });
  };
  const prices = [
    { item_id: "p1", name: "Rice, 50kg bag", unit: "bag", price_kobo: 7_800_000, min_quantity: 2, available: true },
    { item_id: "p2", name: "Rice, 25kg bag", unit: "bag", price_kobo: 4_100_000, min_quantity: 5, available: true },
    { item_id: "p3", name: "Beans, Oloyin", unit: "bag", price_kobo: 9_500_000, min_quantity: 1, available: false },
  ];
  const now = new Date().toISOString();
  const purchase = {
    id: "u1", reference: "PO-0042", supplier_id: "sup", supplier: name, link_id: link,
    lines: [{ item_id: "s1", item: "Rice, 50kg bag", unit: "bag", quantity: 4, cost_kobo: 7_600_000, received: 4 }],
    total_kobo: 30_400_000, status: "received", drafted_by: "me", approved_by: null, sent_at: now, confirmed_at: now,
    confirmed_how: "supplier", counter: null, counter_at: null, received_at: now, cancelled_at: null,
    received_kobo: 30_400_000, paid_kobo: 0, owed_kobo: 30_400_000, created_at: now, seller_status: "delivered",
  };
  const bill = {
    id: "b1", link, supplier: "sup", supplier_name: name, reference: "INV-0107",
    lines: [{ description: "Rice, 50kg bag", quantity: 4, unit_kobo: 7_600_000 }],
    amount_kobo: 30_400_000, paid_kobo: 0, due_kobo: 30_400_000, status: "open", allow_part_payments: false,
    disputed: false, declared_paid_kobo: 0, paying_kobo: 0, sent_kobo: 0, uncounted_kobo: 0,
    pay_link: "https://pay.example.test/inv/INV-0107", mirrored_at: now, created_at: now,
  };
  await page.route(`**/v1/link-chats/${link}/items`, (r) => r.fulfill(json({ items: prices, whose: "theirs" })));
  await page.route("**/v1/supplier-prices?*", (r) => r.fulfill(json({ suppliers: [{ link, business: "sup", name, items: prices, updated_at: now }] })));
  await page.route(`**/v1/item-links/${link}`, (r) => r.fulfill(json({ item_links: [] })));
  await page.route("**/v1/stock", (r) =>
    r.fulfill(json({ keeps_stock: true, catalogue: "products", items: [{ id: "s1", name: "Rice, 50kg bag", available: true, stock: 3, unit: "bag" }] })),
  );
  await page.route("**/v1/stock/suppliers", (r) =>
    r.fulfill(json({ suppliers: [{ id: "sup", name, phone: "0803 123 4567", reached: "link", link_id: link, owed_kobo: 0, open: 0, invited_at: null, created_at: now, retired_at: null }] })),
  );
  await page.route("**/v1/stock/items", async (r) => (await remember(r), r.fulfill(json({ item: { id: "s9" } }, 201))));
  await page.route(`**/v1/supplier-prices/${link}/order`, async (r) => (await remember(r), r.fulfill(json({ purchase: { ...purchase, id: "u2", status: "draft" } }, 201))));
  await page.route("**/v1/stock/purchases/u2/send", async (r) => (await remember(r), r.fulfill(json({ purchase: { ...purchase, id: "u2", status: "sent" } }))));
  await page.route("**/v1/stock/purchases", (r) => r.fulfill(json({ purchases: [purchase] })));
  await page.route("**/v1/bills?*", (r) => r.fulfill(json({ bills: [bill] })));
  await page.route("**/v1/bills/pay", (r) => r.fulfill(json({ workflow_id: null })));
  await page.route("**/v1/bills/b1/query", async (r) => (await remember(r), r.fulfill(json({ bill: { ...bill, disputed: true } }))));
  await page.route(`**/v1/link-chats/${link}/messages`, async (r) =>
    r.request().method() === "POST" ? (await remember(r), r.fulfill(json({ message: { id: "q1" } }, 201))) : r.fallback(),
  );
  return sent;
}

for (const [size, viewport] of [
  ["a phone", { width: 360, height: 780 }],
  ["a computer", { width: 1440, height: 900 }],
] as const) {
  test(`a buyer on ${size}: an order from their price list, a quote, a bill and a reorder`, async ({ browser }) => {
    test.setTimeout(180_000);
    const { buyer, link, name } = await linked(browser, viewport);
    const sent = await trade(buyer, link, name);
    await buyer.goto(`/chat?with=${link}`);
    const pane = buyer.getByTestId("chat-pane");
    const order = buyer.getByTestId("order-sheet");

    // Nothing scrolls sideways with Order in the head.
    await expect(pane.getByTestId("chat-order")).toBeVisible();
    expect(await buyer.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    if (size === "a phone") await buyer.screenshot({ path: "test-results/buyer-head.png" });
    // Order, from the conversation's head: their prices, steppers, the bag.
    await pane.getByTestId("chat-order").click();
    await expect(order.getByRole("heading", { name: `Order from ${name}` })).toBeVisible();
    await expect(order.getByTestId("order-row")).toHaveCount(3);
    await expect(order).toContainText("Not available now");
    await expect(order.getByRole("button", { name: "Review order" })).toBeDisabled();
    // The first step is their least; one more after that.
    await order.getByRole("button", { name: "One more Rice, 50kg bag" }).click();
    await order.getByRole("button", { name: "One more Rice, 50kg bag" }).click();
    await expect(order.getByTestId("order-bag")).toContainText("₦234,000");
    await order.getByRole("button", { name: "One more Rice, 25kg bag" }).click();
    await order.getByRole("button", { name: "One fewer Rice, 25kg bag" }).click();
    await expect(order.getByRole("group", { name: "How many Rice, 25kg bag" })).toContainText("0");
    await axeBoth(buyer, "ordering from a price list");
    if (size === "a phone") {
      await buyer.waitForTimeout(600);
      await buyer.screenshot({ path: "test-results/buyer-order.png" });
    }

    // Review: which of our items each is (the same name found), a note.
    await order.getByRole("button", { name: "Review order" }).click();
    await expect(order.getByRole("heading", { name: "Review your order" })).toBeVisible();
    await expect(order).toContainText("3 × Rice, 50kg bag");
    await expect(order.getByLabel("Rice, 50kg bag")).toHaveValue("s1");
    await order.getByLabel("A note for them (optional)").fill("Before Friday, please");
    await axeBoth(buyer, "reviewing an order");
    await order.getByRole("button", { name: "Place order" }).click();
    await expect(buyer.getByRole("status").filter({ hasText: `Order sent to ${name}` })).toBeVisible();
    await expect(order).toHaveCount(0, { timeout: 5000 });
    expect(sent.find((x) => x.path.endsWith("/order"))?.body).toEqual({
      lines: [{ seller_item_id: "p1", quantity: 3, item_id: "s1" }],
      note: "Before Friday, please",
    });
    expect(sent.some((x) => x.path === "/v1/stock/purchases/u2/send")).toBe(true);

    // Ask for a quote: the lines go to them as a message.
    await pane.getByTestId("chat-order").click();
    await order.getByRole("button", { name: "One more Rice, 25kg bag" }).click();
    await order.getByRole("button", { name: "Review order" }).click();
    await order.getByRole("button", { name: "Ask for a quote" }).click();
    await expect(order).toHaveCount(0, { timeout: 5000 });
    const quote = sent.find((x) => x.path === `/v1/link-chats/${link}/messages`)?.body as { text: string };
    expect(quote.text).toContain("5 × Rice, 25kg bag");

    // Bills: the invoice, paid through their checkout, or questioned.
    const tool = async (title: string) => {
      await pane.getByRole("button", { name: "Open tools" }).click();
      const panel = buyer.getByRole("region", { name: "Tools" });
      await expect(panel.getByRole("button", { name: /^Order From/ })).toContainText(`From ${name}`);
      await panel.getByRole("button", { name: new RegExp(`^${title}`) }).click();
    };
    const sheet = buyer.getByTestId("tool-sheet");
    await tool("Bills");
    await sheet.getByRole("button", { name: /INV-0107/ }).click();
    await expect(sheet.getByTestId("bill-hero")).toContainText("₦304,000");
    const pay = sheet.getByRole("link", { name: /Pay ₦304,000/ });
    await expect(pay).toHaveAttribute("href", "https://pay.example.test/inv/INV-0107");
    await expect(pay).toHaveAttribute("target", "_blank");
    await axeBoth(buyer, "a bill in chat");
    if (size === "a phone") {
      await buyer.waitForTimeout(600);
      await buyer.screenshot({ path: "test-results/buyer-bill.png" });
    }
    await sheet.getByRole("button", { name: "Question it" }).click();
    await sheet.getByLabel(/What is wrong with INV-0107/).fill("Two bags were torn");
    await sheet.getByRole("button", { name: "Send the question" }).click();
    await expect(sheet).toHaveCount(0, { timeout: 5000 });
    expect(sent.find((x) => x.path === "/v1/bills/b1/query")?.body).toEqual({ reason: "Two bags were torn" });

    // Your orders: one again, at today's prices.
    await tool("Your orders");
    await sheet.getByRole("button", { name: /PO-0042/ }).click();
    await expect(sheet).toContainText("4 × Rice, 50kg bag");
    await sheet.getByRole("button", { name: "Reorder" }).click();
    await expect(order.getByRole("group", { name: "How many Rice, 50kg bag" })).toContainText("4");
    await expect(order.getByTestId("order-bag")).toContainText("₦312,000");
  });
}

test("a bill's card opens the bill beside the chat, and an order's card the order", async ({ browser }) => {
  test.setTimeout(120_000);
  const { buyer, link, name } = await linked(browser, { width: 1440, height: 900 });
  await trade(buyer, link, name);
  const at = new Date().toISOString();
  await buyer.route(`**/v1/link-chats/${link}/messages**`, (r) =>
    r.request().method() === "GET"
      ? r.fulfill(
          json({
            messages: [
              { id: "c1", link, mine: false, kind: "card", card: { doc: "purchase", event: "accepted", reference: "PO-0042", id: "u1", amount_kobo: 30_400_000 }, created_at: at },
              { id: "c2", link, mine: false, kind: "card", card: { doc: "bill", event: "bill", reference: "INV-0107", id: "INV-0107", amount_kobo: 30_400_000 }, created_at: at },
            ],
          }),
        )
      : r.fallback(),
  );
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const sheet = buyer.getByTestId("tool-sheet");
  await pane.getByRole("button", { name: "Pay ₦304,000 INV-0107" }).click();
  await expect(sheet.getByTestId("bill-hero")).toContainText("₦304,000");
  await expect(sheet.getByRole("link", { name: /Pay ₦304,000/ })).toHaveAttribute("href", "https://pay.example.test/inv/INV-0107");
  await sheet.getByRole("button", { name: "Close panel" }).click();
  await expect(sheet).toHaveCount(0);
  await pane.getByRole("button", { name: "View order PO-0042" }).click();
  await expect(sheet).toContainText("4 × Rice, 50kg bag");
});
