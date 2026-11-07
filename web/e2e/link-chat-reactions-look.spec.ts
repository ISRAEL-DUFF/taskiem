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
test.use({ bypassCSP: true });
const must = <T>(v: T | null | undefined): T => {
  if (v == null) throw new Error("nothing there");
  return v;
};
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

test("a reaction is a small borderless chip on the bubble's bottom corner, half over its edge", async ({
  browser,
}) => {
  for (const size of [
    { width: 390, height: 844 },
    { width: 1280, height: 800 },
  ]) {
    const { seller, buyer, link } = await linked(browser, { viewport: size });
    const m = await (await say(seller, link, "Rice is ready")).json();
    const mine = await (await say(buyer, link, "Send twenty bags")).json();
    await buyer.request.put(
      `/v1/link-chats/${link}/messages/${m.message.id}/reaction`,
      { headers: H, data: { emoji: "👍" } },
    );
    await seller.request.put(
      `/v1/link-chats/${link}/messages/${mine.message.id}/reaction`,
      { headers: H, data: { emoji: "❤️" } },
    );
    await buyer.request.put(
      `/v1/link-chats/${link}/messages/${mine.message.id}/reaction`,
      { headers: H, data: { emoji: "❤️" } },
    );
    await buyer.goto(`/chat?with=${link}`);
    const theirs = buyer.locator('[data-testid="chat-message"]', {
      hasText: "Rice is ready",
    });
    const ours = buyer.locator('[data-testid="chat-message"]', {
      hasText: "Send twenty bags",
    });
    for (const [bubble, side] of [
      [theirs, "left"],
      [ours, "right"],
    ] as const) {
      const chip = bubble.locator(".reaction-chip").first();
      await expect(chip).toBeVisible();
      const b = must(await bubble.boundingBox());
      const c = must(await chip.boundingBox());
      // Half over the bubble's bottom edge: its middle is at the edge, give or take.
      const middle = c.y + c.height / 2;
      expect(Math.abs(middle - (b.y + b.height))).toBeLessThanOrEqual(3);
      // Inside the bubble's width, at the corner of its own side.
      expect(c.x).toBeGreaterThanOrEqual(b.x);
      expect(c.x + c.width).toBeLessThanOrEqual(b.x + b.width);
      if (side === "left") expect(c.x - b.x).toBeLessThan(20);
      else expect(b.x + b.width - (c.x + c.width)).toBeLessThan(20);
      // No border.
      expect(
        await chip.evaluate((el) => getComputedStyle(el).borderTopWidth),
      ).toBe("0px");
      // It does not hide the time.
      const stamp = must(await bubble.locator(".stamp").boundingBox());
      expect(stamp.y + stamp.height).toBeLessThanOrEqual(c.y + 2);
    }
    // Two people giving the same one is a pill with a count.
    await expect(ours.locator(".reaction-chip .count")).toHaveText("2");
    await axeBoth(buyer, `reactions at ${size.width}`);
  }
});
