import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Chat tools, phase 1 (docs/spec/chat-tools.md): an owner turns the switch
// on in Settings; then "@Taskiem" in a business conversation goes to
// Taskiem only, and the sender is told where its answer is.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@tools.test`,
      name: `${tenant.split(" ")[0]} Owner`,
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function linked(browser: Browser) {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const buyer = await (
    await browser.newContext({ viewport: { width: 390, height: 844 } })
  ).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (
    await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`, { headers: H })
  ).json();
  await buyer.request.post("/v1/links", {
    headers: H,
    data: { business: found.results[0].business, role: "supplier" },
  });
  const link = (await (await seller.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  await seller.request.post(`/v1/links/${link}/accept`, { headers: H });
  return { seller, buyer, link };
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

test("the switch, and @Taskiem in a business conversation", async ({ browser }) => {
  const { seller, buyer, link } = await linked(browser);
  await buyer.goto("/settings");
  const box = buyer.getByLabel("Name a business to message it");
  await expect(box).not.toBeChecked();
  await box.check();
  await expect(box).toBeChecked();
  await axeBoth(buyer, "Settings with chat tools");
  expect((await (await buyer.request.get("/v1/settings/chat-tools", { headers: H })).json()).chat_tools).toBe(true);

  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByLabel("Message").fill("@Taskiem status");
  await pane.getByLabel("Message").press("Enter");
  const note = buyer.locator(".toast", { hasText: "Only Taskiem saw that" });
  await expect(note).toBeVisible();
  await expect(note.getByRole("link", { name: "Open" })).toHaveAttribute("href", "/chat?with=taskiem");
  await expect(pane.locator('[data-testid="chat-message"][data-mine="true"]')).toHaveCount(0);
  const theirs = (await (await seller.request.get(`/v1/link-chats/${link}/messages`, { headers: H })).json()).messages as { kind: string }[];
  expect(theirs.filter((m) => m.kind !== "card")).toEqual([]);
});

const json = (body: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
const stock = {
  keeps_stock: true,
  catalogue: "products",
  items: [
    { id: "s1", name: "Rice, 50kg bag", available: true, stock: 42, unit: "bag", price_kobo: 7_800_000 },
    { id: "s2", name: "Rice, 25kg bag", available: true, stock: 4, low: true, unit: "bag", price_kobo: 4_100_000 },
    { id: "s3", name: "Beans, Oloyin", available: false, stock: 0, unit: "bag", price_kobo: 9_500_000 },
  ],
};

for (const [size, viewport] of [
  ["a phone", { width: 390, height: 844 }],
  ["a computer", { width: 1440, height: 900 }],
] as const) {
  test(`the picker, the tools and a sheet on ${size}`, async ({ browser }) => {
    const { buyer, link } = await linked(browser);
    await buyer.setViewportSize(viewport);
    await buyer.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });
    await buyer.route(`**/v1/link-chats/${link}/items`, (r) =>
      r.fulfill(
        json({
          whose: "theirs",
          items: [
            { item_id: "i1", name: "Rice, 50kg bag", unit: "bag", price_kobo: 7_800_000, min_quantity: 1, available: true },
            { item_id: "i2", name: "Rice, 25kg bag", unit: "bag", price_kobo: 4_100_000, min_quantity: 5, available: true },
          ],
        }),
      ),
    );
    await buyer.route("**/v1/stock", (r) => r.fulfill(json(stock)));
    await buyer.goto(`/chat?with=${link}`);
    const pane = buyer.getByTestId("chat-pane");
    const box = pane.getByLabel("Message");

    // "@" tags things and businesses, in groups; Enter takes the first.
    await box.fill("Need 20 more @ri");
    const picker = buyer.getByRole("region", { name: "Tag something" });
    await expect(picker.getByRole("group", { name: "Products" }).getByRole("button")).toHaveCount(2);
    await axeBoth(buyer, "the picker");
    await box.press("Enter");
    await expect(box).toHaveValue("Need 20 more @Rice, 50kg bag ");
    // Taskiem is always offered last, but Enter never turns "@4500" into it.
    await box.fill("cement @4500");
    await expect(picker.getByRole("button", { name: /Taskiem/ })).toBeVisible();
    await expect(pane.getByTestId("emoji-button")).toHaveCount(0);
    await box.fill("@task");
    await box.press("Enter");
    await expect(box).toHaveValue("@Taskiem ");
    await box.fill("");

    // The tools in a business's conversation are that business's
    // (docs/spec/business-chat-tools.md): their shop and the dealings
    // between the two, none of the buyer's own shop.
    await pane.getByRole("button", { name: "Open tools" }).click();
    const tools = buyer.getByRole("region", { name: "Tools" });
    await expect(tools).toContainText(/With Dangote Depot/);
    await expect(tools.getByRole("button", { name: /^Their shop/ })).toBeVisible();
    await expect(tools.getByRole("button", { name: /^Your orders/ })).toBeVisible();
    for (const own of [/^Stock/, /^Products/, /^Sell/, /^Services/])
      await expect(tools.getByRole("button", { name: own })).toHaveCount(0);
    await axeBoth(buyer, "a business's tools");
    await pane.getByRole("button", { name: "Close tools" }).click();
    await expect(tools).toHaveCount(0);
    await box.fill("/5");
    await expect(tools).toContainText("No tool called “5”");
    await box.press("Enter");
    await expect(pane.locator('[data-testid="chat-message"][data-mine="true"]').last()).toContainText("/5");
    await box.fill("/pro");
    await expect(tools.getByRole("button")).toHaveCount(0);
    await box.fill("");

    // Her own tools are in her Taskiem conversation: the button, or "/"
    // and a word.
    await buyer.goto("/chat?with=taskiem");
    const home = buyer.getByTestId("taskiem-pane");
    await home.getByRole("button", { name: "Open tools" }).click();
    await expect(tools.getByRole("button", { name: /Stock/ })).toContainText("1 low");
    await axeBoth(buyer, "the tools");
    await home.getByRole("button", { name: "Close tools" }).click();
    await expect(tools).toHaveCount(0);
    const own = home.getByRole("textbox").first();
    await own.fill("/pro");
    await expect(tools.getByRole("button")).toHaveCount(1);
    await own.press("Enter");
    await buyer.keyboard.press("Enter");

    // The sheet: its list, its chips, its search; Esc closes it and only it.
    const sheet = buyer.getByRole("dialog", { name: /Products/ });
    await expect(sheet.getByRole("button", { name: /Rice, 25kg bag/ })).toContainText("4 left");
    await axeBoth(buyer, "a tool's sheet");
    if (viewport.width >= 1200) {
      // Beside the chat, not over it: the chat can still be used.
      await own.click();
      await expect(sheet).toBeVisible();
      await sheet.getByLabel("Search products").click();
    }
    await sheet.getByRole("button", { name: "Low stock · 1" }).click();
    await expect(sheet.getByRole("listitem")).toHaveCount(1);
    await sheet.getByRole("button", { name: "All" }).click();
    await sheet.getByLabel("Search products").fill("beans");
    await expect(sheet.getByRole("listitem")).toHaveCount(1);
    await buyer.keyboard.press("Escape");
    await expect(sheet).toHaveCount(0);
    await expect(home).toBeVisible();
    expect(await buyer.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewport.width);

    // Opened again from the panel's recent row, and closed with its button.
    await home.getByRole("button", { name: "Open tools" }).click();
    await tools.getByRole("button", { name: "Products", exact: true }).last().click();
    await expect(sheet).toBeVisible();
    await sheet.getByRole("button", { name: viewport.width >= 1200 ? "Close panel" : "Close" }).click();
    await expect(sheet).toHaveCount(0);
  });
}

test("a tool that cannot load says so, and tries again", async ({ browser }) => {
  const { buyer } = await linked(browser);
  await buyer.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });
  let fail = true;
  await buyer.route("**/v1/stock", (r) => (fail ? r.fulfill({ status: 500, body: "{}" }) : r.fulfill(json(stock))));
  // Her own products: a tool of her Taskiem conversation.
  await buyer.goto("/chat?with=taskiem");
  const pane = buyer.getByTestId("taskiem-pane");
  await pane.getByRole("button", { name: "Open tools" }).click();
  const tools = buyer.getByRole("region", { name: "Tools" });
  await expect(tools.getByRole("button", { name: /^Products/ })).toContainText("Could not load");
  await tools.getByRole("button", { name: /^Products/ }).click();
  const sheet = buyer.getByRole("dialog", { name: /Products/ });
  await expect(sheet).toContainText("Your products could not be loaded");
  await axeBoth(buyer, "a tool that could not load");
  fail = false;
  await sheet.getByRole("button", { name: "Try again" }).click();
  await expect(sheet.getByRole("listitem")).toHaveCount(3);
});

test("an order's card shows where the order is now, after it moves on", async ({ browser }) => {
  const { buyer: seller, link } = await linked(browser);
  await seller.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });
  const at = (s: number) => new Date(Date.now() - 60_000 + s * 1000).toISOString();
  // Their order arrived; we accepted it (our line), then sent it on its way.
  await seller.route(`**/v1/link-chats/${link}/messages**`, (r) =>
    r.request().method() === "GET"
      ? r.fulfill(
          json({
            messages: [
              { id: "m1", link, mine: false, kind: "card", card: { doc: "order", event: "ordered", reference: "P-7K8YED", amount_kobo: 26_000_000 }, created_at: at(0) },
              { id: "m2", link, mine: true, kind: "card", card: { doc: "order", event: "accept", reference: "P-7K8YED", amount_kobo: 26_000_000 }, created_at: at(10) },
              { id: "m3", link, mine: true, kind: "card", card: { doc: "order", event: "dispatched", reference: "P-7K8YED", amount_kobo: 26_000_000 }, created_at: at(20) },
            ],
          }),
        )
      : r.continue(),
  );
  await seller.goto(`/chat?with=${link}`);
  const card = seller.getByTestId("chat-pane").getByRole("article", { name: "Order · P-7K8YED" });
  // Still the card of their order, but no longer "New": on the way, with no buttons left.
  await expect(card).toContainText("sent order P-7K8YED");
  await expect(card).not.toContainText("New");
  await expect(card.getByRole("img", { name: "On the way: step 3 of 4" })).toBeVisible();
  await expect(card.getByRole("button", { name: /^Accept/ })).toHaveCount(0);
});

test("our own changes are diary lines; theirs are cards with their progress", async ({ browser }) => {
  const { buyer, link } = await linked(browser);
  await buyer.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });
  const at = new Date().toISOString();
  await buyer.route(`**/v1/link-chats/${link}/messages**`, (r) =>
    r.request().method() === "GET"
      ? r.fulfill(
          json({
            messages: [
              { id: "m1", link, mine: false, kind: "card", card: { doc: "order", event: "accept", reference: "AD-7Q4K2P", amount_kobo: 82_000_000 }, created_at: at },
              { id: "m2", link, mine: true, kind: "card", card: { doc: "bill", event: "declared", reference: "INV-0042", amount_kobo: 41_000_000 }, created_at: at },
              { id: "m3", link, mine: true, kind: "card", card: { doc: "order", event: "dispatched", reference: "AD-1", amount_kobo: 100_000 }, created_at: at },
            ],
          }),
        )
      : r.continue(),
  );
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const card = pane.getByRole("article", { name: "Order · AD-7Q4K2P" });
  await expect(card).toContainText("Accepted");
  await expect(card.getByRole("img", { name: "Accepted: step 2 of 4" })).toBeVisible();
  // Two changes made together are one line, which unfolds.
  const diary = pane.getByTestId("chat-diary");
  await expect(diary).toHaveCount(1);
  // The newest shows; the earlier one folds under it.
  await expect(diary).toContainText("AD-1");
  await expect(diary).toHaveAttribute("data-msg", "m3");
  // Lines already there when it opens do not land again.
  await expect(diary.locator(".tk-land")).toHaveCount(0);
  await diary.getByRole("button", { name: "and 1 earlier change" }).click();
  await expect(diary).toHaveCount(2);
  await expect(diary.first()).toContainText("You said it paid invoice INV-0042");
  await expect(pane.getByTestId("chat-card")).toHaveCount(1);
  await axeBoth(buyer, "a card and a diary line");
});
