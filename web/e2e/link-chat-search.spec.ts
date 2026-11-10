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
// Search in a conversation as WhatsApp's: a panel beside the thread (the whole
// screen on a phone) with the box, a calendar to go to a day, and the
// messages found as rows with the hit marked; the conversation stays behind.
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

test("on a computer the panel sits beside the thread; rows show who, when and the marked words; a row goes to the message; keys and Esc", async ({
  browser,
}) => {
  const { seller, buyer, link, name } = await linked(browser, {
    viewport: { width: 1280, height: 800 },
  });
  await say(seller, link, "Rice is ₦45,000 a bag this week");
  await say(buyer, link, "Send twenty bags of rice please");
  await say(seller, link, "Palm oil is ready too");
  for (let i = 0; i < 25; i++) await say(seller, link, `Filler ${i}`);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByRole("button", { name: "Search this conversation" }).click();
  const panel = pane.getByTestId("chat-search-panel");
  await expect(
    panel.getByRole("heading", { name: "Search messages" }),
  ).toBeVisible();
  await expect(panel.getByTestId("chat-search")).toBeFocused();
  await expect(panel).toContainText(`Search for messages within ${name}.`);
  await expect(
    panel.getByRole("button", { name: "Search by date" }),
  ).toBeVisible();
  // Beside the thread: the head and the thread end where the panel begins.
  const pb = must(await panel.boundingBox());
  const stream = must(await pane.getByTestId("chat-stream").boundingBox());
  expect(stream.x + stream.width).toBeLessThanOrEqual(pb.x + 2);
  await axeBoth(buyer, "the search panel, empty");

  await panel.getByTestId("chat-search").fill("rice");
  const rows = panel.getByTestId("chat-result");
  await expect(rows).toHaveCount(2);
  await expect(rows.first().locator("mark")).toHaveText(/rice/i);
  await expect(rows.first().locator(".sr-who")).not.toHaveText("");
  await expect(rows.first().locator(".sr-time")).not.toHaveText("");
  await expect(pane.getByTestId("chat-message").first()).toBeVisible(); // the thread is still there
  await axeBoth(buyer, "the search panel, with results");

  // Keys: Down from the box into the rows, Up back; Enter goes to the message.
  await panel.getByTestId("chat-search").focus();
  await buyer.keyboard.press("ArrowDown");
  await expect(rows.first()).toBeFocused();
  await buyer.keyboard.press("ArrowDown");
  await expect(rows.nth(1)).toBeFocused();
  await buyer.keyboard.press("ArrowUp");
  await buyer.keyboard.press("ArrowUp");
  await expect(panel.getByTestId("chat-search")).toBeFocused();
  const send = rows.filter({ hasText: "Send twenty bags" });
  await send.click();
  const bubble = pane
    .getByTestId("chat-message")
    .filter({ hasText: "Send twenty bags" });
  // The flash lasts 1.8 s: looked for first, while the scroll settles.
  await expect(bubble).toHaveClass(/flash/);
  await expect(bubble).toBeInViewport();
  await expect(panel).toBeVisible(); // beside the thread, it stays

  await panel.getByTestId("chat-search").fill("zebra");
  await expect(panel).toContainText("No messages say that.");
  await panel.getByTestId("chat-search").fill("r");
  await expect(panel).toContainText(`Search for messages within ${name}.`);

  // Esc closes the panel only; the chat stays and the search icon has the focus.
  await buyer.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);
  await expect(buyer).toHaveURL(new RegExp(`with=${link}`));
  await expect(
    pane.getByRole("button", { name: "Search this conversation" }),
  ).toBeFocused();
  // The ✕ closes it too.
  await pane.getByRole("button", { name: "Search this conversation" }).click();
  await pane.getByRole("button", { name: "Close search" }).click();
  await expect(panel).toHaveCount(0);
});

test("the calendar goes to a day: the first message of it or after it, and says when there is none", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, {
    viewport: { width: 1280, height: 800 },
  });
  await say(seller, link, "The first message");
  for (let i = 0; i < 16; i++)
    await say(
      seller,
      link,
      `Filler ${i} ` + "and some more words to make it long ".repeat(6),
    );
  await buyer.goto(`/chat?with=${link}`);
  await buyer.waitForTimeout(1200); // read: the thread opens at its end, not at the first unread
  await buyer.reload();
  const pane = buyer.getByTestId("chat-pane");
  await expect(
    pane.getByTestId("chat-message").filter({ hasText: "Filler 15 " }),
  ).toBeInViewport();
  await pane.getByRole("button", { name: "Search this conversation" }).click();
  const panel = pane.getByTestId("chat-search-panel");
  await expect(
    pane.getByTestId("chat-message").filter({ hasText: "The first message" }),
  ).not.toBeInViewport();
  await panel.getByRole("button", { name: "Search by date" }).click();
  const cal = panel.getByTestId("chat-calendar");
  await expect(cal).toBeVisible();
  await axeBoth(buyer, "the search panel with the calendar");
  // A day before the conversation (the month before): its first message.
  await cal.getByRole("button", { name: "Previous month" }).click();
  await cal.locator(".dp-day:not(.other):not(:disabled)").first().click();
  await expect(cal).toHaveCount(0); // chosen: it closes
  const first = pane
    .getByTestId("chat-message")
    .filter({ hasText: "The first message" });
  // The flash lasts 1.8 s: looked for first, while the scroll settles.
  await expect(first).toHaveClass(/flash/);
  await expect(first).toBeInViewport();
  await expect(panel.getByTestId("chat-date-open")).toBeFocused();

  // A day after all of it: nothing, and it says so.
  await buyer.route(/\/messages\?after=/, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ messages: [] }),
    }),
  );
  await panel.getByRole("button", { name: "Search by date" }).click();
  await panel.getByTestId("chat-calendar").locator(".dp-day.today").click();
  await expect(panel).toContainText("No messages on or after that day.");
});

test("on a phone it is the whole screen, the rows fit, nothing scrolls sideways, and a row gives way to the thread", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, {
    viewport: { width: 390, height: 740 },
    hasTouch: true,
    isMobile: true,
  });
  await say(
    seller,
    link,
    "Rice is ready: " + "and a very long line of words ".repeat(12),
  );
  await say(seller, link, "Another rice message");
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByRole("button", { name: "Search this conversation" }).click();
  const panel = pane.getByTestId("chat-search-panel");
  await panel.getByTestId("chat-search").fill("rice");
  await expect(panel.getByTestId("chat-result")).toHaveCount(2, {
    timeout: 10_000,
  });
  const r = must(await panel.boundingBox());
  expect(r.x).toBeLessThanOrEqual(1);
  expect(r.width).toBeGreaterThanOrEqual(389); // all of the screen, not 360 of it
  expect(
    await buyer.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth + 1,
    ),
  ).toBe(false);
  await axeBoth(buyer, "the search panel on a phone");
  await panel.getByTestId("chat-result").first().click();
  await expect(panel).toHaveCount(0, { timeout: 10_000 });
});

test("the calendar is a month with arrows, today filled in, later days dimmed and not to be chosen; keys move through it; Esc closes only it", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser, {
    viewport: { width: 1280, height: 800 },
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByRole("button", { name: "Search this conversation" }).click();
  const panel = pane.getByTestId("chat-search-panel");
  await panel.getByRole("button", { name: "Search by date" }).click();
  const cal = panel.getByTestId("chat-calendar");
  const now = new Date();
  const month = now.toLocaleDateString(undefined, {
    month: "long",
    year: "numeric",
  });
  await expect(cal.locator(".dp-month")).toHaveText(month);
  await expect(cal.getByRole("columnheader")).toHaveCount(7);
  await expect(cal.locator(".dp-day")).toHaveCount(42); // six weeks
  const today = cal.locator(".dp-day.today");
  await expect(today).toHaveText(String(now.getDate()));
  await expect(today).toHaveAttribute("aria-current", "date");
  await expect(today).toHaveAccessibleName(/\d{4}/); // "Wednesday 4 November 2026"
  // Today is the day with the focus; days after it cannot be chosen, nor can the month after.
  await expect(today).toBeFocused();
  await expect(cal.getByRole("button", { name: "Next month" })).toBeDisabled();
  const later = cal.locator(".dp-day:disabled");
  expect(await later.count()).toBeGreaterThan(0);
  const tb = must(await today.boundingBox());
  expect(Math.round(tb.width)).toBe(Math.round(tb.height)); // round

  // Keys: left a day, up a week, Page Up a month back (the arrows then allow the month after).
  await buyer.keyboard.press("ArrowLeft");
  await expect(cal.locator('.dp-day[tabindex="0"]')).toHaveAccessibleName(
    /\d{4}/,
  );
  await buyer.keyboard.press("ArrowUp");
  await buyer.keyboard.press("PageUp");
  await expect(cal.locator(".dp-month")).not.toHaveText(month);
  await expect(cal.getByRole("button", { name: "Next month" })).toBeEnabled();
  // A day and a week back is this month or the last, by today's date (the
  // 4th: the last; the 9th: this); Page Up one more. As many arrows on.
  const back = new Date(now);
  back.setDate(now.getDate() - 8);
  const months =
    (now.getFullYear() - back.getFullYear()) * 12 +
    now.getMonth() -
    back.getMonth() +
    1;
  for (let i = 0; i < months; i++)
    await cal.getByRole("button", { name: "Next month" }).click();
  await expect(cal.locator(".dp-month")).toHaveText(month);
  await expect(cal.getByRole("button", { name: "Next month" })).toBeDisabled();

  // A click elsewhere and Esc close the calendar only.
  await buyer.keyboard.press("Escape");
  await expect(cal).toHaveCount(0);
  await expect(panel).toBeVisible();
  await expect(panel.getByTestId("chat-date-open")).toBeFocused();
  await panel.getByRole("button", { name: "Search by date" }).click();
  await panel.getByRole("heading", { name: "Search messages" }).click();
  await expect(cal).toHaveCount(0);
  await expect(panel).toBeVisible();

  // On a phone it fits the screen.
  await buyer.setViewportSize({ width: 360, height: 740 });
  await panel.getByRole("button", { name: "Search by date" }).click();
  const r = must(await cal.boundingBox());
  expect(r.x).toBeGreaterThanOrEqual(0);
  expect(r.x + r.width).toBeLessThanOrEqual(360);
  expect(
    await buyer.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth + 1,
    ),
  ).toBe(false);
  await axeBoth(buyer, "the calendar on a phone");
});
