import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// The thread behaves like WhatsApp Web's: older messages load as the reader
// scrolls up and the view holds; a new message does not move a reader who
// is looking at older ones (a button counts them); "N unread messages" marks
// where the unread begin; a quote, or a search result, goes to its message.
// The conversation is long, so the API's pages are served by a stand-in
// here (the around parameter itself is tested in Go).
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const T0 = Date.parse("2026-11-01T08:00:00Z");

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

type Msg = Record<string, unknown> & { id: string; created_at: string };
const idOf = (i: number) =>
  `0192e2e0-0000-7000-8000-${String(i).padStart(12, "0")}`;

/** A conversation of n messages, a minute apart, theirs and ours alternating
 * in threes; the last `unread` of theirs not yet read. */
function conversation(link: string, n: number, unread = 0): Msg[] {
  const list: Msg[] = [];
  for (let i = 1; i <= n; i++) {
    const mine = i % 3 === 0;
    const created = new Date(T0 + i * 60_000).toISOString();
    const isUnread = !mine && i > n - unread;
    list.push({
      id: idOf(i),
      link,
      mine,
      kind: "text",
      body: `Message ${i}`,
      sender_name: mine ? undefined : "Musa",
      read_at: isUnread ? undefined : created,
      created_at: created,
    });
  }
  return list;
}

/** The messages API over a list: newest page, before, after, around. */
function serve(all: Msg[]) {
  return async (route: Route) => {
    if (route.request().method() !== "GET") return route.continue();
    const q = new URL(route.request().url()).searchParams;
    const limit = Number(q.get("limit") ?? 50);
    let out: Msg[];
    if (q.get("around")) {
      const at = all.findIndex((m) => m.id === q.get("around"));
      if (at < 0)
        return route.fulfill({
          status: 404,
          contentType: "application/json",
          body: '{"error":"not found"}',
        });
      const half = Math.floor(limit / 2);
      out = all.slice(Math.max(0, at - half), at + 1 + (limit - half));
    } else if (q.get("before"))
      out = all.filter((m) => m.created_at < q.get("before")!).slice(-limit);
    else if (q.get("after"))
      out = all.filter((m) => m.created_at > q.get("after")!).slice(-limit);
    else out = all.slice(-limit);
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ messages: out }),
    });
  };
}

async function linked(browser: import("@playwright/test").Browser) {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const buyer = await (
    await browser.newContext({ viewport: { width: 390, height: 844 } })
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
  return { buyer, link };
}

/** The page around the thread has not scrolled: the chat's header is still in view. */
async function pageStill(page: Page) {
  await expect(
    page.getByTestId("chat-pane").getByRole("heading", { level: 2 }),
  ).toBeInViewport();
  expect(
    await page.evaluate(() => document.scrollingElement?.scrollTop ?? 0),
  ).toBe(0);
}

const scrollTop = (page: Page) =>
  page.getByTestId("chat-stream").evaluate((el) => el.scrollTop);
const fromBottom = (page: Page) =>
  page
    .getByTestId("chat-stream")
    .evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight);

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(400);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

test("older messages load as the reader scrolls up, and the view holds", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.route(
    new RegExp(`/v1/link-chats/${link}/messages(\\?.*)?$`),
    serve(conversation(link, 120)),
  );
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  // Opens at the newest, at the bottom, with only a page of it loaded.
  await expect(pane.getByText("Message 120", { exact: true })).toBeVisible();
  expect(await fromBottom(buyer)).toBeLessThan(5);
  await expect(pane.getByTestId("chat-message")).toHaveCount(50);

  // Scroll to the top: the next page comes, and the reader keeps their place.
  await buyer.getByTestId("chat-stream").evaluate((el) => (el.scrollTop = 0));
  await expect(pane.getByTestId("chat-message")).toHaveCount(100);
  const first = pane.getByText("Message 71", { exact: true }); // the oldest of the first page
  const box = (await first.boundingBox())!;
  const stream = (await buyer.getByTestId("chat-stream").boundingBox())!;
  expect(box.y).toBeGreaterThan(stream.y);
  expect(box.y).toBeLessThan(stream.y + stream.height);

  // And the rest, back to the first message.
  await buyer.getByTestId("chat-stream").evaluate((el) => (el.scrollTop = 0));
  await expect(pane.getByTestId("chat-message")).toHaveCount(120);
  await buyer.getByTestId("chat-stream").evaluate((el) => (el.scrollTop = 0));
  await expect(pane.getByText("Message 1", { exact: true })).toBeVisible();
  await expect(pane.getByTestId("chat-top")).toHaveCount(0); // nothing older
});

test("a new message does not move a reader looking at older ones", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  const all = conversation(link, 120);
  await buyer.route(
    new RegExp(`/v1/link-chats/${link}/messages(\\?.*)?$`),
    serve(all),
  );
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await expect(pane.getByText("Message 120", { exact: true })).toBeVisible();

  // At the bottom, a new message follows the reader down.
  all.push(conversation(link, 121)[120]!);
  await expect(pane.getByText("Message 121", { exact: true })).toBeVisible({
    timeout: 10_000,
  });
  expect(await fromBottom(buyer)).toBeLessThan(5);

  // Scrolled up, it does not: a button counts them.
  await buyer
    .getByTestId("chat-stream")
    .evaluate((el) => (el.scrollTop = el.scrollHeight / 2));
  const held = await scrollTop(buyer);
  all.push(conversation(link, 122)[121]!);
  const jump = pane.getByTestId("chat-jump");
  await expect(jump).toContainText("1 new message", { timeout: 10_000 });
  expect(Math.abs((await scrollTop(buyer)) - held)).toBeLessThan(3);
  await axeBoth(buyer, "the new messages button");
  await jump.click();
  expect(await fromBottom(buyer)).toBeLessThan(5);
  await expect(jump).toHaveCount(0);
});

test("the unread messages are marked, and the thread opens at them", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  await buyer.route(
    new RegExp(`/v1/link-chats/${link}/messages(\\?.*)?$`),
    serve(conversation(link, 120, 3)),
  );
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  // Theirs are 118, 119 and 120 less ours (119 is theirs: 119 % 3 = 2; 120 is ours).
  await expect(pane.getByTestId("chat-unread")).toContainText("unread message");
  await expect(pane.getByTestId("chat-unread")).toBeInViewport();
  await pageStill(buyer);
  await axeBoth(buyer, "the unread divider");
});

test("a quote goes to its message, and a search result opens at its message", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  const all = conversation(link, 120);
  // 120 quotes 3, which is far above the page that opens.
  all[119] = {
    ...all[119]!,
    reply: { pair_id: idOf(3), mine: true, kind: "text", body: "Message 3" },
  };
  await buyer.route(
    new RegExp(`/v1/link-chats/${link}/messages(\\?.*)?$`),
    serve(all),
  );
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await expect(pane.getByText("Message 120", { exact: true })).toBeVisible();
  await pane.getByTestId("chat-quote").click();
  const target = pane.getByText("Message 3", { exact: true });
  await expect(target).toBeInViewport();
  await pageStill(buyer);
  await expect(pane.locator('[data-msg="' + idOf(3) + '"]')).toHaveClass(
    /flash/,
  );
  // Away from the newest page: a button takes the reader back.
  const jump = pane.getByTestId("chat-jump");
  await expect(jump).toContainText("Jump to the latest");
  await axeBoth(buyer, "a message jumped to");
  await jump.click();
  await expect(pane.getByText("Message 120", { exact: true })).toBeVisible();
  await expect(jump).toHaveCount(0);

  // From a search result: /chat?with=…&msg=…
  await buyer.goto(`/chat?with=${link}&msg=${idOf(40)}`);
  await expect(
    buyer.getByTestId("chat-pane").getByText("Message 40", { exact: true }),
  ).toBeVisible();
  await expect(
    buyer.getByTestId("chat-pane").locator('[data-msg="' + idOf(40) + '"]'),
  ).toHaveClass(/flash/);
});
