import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Keyboard and desktop polish (Chat, phase 7): shortcuts that stay off while
// typing, arrows through the list, Esc closing the panel and then the chat,
// right click on a bubble, focus going back to the message box, and words
// for the unread count and the ticks.
test.use({
  bypassCSP: true,
  viewport: { width: 1280, height: 800 },
  launchOptions: {
    args: [
      "--use-fake-device-for-media-stream",
      "--use-fake-ui-for-media-stream",
    ],
  },
});

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
  await expect(menu.locator(".pop-card button").first()).toBeFocused(); // the first row, as WhatsApp's
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

/** What a keyboard user sees on the focused element: a ring (outline or shadow), not clipped away. */
const ringOf = (p: Page) =>
  p.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    if (!el || el === document.body) return null;
    const cs = getComputedStyle(el);
    const shadow = cs.boxShadow !== "none" && cs.boxShadow !== "";
    const outline =
      cs.outlineStyle !== "none" && parseFloat(cs.outlineWidth) > 0;
    // The message box shows its ring on the pill around it.
    const pill = el.closest(".composer-pill");
    const pillRing =
      !!pill && getComputedStyle(pill).boxShadow.includes("0px 0px 0px 2px");
    const r = el.getBoundingClientRect();
    const name =
      el.getAttribute("aria-label") ??
      el.textContent?.trim().slice(0, 30) ??
      el.tagName;
    return {
      name: `${el.tagName} ${name}`,
      ring: shadow || outline || pillRing,
      visible: r.width > 0 && r.height > 0,
    };
  });

test("every control reached with Tab shows a ring: the list, the conversation and its composer", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await say(seller, link, "Rice is 45k");
  for (const path of ["/chat", `/chat?with=${link}`]) {
    await buyer.goto(path);
    await expect(buyer.getByTestId("link-linked")).toHaveCount(1);
    await buyer.locator("body").click({ position: { x: 5, y: 5 } });
    const seen: string[] = [];
    const bare: string[] = [];
    for (let i = 0; i < 45; i++) {
      await buyer.keyboard.press("Tab");
      const r = await ringOf(buyer);
      if (!r) continue;
      if (seen.includes(r.name) && seen.length > 10) break;
      seen.push(r.name);
      if (r.visible && !r.ring) bare.push(r.name);
    }
    expect(seen.length).toBeGreaterThan(8);
    expect(bare, `no ring on: ${bare.join(", ")} (${path})`).toEqual([]);
  }
});

test("Esc cancels a recording and leaves the chat open; Caps Lock does not turn the keys off", async ({
  browser,
}) => {
  const { buyer, link, seller } = await linked(browser, {
    permissions: ["microphone"],
  });
  await say(seller, link, "Hello");
  await buyer.goto(`/chat?with=${link}`);
  await buyer.getByRole("button", { name: "Record a voice note" }).click();
  await expect(buyer.getByTestId("chat-recording")).toBeVisible();
  await buyer.keyboard.press("Escape");
  await expect(buyer.getByTestId("chat-recording")).toHaveCount(0);
  await expect(buyer.getByTestId("chat-pane")).toBeVisible();
  expect(withParam(buyer)).toBe(link);
  await expect(buyer.locator("#chat-text")).toBeFocused();
  // Nothing was sent.
  await expect(buyer.getByTestId("chat-message")).toHaveCount(1);

  await buyer.locator("h2", { hasText: "Chat" }).first().click();
  await buyer.keyboard.press("Shift+K"); // k with Caps Lock on: previous chat
  await expect.poll(() => withParam(buyer)).toBe("taskiem");
});

test("a right click keeps the browser's menu on the bubble's own selected text, not for a selection elsewhere", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await say(seller, link, "Rice is 45k a bag");
  await buyer.goto(`/chat?with=${link}`);
  const b = buyer.locator('[data-testid="chat-message"]', {
    hasText: "Rice is 45k",
  });
  const text = b.locator(".text");
  await expect(text).toBeVisible();
  await text.evaluate((el) => {
    const r = document.createRange();
    r.selectNodeContents(el);
    getSelection()?.removeAllRanges();
    getSelection()?.addRange(r);
  });
  await text.dispatchEvent("contextmenu", { button: 2, cancelable: true });
  await expect(b.getByTestId("chat-more")).toHaveCount(0);
  // The selection is elsewhere: the menu opens.
  await buyer.evaluate(() => {
    const h = document.querySelector('[data-testid="chat-pane"] h2');
    const r = document.createRange();
    if (h) r.selectNodeContents(h);
    getSelection()?.removeAllRanges();
    getSelection()?.addRange(r);
  });
  await text.dispatchEvent("contextmenu", { button: 2, cancelable: true });
  await expect(b.getByTestId("chat-more")).toBeVisible();
});

test("the cursor goes back to the message box when the info sheet, a waiting photo or a menu is closed; j and k stay out of an open menu", async ({
  browser,
}) => {
  const { seller, buyer, link, name } = await linked(browser);
  await say(seller, link, "Hello");
  await buyer.goto(`/chat?with=${link}`);
  const box = buyer.locator("#chat-text");
  await expect(buyer.getByTestId("chat-message")).toHaveCount(1);

  await buyer.getByRole("button", { name: `About ${name}` }).click();
  await expect(buyer.locator(".chat-info")).toBeVisible();
  await buyer.keyboard.press("Escape");
  await expect(buyer.locator(".chat-info")).toHaveCount(0);
  await expect(box).toBeFocused();
  expect(withParam(buyer)).toBe(link);

  await buyer
    .locator('input[type="file"]')
    .first()
    .setInputFiles({
      name: "p.png",
      mimeType: "image/png",
      buffer: Buffer.from(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
        "base64",
      ),
    });
  await expect(buyer.getByTestId("chat-attaching")).toBeVisible();
  await expect(box).toBeFocused(); // to write its caption
  await buyer.keyboard.press("Escape");
  await expect(buyer.getByTestId("chat-attaching")).toHaveCount(0);
  await expect(box).toBeFocused();

  // The conversation's menu: the keys of the list are not typed into it, Esc gives the focus back.
  const more = buyer.getByRole("button", {
    name: "More about this conversation",
  });
  await more.click();
  await expect(buyer.getByTestId("chat-menu")).toBeVisible();
  await buyer.locator("body").click({ position: { x: 5, y: 5 } });
  await more.click();
  await buyer.keyboard.press("j");
  await buyer.waitForTimeout(300);
  expect(withParam(buyer)).toBe(link);
  await buyer.keyboard.press("Escape");
  await expect(buyer.getByTestId("chat-menu")).toHaveCount(0);
  await expect(more).toBeFocused();
  expect(withParam(buyer)).toBe(link);

  // The menu lists the keys.
  await more.click();
  await buyer
    .getByTestId("chat-menu")
    .getByRole("button", { name: "Keyboard" })
    .click();
  await expect(buyer.getByRole("dialog", { name: "Keyboard" })).toBeVisible();
});

test("arrows reach a search's results too, and Esc in an empty search box leaves it", async ({
  browser,
}) => {
  const { buyer, name } = await linked(browser);
  await buyer.goto("/chat");
  const search = buyer.getByTestId("link-search");
  await search.fill(name.slice(0, 14));
  await expect(
    buyer.locator(".chat-list-pane section :is(a[href], button)").first(),
  ).toBeVisible();
  await buyer.keyboard.press("ArrowDown");
  const inResults = await buyer.evaluate(
    () => !!document.activeElement?.closest(".chat-list-pane section"),
  );
  expect(inResults).toBe(true);
  await buyer.keyboard.press("ArrowUp");
  await expect(search).toBeFocused();
  await search.fill("");
  await buyer.keyboard.press("Escape");
  await expect(search).not.toBeFocused();
});

test("a reply that quotes a message passes axe in both themes (the quote's colour on its tint)", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  const first = await (await say(seller, link, "Rice is 45k")).json();
  await seller.request.post(`/v1/link-chats/${link}/messages`, {
    headers: H,
    data: { text: "Done. Invoice follows.", reply_to: first.message.id },
  });
  await buyer.goto(`/chat?with=${link}`);
  await expect(buyer.getByTestId("chat-quote").first()).toBeVisible();
  await axeBoth(buyer, "a quoted reply");
});
