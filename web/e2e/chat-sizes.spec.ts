import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Sizes and colours for the shop itself (docs/spec/chat-categories-and-
// variants.md, phase 3): a trainer set up in 11 sizes and 3 colours from
// its form; Products shows one row with its sizes summed; Sell picks size
// 42, then black (what has none left greyed out); a low card opens stock
// in on the grid with the low ones marked. serve.sh keeps no catalogue, so
// stock is answered here and what is sent is checked (the API is tested
// in Go, api/stock_variants_test.go).
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({
  status,
  contentType: "application/json",
  body: JSON.stringify(body),
});

async function owner(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Zuri Atelier ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@sizes.test`,
      name: "Zuri Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
  await page.request.put("/v1/settings/chat-tools", {
    headers: H,
    data: { chat_tools: true },
  });
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

const choices = [
  { name: "Size", values: ["41", "42", "43"] },
  { name: "Colour", values: ["Black", "White"] },
];
const size = (
  id: string,
  s: string,
  c: string,
  stock: number,
  low = false,
) => ({
  id,
  name: `Air Max 90 – ${s}, ${c}`,
  available: true,
  stock,
  unit: "pair",
  price_kobo: 4_500_000,
  reorder_level: 1,
  low,
  variant_of: "k1",
  variant: { Size: s, Colour: c },
});
const sized = [
  {
    id: "k1",
    name: "Air Max 90",
    available: true,
    stock: 0,
    unit: "pair",
    price_kobo: 4_500_000,
    choices,
  },
  size("v1", "41", "Black", 4),
  size("v2", "42", "Black", 1, true),
  size("v3", "42", "White", 0, true),
  size("v4", "43", "Black", 6),
  size("v5", "43", "White", 2),
  { ...size("v6", "41", "White", 3), available: false }, // no longer sold
];

type Sent = { method: string; path: string; body: unknown };
async function answer(page: Page, items: unknown[]) {
  const sent: Sent[] = [];
  const remember = (r: Route) => {
    const q = r.request();
    sent.push({
      method: q.method(),
      path: new URL(q.url()).pathname,
      body: q.postDataJSON(),
    });
  };
  await page.route("**/v1/stock", (r) =>
    r.fulfill(json({ keeps_stock: true, catalogue: "products", items })),
  );
  await page.route(
    "**/v1/stock/items/*/variants",
    (r) => (remember(r), r.fulfill(json({ item: items[0], variants: [] }))),
  );
  await page.route(
    "**/v1/stock/movements/grid",
    (r) => (remember(r), r.fulfill(json({ movements: [] }))),
  );
  await page.route(
    "**/v1/stock/sales",
    (r) => (remember(r), r.fulfill(json({ sale: { id: "s1" } }, 201))),
  );
  return sent;
}

async function tool(page: Page, word: string) {
  const box = page.getByTestId("taskiem-pane").getByLabel("Message");
  await box.fill(`/${word}`);
  await box.press("Enter");
  await page.keyboard.press("Enter");
}

for (const viewport of [
  { width: 390, height: 844 },
  { width: 360, height: 780 },
]) {
  test(`a trainer set up in sizes and colours from its form at ${viewport.width}`, async ({
    browser,
  }) => {
    const page = await (await browser.newContext({ viewport })).newPage();
    await owner(page);
    const sent = await answer(page, [
      {
        id: "k1",
        name: "Air Max 90",
        available: true,
        stock: 5,
        unit: "pair",
        price_kobo: 4_500_000,
      },
    ]);
    await page.goto("/chat?with=taskiem");
    await tool(page, "pro");
    await page
      .getByRole("dialog", { name: /Products/ })
      .getByRole("button", { name: /Air Max 90/ })
      .click();
    const form = page.getByRole("dialog", { name: /Air Max 90/ });
    await form
      .getByRole("button", { name: "Set up sizes and colours" })
      .click();

    // Ready-made shoe sizes; a second choice, its colours typed.
    await form
      .getByRole("group", { name: "Ready-made values for choice 1" })
      .getByRole("button", { name: "Shoe sizes (EU)" })
      .click();
    await form.getByRole("button", { name: "Add a choice" }).click();
    const second = form.getByRole("group", { name: "Choice 2" });
    await expect(second.getByRole("textbox", { name: /Its name/ })).toHaveValue(
      "Colour",
    );
    await second
      .getByRole("textbox", { name: /Its values/ })
      .fill("Black, White, Red");
    await expect(form.getByRole("status")).toHaveText(
      "33 sizes and colours on sale",
    );
    // The 5 it has on the shelf move into its sizes: what is not counted
    // into one is missing, what is counted beyond them is new stock.
    const share = form.getByTestId("sizes-share");
    await expect(share).toHaveText(
      "Air Max 90 has 5 on the shelf now: they move into its sizes and colours, 0 counted in so far. 5 not counted into one will be recorded as missing.",
    );
    // The same number for all, then one not sold and one with its own price.
    await form.getByRole("textbox", { name: /Same number for all/ }).fill("5");
    await form.getByRole("button", { name: "Fill" }).click();
    await expect(share).toContainText(
      "5 counted in so far. 160 more than that will be recorded as new stock.",
    );
    await form.getByRole("textbox", { name: "46, Red: how many" }).fill("");
    await form
      .getByRole("checkbox", { name: /Some cost more or less/ })
      .check();
    await form
      .getByRole("textbox", { name: "46, Black: its own price in naira" })
      .fill("48,000");
    await expect(form.getByRole("status")).toHaveText(
      "32 sizes and colours on sale",
    );
    await fits(page);
    await axeBoth(page, "setting up sizes and colours");
    await form.getByRole("button", { name: "Save changes" }).click();
    await expect.poll(() => sent.length).toBe(1);
    const body = sent[0]?.body as {
      choices: { name: string; values: string[] }[];
      cells: {
        variant: Record<string, string>;
        count: number;
        price_kobo?: number;
      }[];
    };
    expect(body.choices).toEqual([
      {
        name: "Size",
        values: [
          "36",
          "37",
          "38",
          "39",
          "40",
          "41",
          "42",
          "43",
          "44",
          "45",
          "46",
        ],
      },
      { name: "Colour", values: ["Black", "White", "Red"] },
    ]);
    expect(body.cells).toHaveLength(32);
    expect(body.cells.every((c) => c.count === 5)).toBe(true);
    expect(
      body.cells.find(
        (c) => c.variant.Size === "46" && c.variant.Colour === "Red",
      ),
    ).toBeUndefined();
    expect(
      body.cells.find(
        (c) => c.variant.Size === "46" && c.variant.Colour === "Black",
      )?.price_kobo,
    ).toBe(4_800_000);
  });
}

test("one row per product, Sell picks 42 then black, and the low card opens the grid", async ({
  browser,
}) => {
  const page = await (
    await browser.newContext({ viewport: { width: 390, height: 844 } })
  ).newPage();
  await owner(page);
  const sent = await answer(page, sized);
  await page.route("**/v1/chat-alerts", (r) =>
    r.fulfill(
      json({
        alerts: [
          {
            key: "low_stock:k1",
            kind: "low_stock",
            at: new Date().toISOString(),
            id: "k1",
            cells: ["v2", "v3"],
            title: "Air Max 90 is running low",
            line: "42, Black and 42, White are low",
          },
        ],
      }),
    ),
  );
  await page.goto("/chat?with=taskiem");

  // Products: the trainer once, its sizes summed; not each size.
  await tool(page, "pro");
  const products = page.getByRole("dialog", { name: /Products/ });
  await expect(products.getByRole("listitem")).toHaveCount(1);
  await expect(products.getByRole("listitem")).toContainText(
    "5 sizes and colours · 2 low",
  );
  await expect(products.getByRole("listitem")).toContainText("13 left");
  await page.keyboard.press("Escape");

  // Sell: 42, then black; white has none left.
  await tool(page, "sell");
  const sell = page.getByTestId("sell-sheet");
  await expect(sell.getByTestId("sell-row")).toHaveCount(1);
  await sell.getByRole("button", { name: /Choose/ }).click();
  await sell
    .getByRole("group", { name: "Size" })
    .getByRole("button", { name: "42" })
    .click();
  const colours = sell.getByRole("group", { name: "Colour" });
  await expect(colours.getByRole("button", { name: "White" })).toBeDisabled();
  await colours.getByRole("button", { name: "Black" }).click();
  await sell
    .getByRole("button", { name: "One more Air Max 90, 42, Black" })
    .first()
    .click();
  await expect(
    sell.getByRole("list", { name: "Air Max 90 chosen" }),
  ).toContainText("42, Black");
  await fits(page);
  await axeBoth(page, "Sell, picking a size and colour");
  await sell.getByRole("button", { name: /Next/ }).click();
  await sell.getByRole("button", { name: "Record the sale" }).click();
  await expect
    .poll(() => sent.find((x) => x.path === "/v1/stock/sales")?.body)
    .toMatchObject({
      lines: [{ item_id: "v2", quantity: 1 }],
    });
  await expect(sell).toHaveCount(0, { timeout: 10_000 });

  // The low card: Record stock in opens the grid, the low ones marked.
  const card = page
    .getByTestId("chat-alerts")
    .getByRole("article", { name: "Air Max 90 is running low" });
  await expect(card).toContainText("42, Black and 42, White are low");
  await card.getByRole("button", { name: /^Record stock in/ }).click();
  const sheet = page.getByTestId("tool-sheet");
  await expect(
    sheet.getByRole("textbox", { name: "42, White: how many came in (low)" }),
  ).toBeVisible();
  await expect(
    sheet.getByRole("textbox", { name: "41, Black: how many came in" }),
  ).toBeVisible();
  await expect(sheet.getByRole("textbox", { name: /41, White/ })).toHaveCount(
    0,
  ); // no longer sold
  await fits(page);
  await axeBoth(page, "stock in by its sizes and colours");
  await sheet
    .getByRole("textbox", { name: "42, White: how many came in (low)" })
    .fill("6");
  await sheet
    .getByRole("textbox", { name: "42, Black: how many came in (low)" })
    .fill("4");
  await sheet.getByRole("button", { name: "Record stock in" }).click();
  await expect
    .poll(() => sent.find((x) => x.path === "/v1/stock/movements/grid")?.body)
    .toEqual({
      reason: "received",
      lines: [
        { item_id: "v2", change: 4 },
        { item_id: "v3", change: 6 },
      ],
    });
});

test("a size's own price kept in the editor, and a price list heads the sizes with their product", async ({
  browser,
}) => {
  const page = await (
    await browser.newContext({ viewport: { width: 390, height: 844 } })
  ).newPage();
  await owner(page);
  const items = sized.map((it) =>
    it.id === "k1"
      ? { ...it, own_price: ["colour=white|size=43"] }
      : it.id === "v5"
        ? { ...it, price_kobo: 5_000_000 }
        : it,
  );
  const sent = await answer(page, items);
  await page.goto("/chat?with=taskiem");
  await tool(page, "pro");
  await page
    .getByRole("dialog", { name: /Products/ })
    .getByRole("button", { name: /Air Max 90/ })
    .click();
  const form = page.getByRole("dialog", { name: /Air Max 90/ });
  await form.getByRole("button", { name: "Change sizes and colours" }).click();
  // 43 White has its own price; 41 Black follows the product's, even had
  // its price been the same: the product records which are their own.
  await expect(
    form.getByRole("checkbox", { name: /Some cost more or less/ }),
  ).toBeChecked();
  await expect(
    form.getByRole("textbox", { name: "43, White: its own price in naira" }),
  ).toHaveValue("50000");
  await expect(
    form.getByRole("textbox", { name: "41, Black: its own price in naira" }),
  ).toHaveValue("");
  // Sizes already set up: nothing on the shelf of the product to share.
  await expect(form.getByTestId("sizes-share")).toHaveCount(0);
  await form.getByRole("button", { name: "Save changes" }).click();
  await expect.poll(() => sent.length).toBe(1);
  const cells = (
    sent[0]?.body as {
      cells: { variant: Record<string, string>; price_kobo?: number }[];
    }
  ).cells;
  expect(cells.filter((c) => c.price_kobo !== undefined)).toEqual([
    expect.objectContaining({
      variant: { Size: "43", Colour: "White" },
      price_kobo: 5_000_000,
    }),
  ]);

  // The price list: the product heads its sizes on sale and is not itself
  // one to tick; a size no longer sold is not offered.
  await page.route("**/v1/price-lists", (r) =>
    r.fulfill(
      json({
        lists: [
          {
            id: "l1",
            name: "Trade prices",
            items: [],
            links: [],
            updated_at: "2026-11-08T10:00:00Z",
          },
        ],
      }),
    ),
  );
  await page.goto("/storefront/price-lists");
  await page.getByRole("button", { name: "Items of Trade prices" }).click();
  const list = page.getByRole("dialog", { name: "Items of Trade prices" });
  await expect(list.getByTestId("price-sizes-head")).toHaveText(
    /Air Max 90\s*Sold in sizes and colours: tick each one you sell below\./,
  );
  await expect(
    list.getByRole("checkbox", { name: /^Air Max 90 \(pair\)$/ }),
  ).toHaveCount(0);
  await expect(
    list.getByRole("checkbox", { name: "Air Max 90 – 42, Black (pair)" }),
  ).toBeVisible();
  await expect(list.getByText("Air Max 90 – 41, White")).toHaveCount(0);
  await fits(page);
  await axeBoth(page, "a price list's items with sizes");
});
