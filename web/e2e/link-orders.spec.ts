import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Orders between linked businesses (business links L2,
// docs/spec/business-links.md §7): the seller's order from a linked buyer,
// with what it needs first and Accept, Change and Delivered; the buyer's
// purchase order with the seller's change, Accept the change and Not all
// came. serve.sh runs no Xenibase, so no business keeps stock: both sides
// are served from fixtures, at a phone's width, passing axe in both themes.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: "Dangote Depot", email: `d-${Date.now()}-${Math.random().toString(36).slice(2, 8)}@orders.test`, name: "Musa", password: "correct horse battery" },
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

const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

const order = {
  id: "o1",
  link: "l1",
  buyer: "b1",
  buyer_name: "Ada's Kitchen",
  reference: "P-7QX4KD",
  lines: [
    { item: "Rice", unit: "bag", quantity: 10, price_kobo: 4_000_000, item_id: "r1", matched: "name" },
    { item: "Indomie", unit: "carton", quantity: 5, price_kobo: 850_000 },
  ],
  total_kobo: 44_250_000,
  note: "Before Friday, please",
  status: "received",
  invoice_at: "delivered",
  auto_accept: false,
  placed_by_seller: false,
  created_at: "2026-10-31T09:00:00Z",
  updated_at: "2026-10-31T09:00:00Z",
};

test("an order from a linked business, answered by its seller", async ({ page }) => {
  await signUp(page);
  const sent: { url: string; body: unknown }[] = [];
  let current: Record<string, unknown> = order;
  const ownItems = { keeps_stock: true, catalogue: "products", items: [{ id: "r1", name: "Rice", available: true, stock: 40, unit: "bag", price_kobo: 4_500_000 }] };
  await page.route("**/v1/stock", (route) => route.fulfill(json(ownItems)));
  await page.route("**/v1/links", (route) =>
    route.fulfill(
      json({
        links: [{ id: "l1", business: "b1", name: "Ada's Kitchen", state: "active", role: "customer", asked_by_us: false, requested_at: "2026-10-01T00:00:00Z", expires_at: "2026-11-01T00:00:00Z" }],
        sent: [],
      }),
    ),
  );
  await page.route("**/v1/trade-orders**", async (route) => {
    const r = route.request();
    const path = new URL(r.url()).pathname;
    if (r.method() === "PUT") {
      sent.push({ url: path, body: r.postDataJSON() });
      return route.fulfill(json({ order: current }));
    }
    if (r.method() === "POST" && path === "/v1/trade-orders") {
      sent.push({ url: path, body: r.postDataJSON() });
      return route.fulfill(json({ order: { ...order, id: "o2", status: "proposed", placed_by_seller: true } }, 201));
    }
    if (r.method() === "POST") {
      sent.push({ url: path, body: r.postDataJSON() });
      if (path.endsWith("/accept")) current = { ...order, status: "accepted", accepted_at: "2026-10-31T10:00:00Z" };
      if (path.endsWith("/delivered"))
        current = { ...current, status: "delivered", lines: order.lines.map((l, i) => ({ ...l, delivered: i === 0 ? 8 : 5 })) };
      return route.fulfill(json({ order: current, short: [] }));
    }
    if (path === "/v1/trade-orders") return route.fulfill(json({ orders: [current] }));
    return route.fulfill(json({ order: current }));
  });

  // It waits on the Orders page, then on its own.
  await page.goto("/storefront/orders");
  await expect(page.getByTestId("trade-open")).toContainText("Ada's Kitchen");
  await page.goto("/trade-orders");
  await expect(page.getByTestId("trade-order")).toContainText("P-7QX4KD");
  await expect(page.getByTestId("trade-order")).toContainText("New");
  await axeBoth(page, "/trade-orders");

  // An order drafted for a linked business that buys from us.
  await page.getByRole("button", { name: "Draft an order for them" }).click();
  const draft = page.getByRole("dialog");
  await draft.getByLabel("Your item").selectOption({ label: "Rice" });
  await draft.getByLabel("How many").fill("3");
  await axeBoth(page, "drafting an order for a linked business");
  await draft.getByRole("button", { name: "Send for them to confirm" }).click();
  await expect.poll(() => sent.at(-1)?.url).toBe("/v1/trade-orders");
  expect(sent.at(-1)?.body).toEqual({ link: "l1", lines: [{ item_id: "r1", quantity: 3 }], note: "" });

  await page.getByTestId("trade-order").click();
  await expect(page.getByTestId("trade-hero")).toContainText("₦442,500");
  await expect(page.getByTestId("trade-ask")).toHaveText("Accept it, change it, or decline it.");
  await expect(page.getByText("Their note: “Before Friday, please”")).toBeVisible();
  // Their Rice is our Rice by name: said so, it is remembered.
  await expect(page.getByTestId("trade-items").getByLabel("Their Rice")).toHaveValue("r1");
  await axeBoth(page, "an order from a linked business");
  await page.getByRole("button", { name: "Yes, that is it for Rice" }).click();
  await expect.poll(() => sent.at(-1)?.url).toBe("/v1/trade-orders/o1/lines/0/item");
  expect(sent.at(-1)?.body).toEqual({ item_id: "r1" });

  // A change, in a dialog: what can be supplied, at the seller's prices.
  await page.getByRole("button", { name: "Change", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("How many").first()).toHaveValue("10");
  await axeBoth(page, "changing an order");
  await dialog.getByLabel("How many").first().fill("8");
  await dialog.getByLabel("Price each (₦)").first().fill("42,000");
  await dialog.getByRole("button", { name: "Send the change" }).click();
  await expect.poll(() => sent.at(-1)?.url).toBe("/v1/trade-orders/o1/change");
  expect(sent.at(-1)?.body).toEqual({
    lines: [
      { item: "Rice", unit: "bag", quantity: 8, price_kobo: 4_200_000 },
      { item: "Indomie", unit: "carton", quantity: 5, price_kobo: 850_000 },
    ],
    note: "",
  });

  // Accepted instead; then delivered, 8 of the rice.
  await page.getByRole("button", { name: "Accept", exact: true }).click();
  await expect(page.getByRole("button", { name: "Delivered" })).toBeVisible();
  await page.getByRole("button", { name: "Delivered" }).click();
  await dialog.getByLabel("Rice (ordered 10 bags)").fill("8");
  await axeBoth(page, "recording a delivery");
  await dialog.getByRole("button", { name: "Record the delivery" }).click();
  await expect.poll(() => sent.at(-1)?.url).toBe("/v1/trade-orders/o1/delivered");
  expect(sent.at(-1)?.body).toEqual({ delivered: [8, 5] });
  await expect(page.getByTestId("trade-hero")).toContainText("Delivered");
});

const purchase = {
  id: "p1",
  reference: "P-7QX4KD",
  supplier_id: "s1",
  supplier: "Dangote Depot",
  lines: [
    { item_id: "1", item: "Rice", unit: "bag", quantity: 10, cost_kobo: 4_000_000, received: 0 },
    { item_id: "2", item: "Indomie", unit: "carton", quantity: 5, cost_kobo: 850_000, received: 0 },
  ],
  total_kobo: 44_250_000,
  status: "sent",
  drafted_by: "u1",
  approved_by: "u1",
  sent_at: "2026-10-31T09:00:00Z",
  confirmed_at: null,
  confirmed_how: null,
  counter: "8 Rice at ₦42,000",
  counter_at: "2026-10-31T09:30:00Z",
  received_at: null,
  cancelled_at: null,
  received_kobo: 0,
  paid_kobo: 0,
  owed_kobo: 0,
  created_at: "2026-10-31T09:00:00Z",
  link_id: "l1",
  seller_status: "changed",
  change: [
    { item_id: "1", item: "Rice", unit: "bag", quantity: 8, cost_kobo: 4_200_000, received: 0 },
    { item_id: "2", item: "Indomie", unit: "carton", quantity: 5, cost_kobo: 850_000, received: 0 },
  ],
};

test("an order to a linked supplier, followed by its buyer", async ({ page }) => {
  await signUp(page);
  const sent: { url: string; body: unknown }[] = [];
  let current: Record<string, unknown> = purchase;
  await page.route("**/v1/stock/purchases/p1**", async (route) => {
    const r = route.request();
    const path = new URL(r.url()).pathname;
    if (r.method() === "POST") {
      sent.push({ url: path, body: r.postDataJSON() });
      if (path.endsWith("/accept-change"))
        current = { ...purchase, status: "confirmed", seller_status: "delivered", change: undefined, delivered_at: "2026-10-31T12:00:00Z",
          lines: purchase.change.map((l) => ({ ...l, delivered: l.quantity })) };
    }
    return route.fulfill(json({ purchase: current }));
  });

  await page.goto("/purchases/p1");
  await expect(page.getByTestId("purchase-hero")).toContainText("₦442,500");
  await expect(page.getByTestId("purchase-seller")).toHaveText("Dangote Depot asks to change it.");
  await expect(page.getByTestId("purchase-change")).toContainText("₦378,500");
  await axeBoth(page, "a purchase order with a change");
  await page.getByRole("button", { name: "Accept the change" }).click();
  await expect.poll(() => sent.at(-1)?.url).toBe("/v1/stock/purchases/p1/accept-change");

  // Delivered: Not all came, with our count.
  await expect(page.getByTestId("purchase-seller")).toHaveText("Dangote Depot delivered it.");
  await page.getByRole("button", { name: "Not all came" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Rice (delivered 8)").fill("7");
  await axeBoth(page, "not all came");
  await dialog.getByRole("button", { name: "Record what came" }).click();
  await expect.poll(() => sent.at(-1)?.url).toBe("/v1/stock/purchases/p1/not-all-came");
  expect(sent.at(-1)?.body).toEqual({ counts: [7, 5] });
});

test("an order a linked supplier drafted, confirmed by its buyer", async ({ page }) => {
  await signUp(page);
  const sent: { url: string; body: unknown }[] = [];
  const proposed = { ...purchase, status: "drafted", seller_status: "proposed", change: undefined, counter: null, counter_at: null, approved_by: null, sent_at: null,
    lines: [{ item_id: "", item: "rice", unit: "bag", quantity: 4, cost_kobo: 4_500_000, received: 0 }], total_kobo: 18_000_000 };
  await page.route("**/v1/stock", (route) =>
    route.fulfill(json({ keeps_stock: true, catalogue: "products", items: [{ id: "9", name: "Garri", available: true, stock: 2 }, { id: "1", name: "Rice", available: true, stock: 3 }] })),
  );
  await page.route("**/v1/stock/purchases/p1**", async (route) => {
    const r = route.request();
    if (r.method() === "POST") sent.push({ url: new URL(r.url()).pathname, body: r.postDataJSON() });
    return route.fulfill(json({ purchase: proposed }));
  });
  await page.goto("/purchases/p1");
  await expect(page.getByTestId("purchase-seller")).toContainText("Dangote Depot drafted this order for you.");
  const card = page.getByTestId("purchase-confirm");
  await expect(card.getByLabel("Their rice")).toHaveValue("1");
  await axeBoth(page, "a proposed order");
  await card.getByRole("button", { name: "Confirm the order" }).click();
  await expect.poll(() => sent.at(-1)?.url).toBe("/v1/stock/purchases/p1/confirm-proposed");
  expect(sent.at(-1)?.body).toEqual({ items: ["1"] });
});
