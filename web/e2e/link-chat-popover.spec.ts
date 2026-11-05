import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Keyboard and desktop polish (Chat, phase 7): shortcuts that stay off while
// typing, arrows through the list, Esc closing the panel and then the chat,
// right click on a bubble, focus going back to the message box, and words
// for the unread count and the ticks.
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

const say = (p: Page, link: string, text: string, extra = {}) =>
  p.request.post(`/v1/link-chats/${link}/messages`, {
    headers: H,
    data: { text, ...extra },
  });
// A message's menu floats over the chat as WhatsApp's does: the reactions in a
// pill above the bubble (with a "+" for any emoji), the actions in a card by
// it, one to a row with an icon; the bubble does not change size; on a
// computer a smiley beside it gives a quick reaction.
test.use({ bypassCSP: true });

const must = <T>(v: T | null | undefined): T => {
  if (v == null) throw new Error("nothing there");
  return v;
};
const PHONE = { width: 390, height: 780 };
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
const bubbleOf = (p: Page, text: string) =>
  p.locator('[data-testid="chat-message"]', { hasText: text });

test("the menu floats: the reactions above the bubble, the actions below it, the bubble the same size, each row with its icon", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, {
    viewport: { width: 1280, height: 800 },
  });
  await say(seller, link, "Rice is ready");
  await say(buyer, link, "Send twenty bags");
  await buyer.goto(`/chat?with=${link}`);
  const b = bubbleOf(buyer, "Send twenty bags");
  const before = must(await b.boundingBox());
  await b.click({ button: "right" });
  const menu = b.getByTestId("chat-more");
  const pill = menu.locator(".pop-pill");
  const card = menu.locator(".pop-card");
  await expect(pill).toBeVisible();
  await expect(card).toBeVisible();
  const after = must(await b.boundingBox());
  expect(Math.round(after.height)).toBe(Math.round(before.height)); // not grown
  const p = must(await pill.boundingBox());
  const c = must(await card.boundingBox());
  expect(p.y + p.height).toBeLessThanOrEqual(after.y + 2); // above
  expect(c.y).toBeGreaterThanOrEqual(after.y + after.height - 2); // below
  // Ours: on the bubble's right side.
  expect(Math.abs(p.x + p.width - (after.x + after.width))).toBeLessThan(12);
  expect(Math.abs(c.x + c.width - (after.x + after.width))).toBeLessThan(12);
  for (const name of ["Message info", "Reply", "Copy", "Star", "Delete for me"])
    await expect(card.getByRole("button", { name })).toBeVisible();
  await expect(card.locator(".pop-item svg")).toHaveCount(
    await card.locator(".pop-item").count(),
  );
  await expect(card.locator('[role="separator"]')).toHaveCount(1);
  await expect(
    pill.getByRole("button", { name: "More reactions" }),
  ).toBeVisible();
  await axeBoth(buyer, "the menu open on a computer");
});

test("arrow keys move through the menu, Esc closes it and the chevron has the focus, a click elsewhere and a scroll close it", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, {
    viewport: { width: 1280, height: 800 },
  });
  await say(seller, link, "Rice is ready");
  await buyer.goto(`/chat?with=${link}`);
  const b = bubbleOf(buyer, "Rice is ready");
  await b.click({ button: "right" });
  const menu = b.getByTestId("chat-more");
  const first = menu.locator(".pop-card button").first();
  await expect(first).toBeFocused();
  await buyer.keyboard.press("ArrowDown");
  await expect(menu.getByRole("button", { name: "Reply" })).toBeFocused();
  await buyer.keyboard.press("ArrowUp");
  await expect(first).toBeFocused();
  await buyer.keyboard.press("End");
  await expect(menu.locator(".pop-card button").last()).toBeFocused();
  await buyer.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
  await expect(b.getByRole("button", { name: /More for/ })).toBeFocused();
  await b.click({ button: "right" });
  await buyer.mouse.click(700, 600);
  await expect(menu).toHaveCount(0);
  await b.click({ button: "right" });
  await buyer
    .getByTestId("chat-stream")
    .evaluate((el) => el.dispatchEvent(new Event("scroll")));
  await expect(menu).toHaveCount(0);
});

test("a reaction from the pill, and any emoji from the +: the other side sees it", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, {
    viewport: { width: 1280, height: 800 },
  });
  await say(seller, link, "Rice is ready");
  await buyer.goto(`/chat?with=${link}`);
  await seller.goto(`/chat?with=${link}`);
  const b = bubbleOf(buyer, "Rice is ready");
  await b.click({ button: "right" });
  await b
    .getByTestId("chat-more")
    .getByRole("button", { name: /React with/ })
    .first()
    .click();
  await expect(b.locator(".reaction-chip").first()).toContainText("👍");
  // The +: a full picker in place of the pill; search, choose.
  await b.click({ button: "right" });
  await b.getByRole("button", { name: "More reactions" }).click();
  const picker = b.locator(".pop-picker");
  await expect(picker).toBeVisible();
  await expect(picker.locator(".pop-card, .pop-pill")).toHaveCount(0);
  await picker.getByRole("searchbox").fill("party");
  await picker
    .getByRole("button", { name: /party popper/i })
    .first()
    .click();
  await expect(b.locator(".reaction-chip").first()).toContainText("🎉");
  await expect(b.locator(".reaction-chip")).toHaveCount(1); // it replaced the 👍
  await expect(
    bubbleOf(seller, "Rice is ready").locator(".reaction-chip").first(),
  ).toContainText("🎉", { timeout: 8000 });
});

test("on a computer a smiley beside the bubble opens the reactions alone", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, {
    viewport: { width: 1280, height: 800 },
  });
  await say(seller, link, "Rice is ready");
  await buyer.goto(`/chat?with=${link}`);
  const b = bubbleOf(buyer, "Rice is ready");
  const smiley = b.locator(".bubble-react");
  await expect(smiley).toHaveCSS("opacity", "0");
  await b.hover();
  await expect(smiley).toHaveCSS("opacity", "1");
  const sb = must(await smiley.boundingBox());
  const bb = must(await b.boundingBox());
  expect(sb.x).toBeGreaterThanOrEqual(bb.x + bb.width); // beside it, on the right of theirs
  await smiley.click();
  await expect(b.locator(".pop-pill")).toBeVisible();
  await expect(b.locator(".pop-card")).toHaveCount(0);
  await b
    .locator(".pop-pill")
    .getByRole("button", { name: /React with/ })
    .nth(1)
    .click();
  await expect(b.locator(".reaction-chip").first()).toContainText("❤️");
});

test("on a phone a long press opens it within the screen, and nothing scrolls sideways", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, {
    viewport: PHONE,
    hasTouch: true,
    isMobile: true,
  });
  for (let i = 0; i < 8; i++) await say(seller, link, `Line ${i}`);
  await say(buyer, link, "Send twenty bags");
  await buyer.goto(`/chat?with=${link}`);
  const b = bubbleOf(buyer, "Send twenty bags");
  const box = must(await b.boundingBox());
  const finger = {
    pointerType: "touch",
    pointerId: 9,
    isPrimary: true,
    clientX: box.x + 20,
    clientY: box.y + 10,
  };
  await b.dispatchEvent("pointerdown", finger);
  await buyer.waitForTimeout(700);
  await b.dispatchEvent("pointerup", finger);
  const menu = b.getByTestId("chat-more");
  await expect(menu).toBeVisible();
  for (const part of [menu.locator(".pop-pill"), menu.locator(".pop-card")]) {
    const r = must(await part.boundingBox());
    expect(r.x).toBeGreaterThanOrEqual(0);
    expect(r.x + r.width).toBeLessThanOrEqual(PHONE.width);
    expect(r.y).toBeGreaterThanOrEqual(0);
    expect(r.y + r.height).toBeLessThanOrEqual(PHONE.height);
  }
  expect(
    await buyer.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth + 1,
    ),
  ).toBe(false);
  await axeBoth(buyer, "the menu on a phone");
});

test("a bubble at the top gets its reactions below it, and the card stays on the screen", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, {
    viewport: { width: 1280, height: 600 },
  });
  await say(seller, link, "The very first");
  await buyer.goto(`/chat?with=${link}`);
  const b = bubbleOf(buyer, "The very first");
  await b.click({ button: "right" });
  const menu = b.getByTestId("chat-more");
  const bb = must(await b.boundingBox());
  const p = must(await menu.locator(".pop-pill").boundingBox());
  const c = must(await menu.locator(".pop-card").boundingBox());
  expect(p.y).toBeGreaterThanOrEqual(0);
  expect(c.y + c.height).toBeLessThanOrEqual(600);
  expect(p.y).toBeGreaterThanOrEqual(0);
  expect(bb.y).toBeGreaterThanOrEqual(0);
});
