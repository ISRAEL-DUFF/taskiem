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
      waiting: 1,
    },
  ],
  stuck: [
    {
      id: "m9",
      item_id: "2",
      item: "Rice",
      reason: "sale",
      status: "pending",
      before: null,
      after: null,
      change: null,
      by: "user:ada",
      error: "cut off",
      created_at: "2026-10-29T08:00:00Z",
      done_at: null,
    },
  ],
};

const movements = {
  movements: [
    {
      id: "m2",
      item_id: "1",
      item: "Indomie",
      reason: "adjustment",
      status: "done",
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
      item_id: "1",
      item: "Indomie",
      reason: "received",
      status: "done",
      before: 0,
      after: 50,
      change: 50,
      by: "user:ada",
      created_at: "2026-10-28T09:00:00Z",
      done_at: "2026-10-28T09:00:01Z",
    },
    {
      id: "m0",
      item_id: "2",
      item: "Rice",
      reason: "outside",
      status: "done",
      before: 0,
      after: 30,
      change: 30,
      note: "First count seen in the table",
      by: "table",
      created_at: "2026-10-28T08:00:00Z",
      done_at: "2026-10-28T08:00:00Z",
    },
  ],
};

test("the Stock page before stock is kept, and with items", async ({
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
    page.getByText("Taskiem makes a products table for your items"),
  ).toBeVisible();
  await axeBoth(page, "/storefront/stock, not kept");
  // No Xenibase here: refused as making a table is.
  await page.getByRole("button", { name: "Keep my stock" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "data tables are not available",
  );

  // With items (from fixtures).
  await page.route("**/v1/stock", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(stock),
    }),
  );
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
  await expect(page.getByTestId("stock-item")).toHaveCount(2);
  await expect(
    page.getByTestId("stock-item").filter({ hasText: "Rice" }),
  ).toContainText("1 change on its way");
  await expect(
    page
      .getByRole("status")
      .filter({ hasText: "not reached your products table" }),
  ).toBeVisible();
  await expect(page.getByTestId("movement")).toHaveCount(3);
  await expect(page.getByTestId("movement").first()).toContainText(
    "Indomie: adjusted -46",
  );
  await expect(page.getByTestId("movement").first()).toContainText(
    "water damage",
  );
  await expect(page.getByTestId("movement").last()).toContainText(
    "Changed in the table",
  );
  await axeBoth(page, "/storefront/stock, with items");

  // Add an item.
  await page.getByRole("button", { name: "Add items" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Name").fill("Garri");
  await dialog.getByLabel("How many you have").fill("2.5");
  await expect(dialog).toContainText("whole number");
  await dialog.getByLabel("How many you have").fill("20");
  await dialog.getByLabel("Unit").fill("bag");
  await axeBoth(page, "add an item");
  await dialog.getByRole("button", { name: "Cancel" }).click();

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
  expect(sent).toEqual({
    item_id: "1",
    reason: "adjustment",
    note: "spoilt",
    change: -1,
  });
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
