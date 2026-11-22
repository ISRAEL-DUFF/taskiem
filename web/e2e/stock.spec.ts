import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// A shop's stock (docs/stock.md): the Stock page before the catalogue
// keeps stock, and with items, low ones first, every change listed, and
// the dialogs to add an item and record stock. serve.sh runs no Xenibase,
// so Keep my stock is refused as tables are; the page with items is
// served from fixtures.
test.use({ bypassCSP: true }); // axe is injected into the page

const H = { "X-Taskiem-Request": "1" };

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: "Mama Put Stores",
      email: `ada-${Date.now()}@stock.test`,
      name: "Ada",
      password: "correct horse battery",
    },
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

const stock = {
  keeps_stock: true,
  catalogue: "products",
  low: 1,
  published: false,
  items: [
    {
      id: "1",
      name: "Indomie",
      available: false,
      stock: 4,
      unit: "carton",
      cost_kobo: 850000,
      reorder_level: 10,
      target_level: 50,
      supplier: "Dangote Depot",
      low: true,
      suggest: 46,
      stock_item_id: "0192e2e0-0000-7000-8000-0000000000a1",
      uses: 1,
    },
    {
      id: "2",
      name: "Rice",
      available: true,
      stock: 30,
      unit: "bag",
      price_kobo: 4500000,
      reorder_level: 5,
      low: false,
      stock_item_id: "0192e2e0-0000-7000-8000-0000000000a2",
      uses: 1,
    },
    {
      id: "0192e2e0-0000-7000-8000-0000000000a3",
      name: "Ankara fabric",
      material: true,
      available: false,
      stock: 20,
      unit: "yard",
      low: false,
    },
  ],
};

/** The same three as the Stock page lists them: stock items, each with
 * what it is sold as (GET /v1/stock/stock-items). */
const stockItem = (over: Record<string, unknown>) => ({
  category: "",
  cost_kobo: null,
  low_level: null,
  target_level: null,
  supplier_id: null,
  supplier: "",
  group_of: "",
  group_name: "",
  low: false,
  suggest: null,
  value_kobo: 0,
  retired: false,
  sold_as: [],
  ...over,
});
const soldAs = (id: string, name: string, uses = 1, price: number | null = null) => ({
  product_id: id,
  name,
  uses,
  price_kobo: price,
  variant: null,
  available: true,
});
const stockItems = [
  stockItem({
    id: "0192e2e0-0000-7000-8000-0000000000a3",
    name: "Ankara fabric",
    unit: "yard",
    count: 20,
    cost_kobo: 150000,
    value_kobo: 3000000,
  }),
  stockItem({
    id: "0192e2e0-0000-7000-8000-0000000000a1",
    name: "Indomie",
    unit: "carton",
    count: 4,
    cost_kobo: 850000,
    low_level: 10,
    target_level: 50,
    supplier: "Dangote Depot",
    low: true,
    suggest: 46,
    value_kobo: 3400000,
    sold_as: [soldAs("1", "Indomie")],
  }),
  stockItem({
    id: "0192e2e0-0000-7000-8000-0000000000a2",
    name: "Rice",
    unit: "bag",
    count: 30,
    low_level: 5,
    sold_as: [soldAs("2", "Rice", 1, 4500000)],
  }),
];

/** GET /v1/stock/stock-items, filtered as the server does, the totals
 * following the filter. */
function stockItemsFor(url: string, items = stockItems) {
  const show = new URL(url).searchParams.get("show") ?? "all";
  const list = items.filter(
    (x) =>
      show === "all" ||
      (show === "sold") === (x.sold_as as unknown[]).length > 0,
  );
  return {
    keeps_stock: true,
    value_kobo: list.reduce((a, x) => a + (x.value_kobo as number), 0),
    low: list.filter((x) => x.low).length,
    items: list,
  };
}

const movements = {
  movements: [
    {
      id: "m2",
      item_id: "0192e2e0-0000-7000-8000-0000000000a1",
      item: "Indomie",
      reason: "adjustment",
      before: 50,
      after: 4,
      change: -46,
      note: "water damage",
      by: "user:ada",
      created_at: "2026-10-29T09:00:00Z",
      done_at: "2026-10-29T09:00:01Z",
    },
    {
      id: "m1",
      item_id: "0192e2e0-0000-7000-8000-0000000000a1",
      item: "Indomie",
      reason: "received",
      before: 0,
      after: 50,
      change: 50,
      by: "user:ada",
      created_at: "2026-10-28T09:00:00Z",
      done_at: "2026-10-28T09:00:01Z",
    },
    {
      id: "m0",
      item_id: "0192e2e0-0000-7000-8000-0000000000a2",
      item: "Rice",
      reason: "received",
      before: 0,
      after: 30,
      change: 30,
      note: "First goods in",
      by: "user:ada",
      created_at: "2026-10-28T08:00:00Z",
      done_at: "2026-10-28T08:00:00Z",
    },
  ],
};

test("the Stock page before stock is kept, and with what it counts", async ({
  page,
}) => {
  await signUp(page);
  await page.goto("/storefront/stock");
  await expect(
    page.getByRole("heading", { name: "Stock", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Keep your stock here" }),
  ).toBeVisible();
  await expect(
    page.getByText("Taskiem makes a products table for what you sell"),
  ).toBeVisible();
  await axeBoth(page, "/storefront/stock, not kept");
  // No Xenibase here: refused as making a table is.
  await page.getByRole("button", { name: "Keep my stock" }).click();
  // The error is beside the button pressed. (Where that is out of view,
  // the app also says it in a toast, so only this one is looked for.)
  await expect(
    page
      .getByRole("region", { name: "Keep your stock here" })
      .getByRole("alert"),
  ).toContainText("data tables are not available");

  // With stock items (from fixtures).
  await page.route("**/v1/stock", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(stock),
    }),
  );
  const shows: string[] = [];
  await page.route("**/v1/stock/stock-items?**", (route) => {
    shows.push(new URL(route.request().url()).searchParams.get("show") ?? "");
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(stockItemsFor(route.request().url())),
    });
  });
  await page.route("**/v1/stock/movements?**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(movements),
    }),
  );
  await page.goto("/storefront/stock");
  const low = page.getByTestId("low-item");
  await expect(low).toHaveCount(1);
  await expect(low).toContainText("Indomie");
  await expect(low).toContainText("4 cartons");
  await expect(low).toContainText(
    "reorder at 10 · up to 50 · cost ₦8,500 · Dangote Depot · order 46 cartons",
  );
  const rows = page.getByTestId("stock-row");
  await expect(rows).toHaveCount(3);
  // A material is counted and Not sold; a product's stock item says what
  // it is sold as.
  const fabric = rows.filter({ hasText: "Ankara fabric" });
  await expect(fabric).toContainText("20 yards");
  await expect(fabric.getByTestId("sold-as")).toHaveText("Not sold");
  await expect(
    rows.filter({ hasText: "Rice" }).getByTestId("sold-as"),
  ).toHaveText("Sold as Rice");
  await expect(page.getByTestId("stock-worth")).toHaveText(
    "3 counted · worth ₦64,000 at cost · 1 low",
  );
  await expect(page.getByTestId("movement")).toHaveCount(3);
  await expect(page.getByTestId("movement").first()).toContainText(
    "Indomie: adjusted -46",
  );
  await expect(page.getByTestId("movement").first()).toContainText(
    "water damage",
  );
  await axeBoth(page, "/storefront/stock, with stock items");

  // The filter: Not sold, then Sold; the totals follow it, the low card stays.
  const filter = page.getByRole("group", { name: "Show" });
  await filter.getByRole("button", { name: "Not sold" }).click();
  await expect(filter.getByRole("button", { name: "Not sold" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText("Ankara fabric");
  await expect(page.getByTestId("stock-worth")).toHaveText(
    "1 not sold · worth ₦30,000 at cost",
  );
  await expect(low).toHaveCount(1);
  await filter.getByRole("button", { name: "Sold", exact: true }).click();
  await expect(rows).toHaveCount(2);
  await expect(rows.filter({ hasText: "Ankara fabric" })).toHaveCount(0);
  expect(shows).toEqual(expect.arrayContaining(["all", "not_sold", "sold"]));
  await filter.getByRole("button", { name: "All" }).click();
  await expect(rows).toHaveCount(3);

  // On a phone, nothing scrolls sideways, in either theme.
  for (const width of [390, 360]) {
    await page.setViewportSize({ width, height: 800 });
    await expect(rows).toHaveCount(3);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(width);
    await axeBoth(page, `/storefront/stock at ${width}px`);
  }
  await page.setViewportSize({ width: 1280, height: 720 });

  // Add something to count: Do you sell it? must be answered.
  const added: unknown[] = [];
  await page.route("**/v1/stock/items", async (route) => {
    expect(route.request().headers()["idempotency-key"]).toBeTruthy();
    added.push(route.request().postDataJSON());
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({ item: { id: "x", name: "x" } }),
    });
  });
  await page.getByRole("button", { name: "Add something to count" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("What is it").fill("Thread");
  await dialog.getByLabel("How many you have").fill("2.5");
  await expect(dialog).toContainText("whole number");
  await dialog.getByLabel("How many you have").fill("12");
  await dialog.getByLabel("Unit").fill("roll");
  await expect(dialog).toContainText("Say whether you sell it.");
  await expect(dialog.getByRole("button", { name: "Add it" })).toBeDisabled();
  // No: a material, with no selling price asked.
  await dialog.getByLabel("No, I don't").check();
  await expect(dialog.getByLabel("Selling price (₦)")).toHaveCount(0);
  await expect(dialog.getByTestId("sells-hint")).toContainText(
    "not added to your products",
  );
  await axeBoth(page, "add something to count");
  await dialog.getByRole("button", { name: "Add it" }).click();
  await expect(dialog).toBeHidden();
  expect(added[0]).toMatchObject({
    name: "Thread",
    quantity: 12,
    unit: "roll",
    material: true,
  });
  expect(added[0]).not.toHaveProperty("price_kobo");
  // Yes: its product too, with its price.
  await page.getByRole("button", { name: "Add something to count" }).click();
  await dialog.getByLabel("What is it").fill("Zobo, 50cl");
  await dialog.getByLabel("How many you have").fill("24");
  await dialog.getByLabel("Yes, I sell it").check();
  await dialog.getByLabel("Selling price (₦)").fill("500");
  await dialog.getByRole("button", { name: "Add it" }).click();
  await expect(dialog).toBeHidden();
  expect(added[1]).toMatchObject({
    name: "Zobo, 50cl",
    quantity: 24,
    price_kobo: 50000,
  });
  expect(added[1]).not.toHaveProperty("material");

  // Change: a material by its own id; its name is its own.
  const changed: { url: string; body: unknown }[] = [];
  await page.route("**/v1/stock/items/*", async (route) => {
    changed.push({
      url: new URL(route.request().url()).pathname,
      body: route.request().postDataJSON(),
    });
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ item: {} }),
    });
  });
  await page.getByRole("button", { name: "Change Ankara fabric" }).click();
  await expect(dialog.getByLabel("Selling price (₦)")).toHaveCount(0);
  await dialog.getByLabel("Reorder at").fill("10");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
  expect(changed[0]).toEqual({
    url: "/v1/stock/items/0192e2e0-0000-7000-8000-0000000000a3",
    body: { reorder_level: 10 },
  });
  // A product's stock item through its product, with its price.
  await page.getByRole("button", { name: "Change Rice" }).click();
  await expect(dialog.getByLabel("Selling price (₦)")).toHaveValue("45000");
  await dialog.getByLabel("Cost price (₦)").fill("40000");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
  expect(changed[1]).toEqual({
    url: "/v1/stock/items/2",
    body: { cost_kobo: 4000000 },
  });

  // Record stock: an adjustment needs a reason, and shows the count after.
  await page
    .getByRole("button", { name: "Record stock of Indomie" })
    .first()
    .click();
  await expect(dialog).toContainText("Now: 4 cartons.");
  await dialog.getByLabel("Some were spoilt, lost or found").check();
  await dialog.getByLabel("How many").fill("1");
  await expect(dialog).toContainText("Say why");
  await dialog.getByLabel("Why").fill("spoilt");
  await expect(dialog).toContainText("The count goes from 4 → 3 cartons.");
  await axeBoth(page, "record stock");
  let sent: unknown = null;
  await page.route("**/v1/stock/movements", async (route) => {
    sent = route.request().postDataJSON();
    expect(route.request().headers()["idempotency-key"]).toBeTruthy();
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ movement: movements.movements[0] }),
    });
  });
  await dialog.getByRole("button", { name: "Record" }).click();
  await expect(dialog).toBeHidden();
  // The stock item itself, by its id.
  expect(sent).toEqual({
    item_id: "0192e2e0-0000-7000-8000-0000000000a1",
    reason: "adjustment",
    note: "spoilt",
    change: -1,
  });
  // Its history, by the same id.
  const history = page.waitForRequest((r) =>
    r.url().includes("item=0192e2e0-0000-7000-8000-0000000000a2"),
  );
  await page.getByRole("button", { name: "History of Rice" }).click();
  await history;
  await expect(
    page.getByRole("heading", { name: "Rice: every change" }),
  ).toBeVisible();
});

const sales = {
  day: "2026-10-29",
  total_kobo: 12000000,
  by_how: { cash: 9000000, transfer: 3000000, pos: 0 },
  count: 2,
  waiting: 1,
  top: [{ item: "Rice", unit: "bag", quantity: 2, total_kobo: 9000000 }],
  unsold: [
    {
      entry_id: "8d0c4a8e-2f7b-4a11-9a53-3c1e7a0f9b10",
      amount_kobo: 450000,
      channel: "moniepointpos/shop",
      at: new Date().toISOString(),
    },
  ],
  sales: [
    {
      id: "s1",
      reference: "S-7QX4KD",
      lines: [
        {
          item_id: "2",
          item: "Rice",
          quantity: 2,
          unit: "bag",
          price_kobo: 4500000,
          line_kobo: 9000000,
        },
      ],
      amount_kobo: 9000000,
      paid_how: "cash",
      status: "recorded",
      by: "user:ada",
      sold_at: new Date().toISOString(),
    },
    {
      id: "s2",
      reference: "S-K2M9PA",
      lines: [
        {
          item_id: "1",
          item: "Indomie",
          quantity: 3,
          unit: "carton",
          price_kobo: 1000000,
          line_kobo: 3000000,
        },
      ],
      amount_kobo: 3000000,
      paid_how: "transfer",
      status: "waiting",
      by: "user:ada",
      sold_at: new Date().toISOString(),
      short: ["Indomie"],
    },
  ],
};

test("sales on the Stock page: today's total, Record a sale, Say what was sold", async ({
  page,
}) => {
  await signUp(page);
  const json = (body: unknown) => ({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
  await page.route("**/v1/stock", (route) => route.fulfill(json(stock)));
  await page.route("**/v1/stock/movements?**", (route) =>
    route.fulfill(json(movements)),
  );
  const sent: unknown[] = [];
  await page.route("**/v1/stock/sales", async (route) => {
    if (route.request().method() === "POST") {
      expect(route.request().headers()["idempotency-key"]).toBeTruthy();
      sent.push(route.request().postDataJSON());
      await route.fulfill({ ...json({ sale: sales.sales[0] }), status: 201 });
      return;
    }
    await route.fulfill(json(sales));
  });
  await page.goto("/storefront/stock");
  const hero = page.getByTestId("sales-hero");
  await expect(hero).toContainText("₦120,000");
  await expect(hero).toContainText("2 sales · Cash ₦90,000 · Transfer ₦30,000");
  await expect(hero).toContainText("1 waiting for money");
  await expect(page.getByTestId("sale")).toHaveCount(2);
  await expect(page.getByTestId("sale").last()).toContainText(
    "3 cartons Indomie",
  );
  await expect(page.getByTestId("sale").last()).toContainText(
    "waiting for the transfer · S-K2M9PA · more Indomie sold than counted",
  );
  await expect(page.getByTestId("unsold")).toContainText("₦4,500");
  await axeBoth(page, "/storefront/stock, with sales");

  // Record a sale: Indomie has no price, so the amount is needed.
  await page.getByRole("button", { name: "Record a sale" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("What was sold").selectOption("1");
  await dialog.getByLabel("How many").fill("2");
  await expect(dialog).toContainText(
    "Indomie has no price: give its price or the amount taken.",
  );
  await dialog.getByRole("button", { name: "Add another item" }).click();
  await dialog.getByLabel("What was sold").last().selectOption("2");
  await dialog.getByLabel("Transfer").check();
  await dialog.getByLabel("Amount taken (₦)").fill("65,000");
  await expect(dialog).toContainText("matched when the transfer comes in");
  await axeBoth(page, "record a sale");
  await dialog.getByRole("button", { name: "Record the sale" }).click();
  await expect(dialog).toBeHidden();
  expect(sent[0]).toEqual({
    lines: [
      { item_id: "1", quantity: 2 },
      { item_id: "2", quantity: 1 },
    ],
    paid_how: "transfer",
    note: "",
    amount_kobo: 6500000,
  });

  // Say what a terminal's sale was: paid on the terminal, for its amount.
  await page
    .getByRole("button", { name: "Say what was sold for ₦4,500" })
    .click();
  await expect(dialog).toContainText("What was sold for ₦4,500");
  await expect(dialog.getByLabel("Amount taken (₦)")).toHaveCount(0);
  await dialog.getByLabel("What was sold").selectOption("2");
  await dialog.getByLabel("Price for one (₦)").fill("4500");
  await dialog.getByRole("button", { name: "Record the sale" }).click();
  await expect(dialog).toBeHidden();
  expect(sent[1]).toEqual({
    lines: [{ item_id: "2", quantity: 1, price_kobo: 450000 }],
    paid_how: "pos",
    note: "",
    amount_kobo: 450000,
    entry_id: "8d0c4a8e-2f7b-4a11-9a53-3c1e7a0f9b10",
  });
});

const purchases = {
  purchases: [
    {
      id: "p2",
      reference: "P-7QX4KD",
      supplier_id: "s1",
      supplier: "Dangote Depot",
      lines: [
        {
          item_id: "1",
          item: "Indomie",
          unit: "carton",
          quantity: 46,
          cost_kobo: 850000,
          received: 0,
        },
      ],
      total_kobo: 39100000,
      status: "drafted",
      drafted_by: "user:ada",
      approved_by: null,
      sent_at: null,
      confirmed_at: null,
      confirmed_how: null,
      counter: null,
      counter_at: null,
      received_at: null,
      cancelled_at: null,
      received_kobo: 0,
      paid_kobo: 0,
      owed_kobo: 0,
      created_at: new Date().toISOString(),
    },
    {
      id: "p1",
      reference: "P-K2M9PA",
      supplier_id: "s1",
      supplier: "Dangote Depot",
      lines: [
        {
          item_id: "1",
          item: "Indomie",
          unit: "carton",
          quantity: 46,
          cost_kobo: 850000,
          received: 46,
        },
        {
          item_id: "2",
          item: "Rice",
          unit: "bag",
          quantity: 17,
          cost_kobo: 4000000,
          received: 10,
        },
      ],
      total_kobo: 107100000,
      status: "part_received",
      drafted_by: "user:ada",
      approved_by: "user:ada",
      sent_at: new Date().toISOString(),
      confirmed_at: new Date().toISOString(),
      confirmed_how: "supplier",
      counter: "only 10 bags of rice this week",
      counter_at: new Date().toISOString(),
      received_at: null,
      cancelled_at: null,
      received_kobo: 79100000,
      paid_kobo: 0,
      owed_kobo: 79100000,
      created_at: new Date().toISOString(),
    },
  ],
};

const suppliers = {
  suppliers: [
    {
      id: "s1",
      name: "Dangote Depot",
      phone: "+2348031234567",
      reached: "whatsapp",
      owed_kobo: 79100000,
      open: 2,
      invited_at: null,
      created_at: "2026-10-28T09:00:00Z",
      retired_at: null,
    },
  ],
  settings: { purchase_limit_kobo: null },
};

test("suppliers and orders to them", async ({ page }) => {
  await signUp(page);
  const json = (body: unknown, status = 200) => ({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
  const sent: { url: string; body: unknown }[] = [];
  await page.route("**/v1/stock", (route) => route.fulfill(json(stock)));
  await page.route("**/v1/stock/movements?**", (route) =>
    route.fulfill(json(movements)),
  );
  await page.route("**/v1/stock/sales", (route) =>
    route.fulfill(
      json({
        day: "2026-10-29",
        total_kobo: 0,
        by_how: { cash: 0, transfer: 0, pos: 0 },
        count: 0,
        waiting: 0,
        top: [],
        unsold: [],
        sales: [],
      }),
    ),
  );
  await page.route("**/v1/stock/purchases**", async (route) => {
    const r = route.request();
    if (r.method() === "POST") {
      sent.push({ url: new URL(r.url()).pathname, body: r.postDataJSON() });
      if (r.url().endsWith("/low"))
        return route.fulfill(json({ purchases: [], no_supplier: ["Garri"] }));
      return route.fulfill(json({ purchase: purchases.purchases[0] }));
    }
    await route.fulfill(json(purchases));
  });
  await page.route("**/v1/stock/suppliers**", async (route) => {
    const r = route.request();
    if (r.method() === "POST") {
      sent.push({ url: new URL(r.url()).pathname, body: r.postDataJSON() });
      return route.fulfill(json({ supplier: suppliers.suppliers[0] }, 201));
    }
    await route.fulfill(json(suppliers));
  });

  // The orders on the Stock page.
  await page.goto("/storefront/stock");
  const rows = page.getByTestId("purchase");
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText(
    "P-7QX4KD · Dangote Depot · ₦391,000",
  );
  await expect(rows.first()).toContainText("Draft");
  await expect(rows.last()).toContainText(
    "the supplier confirmed · 1 item still to come · owed ₦791,000",
  );
  await expect(rows.last()).toContainText(
    "Dangote Depot asked: “only 10 bags of rice this week”",
  );
  await axeBoth(page, "/storefront/stock, with orders");
  await page.getByRole("button", { name: "Order what is low" }).click();
  await expect(
    page.getByRole("status").filter({ hasText: "Garri has no supplier" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Approve and send P-7QX4KD" }).click();
  await expect
    .poll(() => sent.map((s) => s.url))
    .toContain("/v1/stock/purchases/p2/send");

  // Goods in: what is still to come, filled in.
  await page.getByRole("button", { name: "Goods came in on P-K2M9PA" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Rice: how many came")).toHaveValue("7");
  await expect(dialog.getByLabel("Indomie: how many came")).toHaveValue("0");
  await axeBoth(page, "goods in");
  await dialog.getByRole("button", { name: "Record" }).click();
  await expect(dialog).toBeHidden();
  expect(sent.at(-1)).toEqual({
    url: "/v1/stock/purchases/p1/receive",
    body: { lines: [{ item_id: "2", quantity: 7 }] },
  });

  // A payment: what is owed, by transfer.
  await page
    .getByRole("button", { name: "Record a payment for P-K2M9PA" })
    .click();
  await expect(dialog.getByLabel("Amount paid (₦)")).toHaveValue("791000");
  await dialog.getByLabel("Amount paid (₦)").fill("500,000");
  await dialog.getByRole("button", { name: "Record the payment" }).click();
  await expect(dialog).toBeHidden();
  expect(sent.at(-1)).toEqual({
    url: "/v1/stock/purchases/p1/pay",
    body: { amount_kobo: 50000000, paid_how: "transfer" },
  });

  // The Suppliers page.
  await page.goto("/storefront/suppliers");
  await expect(page.getByTestId("supplier")).toContainText("Owed ₦791,000");
  await expect(page.getByTestId("supplier")).toContainText(
    "+2348031234567 · Gets orders on WhatsApp · 2 open orders",
  );
  await axeBoth(page, "/storefront/suppliers");
  await page.getByRole("button", { name: "Add a supplier" }).click();
  await dialog.getByLabel("Name").fill("Mama Rice");
  await dialog.getByLabel("Mobile number").fill("12");
  await expect(dialog).toContainText("Enter a mobile number");
  await dialog.getByLabel("Mobile number").fill("0805 555 1234");
  await dialog.getByLabel("Rice").check();
  await axeBoth(page, "add a supplier");
  await dialog.getByRole("button", { name: "Add the supplier" }).click();
  await expect(dialog).toBeHidden();
  expect(sent.at(-1)).toEqual({
    url: "/v1/stock/suppliers",
    body: { name: "Mama Rice", phone: "0805 555 1234", items: ["2"] },
  });
});

test("count the shelf: a sheet, a CSV, the month's losses", async ({
  page,
}) => {
  await signUp(page);
  const json = (body: unknown, status = 200) => ({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
  await page.route("**/v1/stock", (route) => route.fulfill(json(stock)));
  await page.route("**/v1/stock/movements?**", (route) =>
    route.fulfill(json(movements)),
  );
  await page.route("**/v1/stock/shrinkage", (route) =>
    route.fulfill(
      json({
        from: "2026-10-01T00:00:00+01:00",
        to: "2026-10-30T00:00:00+01:00",
        received: 35,
        sold: 12,
        lost: 4,
        lost_kobo: 12000000,
        items: [
          {
            item_id: "2",
            item: "Rice",
            received: 20,
            sold: 2,
            lost: 3,
            lost_kobo: 12000000,
          },
          {
            item_id: "1",
            item: "Indomie",
            received: 15,
            sold: 10,
            lost: 1,
            lost_kobo: 0,
          },
        ],
      }),
    ),
  );
  let sent: unknown = null;
  await page.route("**/v1/stock/counts", async (route) => {
    if (route.request().method() === "POST") {
      sent = route.request().postDataJSON();
      return route.fulfill(json({ count: {} }, 201));
    }
    await route.fulfill(
      json({
        counts: [
          {
            id: "c1",
            reference: "C-7QX4KD",
            lines: [
              { item_id: "2", item: "Rice", expected: 18, counted: 15 },
              { item_id: "1", item: "Indomie", expected: 4, counted: 4 },
            ],
            full: true,
            reason: "rats",
            lost_kobo: 12000000,
            by: "user:ada",
            counted_at: new Date().toISOString(),
            changed: 1,
          },
        ],
      }),
    );
  });
  await page.route("**/v1/stock/settings", (route) =>
    route.fulfill(
      json({ settings: { purchase_limit_kobo: null, count_limit_kobo: null } }),
    ),
  );
  await page.goto("/storefront/stock");
  await expect(page.getByTestId("shrink")).toContainText(
    "This month: 35 in, 12 sold, 4 lost (₦120,000 at cost). Lost most: Rice (3, ₦120,000).",
  );
  await expect(page.getByTestId("count")).toContainText(
    "C-7QX4KD · 2 items, every item",
  );
  await expect(page.getByTestId("count")).toContainText("₦120,000 missing");
  await axeBoth(page, "/storefront/stock, with counts");

  await page.getByRole("button", { name: "Count the shelf" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("textbox", { name: /^Indomie/ }).fill("3");
  await expect(dialog).toContainText("4 → 3 cartons (1 fewer)");
  await dialog.getByLabel("Or read a CSV").setInputFiles({
    name: "count.csv",
    mimeType: "text/csv",
    buffer: Buffer.from("Item,Count\nRice,28\nGarri,4\n"),
  });
  await expect(dialog.getByRole("textbox", { name: /^Rice/ })).toHaveValue(
    "28",
  );
  await expect(dialog.getByRole("status")).toContainText(
    "No item is called Garri",
  );
  await dialog.getByLabel("Why they differ").fill("spoilt");
  await axeBoth(page, "count the shelf");
  await dialog.getByRole("button", { name: "Record the count" }).click();
  await expect(dialog).toBeHidden();
  expect(sent).toEqual({
    lines: [
      { item_id: "1", count: 3 },
      { item_id: "2", count: 28 },
    ],
    reason: "spoilt",
  });
});

test("Counted by: a pack of 6 counted by the bottles, shown on the Stock page", async ({
  page,
}) => {
  await signUp(page);
  const r = await page.request.put("/v1/settings/chat-tools", {
    headers: H,
    data: { chat_tools: true },
  });
  expect(r.status(), await r.text()).toBe(200);
  const json = (body: unknown) => ({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
  const bottlesId = "0192e2e0-0000-7000-8000-0000000000b1";
  const packId = "0192e2e0-0000-7000-8000-0000000000b2";
  // The pack counts its own stock item until it is counted by the bottles.
  let counted: { stock_item_id: string; uses: number } = {
    stock_item_id: packId,
    uses: 1,
  };
  const byBottles = () => counted.stock_item_id === bottlesId;
  const products = () => ({
    keeps_stock: true,
    catalogue: "products",
    items: [
      {
        id: "8",
        name: "Malt, pack of 6",
        available: true,
        stock: byBottles() ? 20 : 1,
        unit: byBottles() ? "bottle" : "",
        price_kobo: 210000,
        ...counted,
      },
      {
        id: bottlesId,
        name: "Malt bottle",
        material: !byBottles(),
        available: false,
        stock: 20,
        unit: "bottle",
      },
    ],
  });
  const items = () => [
    stockItem({
      id: bottlesId,
      name: "Malt bottle",
      unit: "bottle",
      count: 20,
      cost_kobo: 25000,
      value_kobo: 500000,
      sold_as: byBottles()
        ? [soldAs("8", "Malt, pack of 6", counted.uses, 210000)]
        : [],
    }),
    stockItem({
      id: packId,
      name: "Malt, pack of 6",
      unit: "",
      count: 1,
      sold_as: byBottles() ? [] : [soldAs("8", "Malt, pack of 6", 1, 210000)],
    }),
  ];
  await page.route("**/v1/stock", (route) => route.fulfill(json(products())));
  await page.route("**/v1/stock/stock-items?**", (route) =>
    route.fulfill(json(stockItemsFor(route.request().url(), items()))),
  );
  await page.route("**/v1/stock/movements?**", (route) =>
    route.fulfill(json({ movements: [] })),
  );
  let put: unknown = null;
  await page.route("**/v1/stock/products/8/counted-by", async (route) => {
    expect(route.request().method()).toBe("PUT");
    put = route.request().postDataJSON();
    counted = put as typeof counted;
    await route.fulfill(json({ item: products().items[0] }));
  });

  // The pack's form, from the Products tool in the chat.
  await page.goto("/chat?with=taskiem");
  const sheet = page.getByTestId("tool-sheet");
  const openPack = async () => {
    await page.getByRole("button", { name: "Open tools" }).click();
    await page
      .getByRole("region", { name: "Tools" })
      .getByRole("button", { name: /^Products/ })
      .first()
      .click();
    await sheet.getByRole("button", { name: /Malt, pack of 6/ }).click();
  };
  await openPack();
  await expect(page.getByTestId("counted-by")).toContainText(
    "Malt, pack of 6 · 1 now",
  );
  await page
    .getByRole("button", { name: "Change what it is counted by" })
    .click();
  const pick = sheet.getByTestId("counted-pick");
  await expect(pick.getByLabel(/^Malt bottle/)).toBeVisible();
  await sheet.getByLabel("Find it").fill("bottle");
  // The one it is counted by now stays in view.
  await expect(pick.getByLabel(/^Malt, pack of 6/)).toBeChecked();
  await pick.getByLabel(/^Malt bottle/).check();
  await sheet.getByRole("textbox", { name: "One sale uses" }).fill("6");
  await expect(sheet).toContainText("6 bottles of Malt bottle");
  await axeBoth(page, "counted by");
  await sheet.getByRole("button", { name: "Save changes" }).click();
  await expect(sheet).toHaveCount(0, { timeout: 5000 });
  expect(put).toEqual({ stock_item_id: bottlesId, uses: 6 });

  // The form says it now.
  await openPack();
  await expect(page.getByTestId("counted-by")).toContainText(
    "Malt bottle · one sale uses 6 bottles · 20 bottles now",
  );

  // And the Stock page: the bottles are sold as the pack, 6 a sale.
  await page.goto("/storefront/stock");
  const row = page.getByTestId("stock-row").filter({ hasText: "Malt bottle" });
  await expect(row.getByTestId("sold-as")).toHaveText(
    "Sold as Malt, pack of 6 (uses 6 bottles)",
  );
  // Its name and price are the pack's: not changed from here.
  await page.getByRole("button", { name: "Change Malt bottle" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveCount(0);
  await expect(dialog.getByLabel("Selling price (₦)")).toHaveCount(0);
});

test("Counted by is not offered for a product sold in sizes and colours", async ({
  page,
}) => {
  await signUp(page);
  await page.request.put("/v1/settings/chat-tools", {
    headers: H,
    data: { chat_tools: true },
  });
  await page.route("**/v1/stock", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        keeps_stock: true,
        catalogue: "products",
        items: [
          {
            id: "9",
            name: "Sneakers",
            available: true,
            stock: null,
            price_kobo: 2500000,
            choices: [{ name: "Size", values: ["42", "43"] }],
          },
        ],
      }),
    }),
  );
  await page.goto("/chat?with=taskiem");
  await page.getByRole("button", { name: "Open tools" }).click();
  await page
    .getByRole("region", { name: "Tools" })
    .getByRole("button", { name: /^Products/ })
    .first()
    .click();
  await page
    .getByTestId("tool-sheet")
    .getByRole("button", { name: /Sneakers/ })
    .click();
  await expect(page.getByTestId("counted-by")).toContainText(
    "Sold in sizes and colours: each is counted by its own",
  );
  await expect(
    page.getByRole("button", { name: "Change what it is counted by" }),
  ).toHaveCount(0);
});
