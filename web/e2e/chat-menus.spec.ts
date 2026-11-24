import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Menus in the Taskiem chat (docs/spec/chat-menus.md, M1): help shows the
// topics as tiles; a tile turns the card into that topic's menu, five rows
// at most and "All N" for the rest; a row that asks starts its questions,
// and the card folds; a newer menu folds the older; something Taskiem did
// not understand shows the topics too. Axe in both themes, at 390 and
// 360 px, nothing wider than the screen.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Menu Kitchen ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@menu.test`,
      name: "Ada Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

const pane = (p: Page) => p.getByTestId("taskiem-pane");
const menus = (p: Page) => pane(p).getByTestId("taskiem-menu");
const send = async (p: Page, text: string) => {
  await pane(p).locator("#taskiem-text").fill(text);
  await pane(p).locator("#taskiem-text").press("Enter");
};

for (const width of [390, 360])
  test(`menus in the Taskiem chat, ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await signUp(page);
    await page.goto("/chat?with=taskiem");

    // help: the topics, as tiles.
    await send(page, "help");
    const topics = menus(page).last();
    await expect(topics).toHaveAttribute("data-topic", "");
    await expect(topics.getByRole("group", { name: "Topics" }).getByRole("button", { name: "Staff and tasks" })).toBeVisible();
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      const vs = await axe(page);
      expect(vs, describe(`topics (${scheme})`, vs)).toEqual([]);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

    // A tile: that topic's menu, in place.
    await topics.getByRole("button", { name: "Staff and tasks" }).click();
    await expect(topics).toHaveAttribute("data-topic", "staff");
    await expect(topics.getByRole("button", { name: /Give someone a task/ })).toBeVisible();
    // "All topics" turns it back.
    await topics.getByRole("button", { name: "All topics" }).click();
    await expect(topics).toHaveAttribute("data-topic", "");

    // A topic with more than five: five rows, and All N in a sheet.
    await topics.getByRole("button", { name: "Products" }).click();
    const rows = topics.locator(".tk-menu-row");
    const n = await rows.count();
    expect(n).toBeLessThanOrEqual(5);

    // help staff: a newer menu; the older folds.
    await send(page, "help staff");
    await expect(menus(page)).toHaveCount(2);
    await expect(menus(page).first()).toHaveAttribute("data-folded", "1");
    // Folded to what happened: no way back into a menu that is done.
    await expect(menus(page).first()).toContainText("Products · Not used");
    await expect(menus(page).first().getByRole("button")).toHaveCount(0);
    const staff = menus(page).last();
    await expect(staff).toHaveAttribute("data-topic", "staff");

    // A row that takes values: its form (M4); Save sends it, and the card
    // folds to its line.
    await staff.getByRole("button", { name: /Give someone a task/ }).click();
    const form = page.getByTestId("taskiem-action-form");
    await expect(form.getByText("Who is it for?", { exact: true })).toBeVisible();
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      const vs = await axe(page);
      expect(vs, describe(`a form (${scheme})`, vs)).toEqual([]);
    }
    await page.emulateMedia({ colorScheme: "light" });
    await form.getByLabel("Who is it for?").fill("Tunde");
    await form.getByLabel("What should they do, and by when?").fill("count the float by 7am");
    await form.getByRole("button", { name: "Give someone a task" }).click();
    await expect(form).toHaveCount(0);
    await expect(menus(page).last()).toHaveAttribute("data-folded", "1");
    await expect(menus(page).last()).toContainText("Staff and tasks · You chose Give someone a task");
    await expect(pane(page).getByText("Give someone a task: Tunde, count the float by 7am", { exact: true }).first()).toBeVisible();

    // Not understood: the short answer, and the topics.
    await send(page, "zzq blorp");
    await expect(pane(page).getByText("Sorry, I did not understand that.", { exact: true }).last()).toBeVisible();
    await expect(menus(page).last()).toHaveAttribute("data-topic", "");
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

    // Its ✕: closed, nothing done; kept so after a reload, as the others.
    await menus(page).last().getByRole("button", { name: "Close this menu" }).click();
    await expect(menus(page).last()).toContainText("Topics · Closed, nothing done");
    await page.reload();
    await expect(menus(page).last()).toContainText("Topics · Closed, nothing done");
    await expect(menus(page).first()).toContainText("Topics · Not used");
    await expect(menus(page).nth(1)).toContainText("Staff and tasks · You chose Give someone a task");
  });

// Review fixes: a menu whose catalogue could not be read says so and tries
// again; the row Taskiem picked with values it took opens its form with
// them in it, even for an action with a tool of its own.
test("a failed menu tries again; a picked row's values reach its form", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await signUp(page);
  let fail = true;
  await page.route("**/v1/chat/actions", (r) =>
    fail
      ? r.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "try again" }) })
      : r.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            topics: [
              {
                id: "sales", name: "Sales", icon: "sell", color: "lime",
                actions: [{
                  id: "sales.sell", title: "Record a sale", line: "Items, amount, and how they paid", icon: "sell", does: "changes", opens: "form", tool: "sell",
                  fields: [
                    { key: "what", label: "What did you sell?", kind: "text" },
                    { key: "amount", label: "For how much in all?", kind: "money" },
                  ],
                }],
              },
            ],
          }),
        }),
  );
  await page.route("**/v1/taskiem-chat", (r) =>
    r.request().method() !== "POST"
      ? r.continue()
      : r.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            messages: [{
              id: "22222222-0000-4000-8000-000000000001", channel: "web", inbound: false, kind: "menu", created_at: new Date().toISOString(),
              text: JSON.stringify({ topic: "sales", pick: "sales.sell", values: { what: "3 bags of rice" }, line: "About Sales." }),
            }],
          }),
        }),
  );
  await page.goto("/chat?with=taskiem");
  await send(page, "I sold 3 bags of rice");
  const card = menus(page).last();
  await expect(card.getByText("The menu could not be loaded.")).toBeVisible();
  fail = false;
  await card.getByRole("button", { name: "Try again" }).click();
  await card.getByRole("button", { name: /Record a sale/ }).click();
  const form = page.getByTestId("taskiem-action-form");
  await expect(form.getByLabel("What did you sell?")).toHaveValue("3 bags of rice");
});

// Menus everywhere, E3 (menus-everywhere.md §5): Today's "What would you
// like to do?": the topics used most as tiles, "All topics" for the rest; a
// tile's rows in a sheet; a row done through the Taskiem chat, its answer in
// a sheet, kept in the Taskiem chat.
for (const width of [390, 360])
  test(`Today's menu, ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await signUp(page);
    await page.goto("/home");
    const menu = page.getByTestId("today-menu");
    await expect(menu.getByRole("heading", { name: "What would you like to do?" })).toBeVisible();
    await expect(menu.getByRole("button", { name: "All topics" })).toBeVisible();
    await expect(menu.getByRole("button", { name: "Staff and tasks" })).toHaveCount(0);
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      const vs = await axe(page);
      expect(vs, describe(`Today's menu (${scheme})`, vs)).toEqual([]);
    }
    await page.emulateMedia({ colorScheme: "light" });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await menu.getByRole("button", { name: "All topics" }).click();
    await menu.getByRole("button", { name: "Staff and tasks" }).click();
    const sheet = page.getByTestId("today-topic");
    await expect(sheet).toBeVisible();
    await sheet.getByRole("button", { name: /^Your tasks/ }).click();
    const answer = page.getByTestId("action-answer");
    await expect(answer).toBeVisible();
    await expect(sheet).toHaveCount(0);
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      const vs = await axe(page);
      expect(vs, describe(`an answer on Today (${scheme})`, vs)).toEqual([]);
    }
    await page.emulateMedia({ colorScheme: "light" });
    // Kept in the Taskiem chat.
    await answer.getByRole("button", { name: "Taskiem chat" }).click();
    await expect(pane(page).getByText("Your tasks", { exact: true }).first()).toBeVisible();
  });

// Menus everywhere, E4 (menus-everywhere.md §6): search everywhere, from
// Today's head and the chats': one box; actions, chats, products, invoices,
// orders and customers in groups; an action done, its answer in a sheet.
for (const width of [390, 360])
  test(`search everywhere, ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await signUp(page);
    await page.goto("/home");
    await page.getByRole("link", { name: "Search everything" }).click();
    await expect(page).toHaveURL(/\/search$/);
    const box = page.getByRole("searchbox", { name: "Search everything" });
    await expect(box).toBeFocused();
    await box.fill("task");
    const actions = page.getByTestId("search-action");
    await expect(actions.getByRole("button", { name: /Give someone a task/ })).toBeVisible();
    if (width === 390) await page.screenshot({ path: "test-results/search-everywhere-phone.png" });
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      const vs = await axe(page);
      expect(vs, describe(`search everywhere (${scheme})`, vs)).toEqual([]);
    }
    await page.emulateMedia({ colorScheme: "light" });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    // A chip: only that kind.
    await page.getByRole("group", { name: "Kinds" }).getByRole("button", { name: "Invoices" }).click();
    await expect(actions).toHaveCount(0);
    await page.getByRole("group", { name: "Kinds" }).getByRole("button", { name: "All" }).click();
    // An action: done, its answer in a sheet.
    await actions.getByRole("button", { name: /Your tasks/ }).click();
    await expect(page.getByTestId("action-answer")).toBeVisible();
    // Nothing found, said so.
    await page.getByTestId("action-answer").getByRole("button", { name: "Done" }).click();
    await box.fill("zzqx");
    await expect(page.getByText("Nothing found for “zzqx”.")).toBeVisible();
  });

test("search everywhere from the chats' head", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signUp(page);
  await page.goto("/chat");
  await page.getByRole("link", { name: "Search everything" }).click();
  await expect(page.getByTestId("search-everywhere")).toBeVisible();
});

// Search everywhere finds saved things and services (menus-everywhere.md
// §6, g-search-saved): a saved thing by its name, the person's own and the
// business's, shown by its kind and whose it is and never what it holds;
// three in All, Show more for the rest; a service with its price and
// minutes. Axe in both themes, nothing wider than the screen.
for (const width of [390, 360])
  test(`search everywhere finds saved things and services, ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await signUp(page);
    const post = async (path: string, data: object) => {
      const r = await page.request.post(path, { headers: H, data });
      expect(r.status(), await r.text()).toBe(201);
    };
    await post("/v1/me/saved", { name: "mum", title: "Mum Adebayo", bank_name: "Guaranty Trust Bank", account_number: "2222222222" });
    await post("/v1/saved", { name: "mum-shop", kind: "place", title: "Mum's shop", details: { line: "4 Allen Avenue", area: "Ikeja" } });
    await post("/v1/saved", { name: "mum-store", kind: "text", title: "Store note", details: { body: "Back door code 4471" } });
    await post("/v1/saved", { name: "mums-pharmacy", kind: "place", title: "Pharmacy", details: { line: "9 Opebi Road", area: "Ikeja" } });
    // A service: the e2e server has no tables to hold services, so the
    // server's answer for them stands in (its search is tested in Go,
    // chat_refs_saved_test.go).
    await page.route(/\/v1\/chat-refs\?/, async (route) => {
      if (new URL(route.request().url()).searchParams.get("kind") !== "service") return route.continue();
      await route.fulfill({
        json: {
          refs: [{ kind: "service", id: "svc-1", name: "Mum's haircut", when: "0001-01-01T00:00:00Z", total_kobo: 300000, minutes: 30, status: "available" }],
          cursor: "",
        },
      });
    });
    await page.goto("/search");
    const box = page.getByRole("searchbox", { name: "Search everything" });
    await box.fill("mum");
    const saved = page.getByTestId("search-saved");
    await expect(saved.getByRole("link")).toHaveCount(3);
    // Newest first, the person's own and the business's merged by date: @mum, saved first, is behind Show more.
    await expect(saved.getByRole("link", { name: /@mums-pharmacy.*A place · The business's/ })).toBeVisible();
    await expect(saved.getByRole("button", { name: "Show more Saved" })).toBeVisible();
    const services = page.getByTestId("search-service");
    await expect(services.getByRole("link", { name: /Mum's haircut.*30 min/ })).toBeVisible();
    // Never what a saved thing holds, nor its title.
    for (const secret of ["Adebayo", "2222", "Allen", "4471", "Store note"]) await expect(page.getByText(secret)).toHaveCount(0);
    if (width === 390) await page.screenshot({ path: "test-results/search-saved-phone.png", fullPage: true });
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      const vs = await axe(page);
      expect(vs, describe(`search saved (${scheme})`, vs)).toEqual([]);
    }
    await page.emulateMedia({ colorScheme: "light" });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    // Show more: the Saved chip, every one.
    await saved.getByRole("button", { name: "Show more Saved" }).click();
    await expect(page.getByRole("group", { name: "Kinds" }).getByRole("button", { name: "Saved", pressed: true })).toBeVisible();
    await expect(saved.getByRole("link")).toHaveCount(4);
    await expect(page.getByTestId("search-service")).toHaveCount(0);
    // A saved thing of one's own opens where it is kept, beside the business's.
    await saved.getByRole("link", { name: /@mum\b.*Yours/ }).click();
    await expect(page).toHaveURL(/\/settings\/saved$/);
    await expect(page.getByTestId("my-saved").getByText("@mum", { exact: true }).first()).toBeVisible();
  });
