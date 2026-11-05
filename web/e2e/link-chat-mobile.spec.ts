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
// The Chat page as WhatsApp's app is on a phone and WhatsApp Web on a
// computer: flat heads, a pill search, big avatars, a round button to start a
// chat, the tab bar under the list on a phone, the box as one pill; and long
// messages folded with "Read more".
test.use({ bypassCSP: true });

const must = <T>(v: T | null | undefined): T => {
  if (v == null) throw new Error("nothing there");
  return v;
};
const PHONE = { width: 390, height: 844 };
const long = (n: number) =>
  Array.from({ length: n }, (_, i) => `word${i}`).join(" ");

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
const sideways = (p: Page) =>
  p.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth + 1,
  );

test("on a phone the list has the tab bar and a round button to start a chat; a conversation takes the whole screen", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, {
    viewport: PHONE,
    hasTouch: true,
    isMobile: true,
  });
  await say(seller, link, "Rice is ready");
  await buyer.goto("/chat");
  const bar = buyer.getByRole("navigation", { name: "Quick" });
  await expect(bar).toBeVisible();
  await expect(bar.getByRole("link", { name: /Chat/ })).toHaveAttribute(
    "aria-current",
    "page",
  );
  await expect(bar.getByRole("link", { name: /Chat/ })).toContainText(
    "1 unread",
  );
  await expect(bar.getByRole("button", { name: "Open menu" })).toBeVisible();
  const fab = buyer.getByTestId("new-chat");
  await expect(fab).toBeVisible();
  await expect(fab).toContainText("Find a business");
  const box = must(await fab.boundingBox());
  expect(box.x + box.width).toBeGreaterThan(PHONE.width - 24); // at the right
  const barBox = must(await bar.boundingBox());
  expect(box.y + box.height).toBeLessThanOrEqual(barBox.y); // above the bar
  expect(await sideways(buyer)).toBe(false);
  await axeBoth(buyer, "the list on a phone");

  // The button starts a link request: the search box takes the focus.
  await fab.click();
  await expect(buyer.getByTestId("link-search")).toBeFocused();

  // A conversation: no tab bar, no button; back brings them again.
  await buyer.goto(`/chat?with=${link}`);
  await expect(buyer.getByTestId("chat-pane")).toBeVisible();
  await expect(bar).toBeHidden();
  await expect(fab).toBeHidden();
  expect(await sideways(buyer)).toBe(false);
  await buyer.getByRole("button", { name: "Back to the chats" }).click();
  await expect(bar).toBeVisible();
});

test("the conversation's head shows who they are, search and the menu as plain icons; the box is one pill with the round send beside it", async ({
  browser,
}) => {
  const { seller, buyer, link, name } = await linked(browser, {
    viewport: PHONE,
    hasTouch: true,
    isMobile: true,
  });
  await say(seller, link, "Rice is ready");
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const head = pane.locator(".chat-head");
  await expect(head.getByRole("heading", { name })).toBeVisible();
  await expect(head.locator(".chat-sub")).toHaveText("They supply you");
  // The search is behind its icon, as WhatsApp's.
  await expect(pane.getByTestId("chat-search")).toHaveCount(0);
  await head.getByRole("button", { name: "Search this conversation" }).click();
  await pane.getByTestId("chat-search").fill("Rice");
  await expect(pane.getByTestId("chat-message")).toHaveCount(1);
  await pane.getByTestId("chat-search").press("Escape");
  await expect(pane.getByTestId("chat-search")).toHaveCount(0);

  // The box: emoji, the words, attach and camera in one pill; the microphone, round, beside it.
  const pill = pane.locator(".composer-pill");
  await expect(pill.getByRole("button", { name: "Emoji" })).toBeVisible();
  await expect(pill.locator("#chat-text")).toBeVisible();
  await expect(pill.getByRole("button", { name: "Attach" })).toBeVisible();
  await expect(pill.getByTestId("camera-quick")).toBeVisible();
  const mic = pane.getByTestId("chat-mic");
  const mb = must(await mic.boundingBox());
  expect(Math.round(mb.width)).toBe(Math.round(mb.height));
  await pane.locator("#chat-text").fill("hello");
  const send = pane.getByRole("button", { name: "Send", exact: true });
  const sb = must(await send.boundingBox());
  expect(Math.round(sb.width)).toBe(Math.round(sb.height));
  await send.click();
  await expect(
    pane.getByTestId("chat-message").filter({ hasText: "hello" }),
  ).toBeVisible();
  expect(await sideways(buyer)).toBe(false);
  for (const width of [390, 360]) {
    await buyer.setViewportSize({ width, height: 740 });
    await buyer.waitForTimeout(200);
    expect(await sideways(buyer), `sideways at ${width}`).toBe(false);
    await axeBoth(buyer, `the conversation at ${width}`);
  }
});

test("on a computer the round button is in the list's head, and the list and the conversation sit side by side", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser, {
    viewport: { width: 1280, height: 800 },
  });
  await buyer.goto(`/chat?with=${link}`);
  const btn = buyer.getByTestId("new-chat");
  await expect(btn).toBeVisible();
  await expect(btn.locator(".fab-label")).toBeHidden();
  const head = must(await buyer.locator(".chat-list-head").boundingBox());
  const b = must(await btn.boundingBox());
  expect(b.y).toBeGreaterThanOrEqual(head.y);
  expect(b.y + b.height).toBeLessThanOrEqual(head.y + head.height);
  await expect(buyer.getByTestId("chat-pane")).toBeVisible();
  await expect(buyer.getByRole("navigation", { name: "Quick" })).toBeHidden();
  await btn.click();
  await expect(buyer.getByTestId("link-search")).toBeFocused();
  await axeBoth(buyer, "the list on a computer");
});

test("a long message is folded: Read more shows the next stretch, until the whole", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, { viewport: PHONE });
  const text = long(450); // about 3,500 characters (a message holds 4,000)
  await say(seller, link, text);
  await say(seller, link, "A short one");
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const big = pane.getByTestId("chat-message").filter({ hasText: "word0 " });
  await expect(big).toBeVisible();
  const shown = async () =>
    ((await big.locator(".text").innerText()).match(/word\d+/g) ?? []).length;
  const first = await shown();
  expect(first).toBeGreaterThan(50);
  expect(first).toBeLessThan(200); // not the whole of it
  await expect(big.getByRole("button", { name: "Read more" })).toBeVisible();
  await axeBoth(buyer, "a folded message");
  await big.getByRole("button", { name: "Read more" }).click();
  const second = await shown();
  expect(second).toBeGreaterThan(first + 100);
  expect(second).toBeLessThan(450);
  // The short one has no fold.
  await expect(
    pane
      .getByTestId("chat-message")
      .filter({ hasText: "A short one" })
      .getByRole("button", { name: "Read more" }),
  ).toHaveCount(0);
  for (let i = 0; i < 12; i++) {
    const more = big.getByRole("button", { name: "Read more" });
    if ((await more.count()) === 0) break;
    await more.click();
  }
  await expect(big.getByRole("button", { name: "Read more" })).toHaveCount(0);
  expect(await shown()).toBe(450);
  expect(await sideways(buyer)).toBe(false);
});

test("Taskiem's long answer is folded the same way", async ({ browser }) => {
  const page = await (await browser.newContext({ viewport: PHONE })).newPage();
  await signUp(page, `Chioma Cakes ${letters()}`);
  const t = (n: number) => new Date(Date.now() - n * 60_000).toISOString();
  await page.route("**/v1/chat/stream", (route) => route.abort());
  await page.route(/\/v1\/taskiem-chat(\?.*)?$/, (route) =>
    route.request().method() !== "GET"
      ? route.continue()
      : route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            messages: [
              {
                id: "11111111-0000-4000-8000-000000000001",
                channel: "web",
                inbound: true,
                kind: "text",
                text: "help",
                created_at: t(3),
              },
              {
                id: "11111111-0000-4000-8000-000000000002",
                channel: "web",
                inbound: false,
                kind: "text",
                text: long(900),
                created_at: t(2),
              },
            ],
          }),
        }),
  );
  await page.goto("/chat?with=taskiem");
  const answer = page.getByTestId("taskiem-message").last();
  await expect(answer.getByRole("button", { name: "Read more" })).toBeVisible();
  const n = async () =>
    ((await answer.locator(".text").innerText()).match(/word\d+/g) ?? [])
      .length;
  const first = await n();
  expect(first).toBeLessThan(200);
  await answer.getByRole("button", { name: "Read more" }).click();
  expect(await n()).toBeGreaterThan(first + 100);
  await axeBoth(page, "a folded answer");
});

test("the camera is beside the clip on a touch screen, inside the pill; a computer's window, narrow or wide, has it in the clip's menu", async ({
  browser,
}) => {
  const phone = await linked(browser, {
    viewport: PHONE,
    hasTouch: true,
    isMobile: true,
  });
  await phone.buyer.goto(`/chat?with=${phone.link}`);
  const pane = phone.buyer.getByTestId("chat-pane");
  const quick = pane.getByTestId("camera-quick");
  await expect(quick).toBeVisible();
  const p = must(await pane.locator(".composer-pill").boundingBox());
  const q = must(await quick.boundingBox());
  expect(q.x).toBeGreaterThanOrEqual(p.x);
  expect(q.x + q.width).toBeLessThanOrEqual(p.x + p.width + 1); // inside the pill
  await pane.locator("#chat-text").fill("hi");
  await expect(quick).toBeHidden(); // the send button takes over, as WhatsApp's

  for (const viewport of [PHONE, { width: 1280, height: 800 }]) {
    const computer = await linked(browser, { viewport }); // a mouse: no touch
    await computer.buyer.goto(`/chat?with=${computer.link}`);
    await expect(
      computer.buyer.getByTestId("chat-pane").getByTestId("camera-quick"),
    ).toBeHidden();
  }
});
