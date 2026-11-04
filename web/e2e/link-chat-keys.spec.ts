import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Keyboard and desktop polish (Chat, phase 7): shortcuts that stay off while
// typing, arrows through the list, Esc closing the panel and then the chat,
// right click on a bubble, focus going back to the message box, and words
// for the unread count and the ticks.
test.use({ bypassCSP: true, viewport: { width: 1280, height: 800 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@chat.test`,
      name: `${tenant.split(" ")[0]} Owner`,
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

type Ctx = Parameters<Browser["newContext"]>[0];
async function linked(browser: Browser, buyerOptions: Ctx = {}) {
  const stamp = letters();
  const seller = await (
    await browser.newContext({ viewport: { width: 1280, height: 800 } })
  ).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const buyer = await (
    await browser.newContext({
      viewport: { width: 1280, height: 800 },
      ...buyerOptions,
    })
  ).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  await seller.request.put("/v1/link-settings", {
    headers: H,
    data: { findable: true },
  });
  const found = await (
    await buyer.request.get(
      `/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`,
      { headers: H },
    )
  ).json();
  await buyer.request.post("/v1/links", {
    headers: H,
    data: { business: found.results[0].business, role: "supplier" },
  });
  const link = (
    await (await seller.request.get("/v1/links", { headers: H })).json()
  ).links[0].id as string;
  await seller.request.post(`/v1/links/${link}/accept`, { headers: H });
  return { seller, buyer, link, name: `Dangote Depot ${stamp}` };
}

const say = (p: Page, link: string, text: string) =>
  p.request.post(`/v1/link-chats/${link}/messages`, {
    headers: H,
    data: { text },
  });

/** A second business that has linked with the buyer, so the list has two. */
async function alsoLinked(buyer: Page, browser: Browser) {
  const stamp = letters();
  const other = await (await browser.newContext()).newPage();
  await signUp(other, `Bello Bakers ${stamp}`);
  await other.request.put("/v1/link-settings", {
    headers: H,
    data: { findable: true },
  });
  const found = await (
    await buyer.request.get(
      `/v1/links/search?q=${encodeURIComponent(`bello bakers ${stamp}`)}`,
      { headers: H },
    )
  ).json();
  await buyer.request.post("/v1/links", {
    headers: H,
    data: { business: found.results[0].business, role: "supplier" },
  });
  const link = (
    await (await other.request.get("/v1/links", { headers: H })).json()
  ).links[0].id as string;
  await other.request.post(`/v1/links/${link}/accept`, { headers: H });
  return { other, link };
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

const withParam = (p: Page) => new URL(p.url()).searchParams.get("with");

test("the keys of the list: / searches, j and k move between chats, ? lists them, and none works while typing", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  const { link: link2 } = await alsoLinked(buyer, browser);
  await say(seller, link, "Hello");
  await buyer.goto("/chat");
  const search = buyer.getByTestId("link-search");
  await expect(buyer.getByTestId("link-linked")).toHaveCount(2);

  await buyer.keyboard.press("/");
  await expect(search).toBeFocused();
  await buyer.keyboard.type("jkn?/"); // typed, not taken for shortcuts
  await expect(search).toHaveValue("jkn?/");
  await buyer.keyboard.press("Escape"); // empties the box
  await expect(search).toHaveValue("");
  await expect(search).toBeFocused();
  await search.blur();

  const goes = async (key: string, to: string | null) => {
    await buyer.keyboard.press(key);
    await expect.poll(() => withParam(buyer)).toBe(to);
  };
  // The list follows what is read (a chat opened has nothing unread and may
  // move), so the keys are checked by where they lead, not by a fixed order.
  await buyer.waitForTimeout(1200);
  await goes("j", "taskiem"); // the list's first
  await expect(buyer.getByTestId("taskiem-pane")).toBeVisible();
  const visited = ["taskiem"];
  for (let i = 0; i < 2; i++) {
    const was = withParam(buyer);
    await buyer.keyboard.press("j");
    await expect.poll(() => withParam(buyer)).not.toBe(was);
    visited.push(withParam(buyer) ?? "");
    await buyer.waitForTimeout(500);
  }
  expect(new Set(visited).size).toBe(3);
  expect([link, link2]).toContain(visited[2]);
  const last = withParam(buyer);
  await buyer.keyboard.press("j"); // the end: stays
  await buyer.waitForTimeout(300);
  expect(withParam(buyer)).toBe(last);
  await buyer.keyboard.press("k");
  await expect.poll(() => withParam(buyer)).not.toBe(last);
  const second = withParam(buyer) as string;

  // Typing in the message box is only typing.
  const box = buyer.locator("#chat-text");
  await box.click();
  await buyer.keyboard.type("jk/n?");
  await expect(box).toHaveValue("jk/n?");
  expect(withParam(buyer)).toBe(second);

  // ? shows the keys, with the focus in the dialog; Esc closes it and leaves the chat.
  await buyer.locator("h2", { hasText: "Chat" }).first().click();
  await buyer.keyboard.press("?");
  const sheet = buyer.getByRole("dialog", { name: "Keyboard" });
  await expect(sheet).toBeVisible();
  await axeBoth(buyer, "the keyboard sheet");
  await buyer.keyboard.press("Escape");
  await expect(sheet).toHaveCount(0);
  expect(withParam(buyer)).toBe(second);

  // n goes to the list and the search box, ready for a new link request.
  await buyer.locator("h2", { hasText: "Chat" }).first().click();
  await buyer.keyboard.press("n");
  await expect(search).toBeFocused();
  expect(withParam(buyer)).toBeNull();
});

test("arrows go down the list and back to the search box, Enter opens a chat", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.goto("/chat");
  await expect(buyer.getByTestId("link-linked")).toHaveCount(1);
  const search = buyer.getByTestId("link-search");
  await search.focus();
  await buyer.keyboard.press("ArrowDown");
  await expect(
    buyer.locator('a.chat-row[href="/chat?with=taskiem"]'),
  ).toBeFocused();
  await buyer.keyboard.press("ArrowDown");
  await expect(
    buyer.locator(`a.chat-row[href="/chat?with=${link}"]`),
  ).toBeFocused();
  await buyer.keyboard.press("ArrowDown"); // the last: stays
  await expect(
    buyer.locator(`a.chat-row[href="/chat?with=${link}"]`),
  ).toBeFocused();
  await buyer.keyboard.press("ArrowUp");
  await buyer.keyboard.press("ArrowUp");
  await expect(search).toBeFocused();
  await buyer.keyboard.press("ArrowDown");
  await buyer.keyboard.press("ArrowDown");
  await buyer.keyboard.press("Enter");
  await expect(buyer.getByTestId("chat-pane")).toBeVisible();
  expect(withParam(buyer)).toBe(link);
});

test("Esc closes the panel first and the chat next, and the chat's row has the focus again", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await say(seller, link, "Hello");
  await buyer.goto(`/chat?with=${link}`);
  const box = buyer.locator("#chat-text");
  await expect(
    buyer.locator('[data-testid="chat-message"]', { hasText: "Hello" }),
  ).toBeVisible();
  await box.click();
  await buyer.getByRole("button", { name: "Emoji" }).click();
  await expect(buyer.getByTestId("emoji-panel")).toBeVisible();
  await buyer.keyboard.press("Escape");
  await expect(buyer.getByTestId("emoji-panel")).toHaveCount(0);
  expect(withParam(buyer)).toBe(link); // the panel only
  await expect(box).toBeFocused(); // back in the box
  await buyer.keyboard.press("Escape");
  await expect(buyer.getByTestId("chat-pane")).toHaveCount(0);
  expect(withParam(buyer)).toBeNull();
  await expect(
    buyer.locator(`a.chat-row[href="/chat?with=${link}"]`),
  ).toBeFocused();
});

test("a right click on a bubble opens its menu; Esc closes it and the chat stays", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await say(seller, link, "Rice is 45k");
  await buyer.goto(`/chat?with=${link}`);
  const b = buyer.locator('[data-testid="chat-message"]', {
    hasText: "Rice is 45k",
  });
  await b.click({ button: "right" });
  const menu = b.getByTestId("chat-more");
  await expect(menu).toBeVisible();
  await expect(menu.getByRole("button").first()).toBeFocused();
  await buyer.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
  expect(withParam(buyer)).toBe(link);
  await expect(b.getByRole("button", { name: /More for/ })).toBeFocused();

  // A click elsewhere closes it too; a right click on a link keeps the browser's own menu.
  await b.click({ button: "right" });
  await expect(menu).toBeVisible();
  await buyer.getByTestId("chat-pane").getByRole("heading").click();
  await expect(menu).toHaveCount(0);

  // Reply from it: the cursor is in the box, to write the reply.
  await b.click({ button: "right" });
  await menu.getByRole("button", { name: "Reply" }).click();
  await expect(buyer.locator("#chat-text")).toBeFocused();
  await buyer.keyboard.type("Send 10 bags");
  await buyer.getByRole("button", { name: "Send", exact: true }).click();
  await expect(buyer.locator("#chat-text")).toBeFocused(); // ready for the next
});

test("a screen reader hears the unread count and the ticks in words", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await say(seller, link, "One");
  await say(seller, link, "Two");
  await buyer.goto("/chat");
  const row = buyer.locator(`a.chat-row[href="/chat?with=${link}"]`);
  await expect(row).toContainText("2 unread messages");
  expect(await row.getAttribute("aria-label")).toBeNull();
  await axeBoth(buyer, "the list with unread");
  await buyer.goto(`/chat?with=${link}`);
  await buyer.locator("#chat-text").fill("Reply");
  await buyer.keyboard.press("Enter");
  await expect(
    buyer.getByTestId("chat-pane").getByRole("img", { name: /Delivered|Read/ }),
  ).toBeVisible();
});
