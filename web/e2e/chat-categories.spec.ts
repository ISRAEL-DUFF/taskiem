import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Product categories (docs/spec/chat-categories-and-variants.md, phase 1):
// "@" with a category's name offers it first, and a tap opens its items;
// the Products sheet has a chip per category; the product form sets one,
// offering those already used; a supplier's list is ordered from under its
// categories' headings. serve.sh runs no Xenibase, so the catalogue and the
// supplier's prices are answered here (the API is tested in Go).
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({
  status,
  contentType: "application/json",
  body: JSON.stringify(body),
});

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@cat.test`,
      name: `${tenant.split(" ")[0]} Owner`,
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
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

const fits = async (page: Page) =>
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);

// A shoe shop's catalogue: 14 sneakers (one called Nike only in its words),
// 3 sandals, and things with no category.
const items = [
  ...Array.from({ length: 14 }, (_, i) => ({
    id: `k${i + 1}`,
    name: `Trainer ${String(i + 1).padStart(2, "0")}`,
    available: true,
    stock: 5 + i,
    unit: "pair",
    price_kobo: 4_500_000,
    category: "Sneakers",
    ...(i === 0 ? { description: "Nike, white leather" } : {}),
  })),
  ...["Slides", "Flip flops", "Strappy"].map((name, i) => ({
    id: `d${i + 1}`,
    name,
    available: true,
    stock: 6,
    unit: "pair",
    price_kobo: 1_500_000,
    category: "Sandals",
  })),
  {
    id: "p1",
    name: "Shoe polish",
    available: true,
    stock: 20,
    price_kobo: 200_000,
  },
];

for (const viewport of [
  { width: 390, height: 844 },
  { width: 360, height: 780 },
]) {
  test(`all sneakers in two taps, chips, and a product's category at ${viewport.width}`, async ({
    browser,
  }) => {
    const page = await (await browser.newContext({ viewport })).newPage();
    await signUp(page, `Zuri Atelier ${letters()}`);
    await page.request.put("/v1/settings/chat-tools", {
      headers: H,
      data: { chat_tools: true },
    });
    await page.route("**/v1/stock", (r) =>
      r.fulfill(json({ keeps_stock: true, catalogue: "products", items })),
    );
    const changes: unknown[] = [];
    await page.route("**/v1/stock/items/*", async (r) => {
      if (r.request().method() !== "PATCH") return r.fallback();
      changes.push(r.request().postDataJSON());
      return r.fulfill(json({ item: { ...items[0], category: "Sandals" } }));
    });
    await page.goto("/chat?with=taskiem");
    const pane = page.getByTestId("taskiem-pane");
    const box = pane.getByLabel("Message");

    // One: "@sne" offers Sneakers first. Two: a tap opens them.
    await box.fill("@sne");
    const picker = page.getByRole("region", { name: "Tag something" });
    const category = picker
      .getByRole("group", { name: "Categories" })
      .getByRole("button");
    await expect(category).toHaveCount(1);
    await expect(category).toContainText("Sneakers");
    await expect(category).toContainText("14 items");
    await axeBoth(page, "the picker with a category");
    await category.click();
    const sheet = page.getByRole("dialog", { name: /Products/ });
    await expect(
      sheet.getByRole("button", { name: "Sneakers" }),
    ).toHaveAttribute("aria-pressed", "true");
    await expect(sheet.getByRole("listitem")).toHaveCount(14);
    // The words typed for it go: it was not a tag.
    await expect(box).toHaveValue("");
    await fits(page);
    await axeBoth(page, "Products, filtered to a category");

    // A chip per category of two or more, after the sheet's own.
    await expect(
      sheet.getByRole("group", { name: "Show" }).getByRole("button"),
    ).toHaveText([
      "All",
      "Low stock · 0",
      "Hidden · 0",
      "Sneakers · 14",
      "Sandals · 3",
    ]);
    await sheet.getByRole("button", { name: "Sandals · 3" }).click();
    await expect(sheet.getByRole("listitem")).toHaveCount(3);
    await sheet.getByRole("button", { name: "All" }).click();
    // Search reads a product's words after its name.
    await sheet.getByLabel("Search products").fill("nike");
    await expect(sheet.getByRole("listitem")).toHaveCount(1);
    await expect(sheet.getByRole("listitem")).toContainText("Trainer 01");

    // The product's form: its category, those already used offered.
    await sheet.getByRole("button", { name: /Trainer 01/ }).click();
    const form = page.getByRole("dialog", { name: /Trainer 01/ });
    const field = form.getByRole("textbox", { name: /^Category/ });
    await expect(field).toHaveValue("Sneakers");
    await field.fill("");
    const used = form.getByRole("group", { name: "Categories you use" });
    await expect(used.getByRole("button")).toHaveText([
      "Sneakers · 14",
      "Sandals · 3",
    ]);
    await fits(page);
    await axeBoth(page, "a product's category");
    await used.getByRole("button", { name: "Sandals · 3" }).click();
    await expect(field).toHaveValue("Sandals");
    await form.getByRole("button", { name: "Save" }).click();
    await expect.poll(() => changes).toEqual([{ category: "Sandals" }]);
  });
}

/** A buyer linked to a supplier, with chat tools on. */
async function linked(
  browser: Browser,
  viewport: { width: number; height: number },
) {
  const stamp = letters();
  const supplier = await (await browser.newContext()).newPage();
  await signUp(supplier, `Bata Wholesale ${stamp}`);
  const buyer = await (await browser.newContext({ viewport })).newPage();
  await signUp(buyer, `Zuri Atelier ${stamp}`);
  await supplier.request.put("/v1/link-settings", {
    headers: H,
    data: { findable: true },
  });
  const found = await (
    await buyer.request.get(
      `/v1/links/search?q=${encodeURIComponent(`bata wholesale ${stamp}`)}`,
      { headers: H },
    )
  ).json();
  await buyer.request.post("/v1/links", {
    headers: H,
    data: { business: found.results[0].business, role: "supplier" },
  });
  const id = (
    await (await supplier.request.get("/v1/links", { headers: H })).json()
  ).links[0].id as string;
  await supplier.request.post(`/v1/links/${id}/accept`, { headers: H });
  const conv = (
    await (await buyer.request.get("/v1/link-chats", { headers: H })).json()
  ).conversations[0];
  await buyer.request.put("/v1/settings/chat-tools", {
    headers: H,
    data: { chat_tools: true },
  });
  return { buyer, link: conv.link as string, name: conv.name as string };
}

test("a supplier's list by its categories, from @ in their conversation", async ({
  browser,
}) => {
  const viewport = { width: 390, height: 844 };
  const { buyer, link, name } = await linked(browser, viewport);
  const prices = [
    {
      item_id: "a",
      name: "Canvas low-top",
      unit: "pair",
      category: "Sneakers",
      price_kobo: 2_000_000,
      min_quantity: 6,
      available: true,
    },
    {
      item_id: "b",
      name: "Slides",
      unit: "pair",
      category: "Sandals",
      price_kobo: 800_000,
      min_quantity: 12,
      available: true,
    },
    {
      item_id: "c",
      name: "Runner",
      unit: "pair",
      category: "Sneakers",
      price_kobo: 2_500_000,
      min_quantity: 6,
      available: true,
    },
    {
      item_id: "d",
      name: "Laces",
      unit: "pack",
      price_kobo: 50_000,
      min_quantity: 10,
      available: true,
    },
  ];
  const now = new Date().toISOString();
  await buyer.route(`**/v1/link-chats/${link}/items`, (r) =>
    r.fulfill(json({ items: prices, whose: "theirs" })),
  );
  await buyer.route("**/v1/supplier-prices?*", (r) =>
    r.fulfill(
      json({
        suppliers: [
          {
            link,
            business: name,
            name: "Trade prices",
            items: prices,
            updated_at: now,
          },
        ],
      }),
    ),
  );
  await buyer.route(`**/v1/item-links/${link}`, (r) =>
    r.fulfill(json({ item_links: [] })),
  );
  await buyer.route("**/v1/stock", (r) =>
    r.fulfill(json({ keeps_stock: true, catalogue: "products", items: [] })),
  );
  await buyer.route("**/v1/stock/suppliers", (r) =>
    r.fulfill(json({ suppliers: [] })),
  );
  await buyer.route("**/v1/stock/purchases", (r) =>
    r.fulfill(json({ purchases: [] })),
  );
  await buyer.goto(`/chat?with=${link}`);
  const box = buyer.getByTestId("chat-pane").getByLabel("Message");

  await box.fill("@sand");
  const category = buyer
    .getByRole("region", { name: "Tag something" })
    .getByRole("group", { name: "Categories" })
    .getByRole("button");
  await expect(category).toContainText("1 item");
  await category.click();
  const order = buyer.getByTestId("order-sheet");
  await expect(order.getByRole("button", { name: "Sandals" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(order.getByTestId("order-row")).toHaveCount(1);
  await expect(order.getByTestId("order-row")).toContainText("Slides");
  await axeBoth(buyer, "their list, filtered to a category");

  // All: under their headings, in the list's order, Other last.
  await order.getByRole("button", { name: "All" }).click();
  await expect(order.getByRole("heading", { level: 3 })).toHaveText([
    "Sneakers",
    "Sandals",
    "Other",
  ]);
  await expect(
    order.getByRole("region", { name: "Sneakers" }).getByTestId("order-row"),
  ).toHaveCount(2);
  await fits(buyer);
  await axeBoth(buyer, "their list under its categories");
  // Search reads the category too.
  await order.getByLabel("Search their price list").fill("sneak");
  await expect(order.getByTestId("order-row")).toHaveCount(2);
});
