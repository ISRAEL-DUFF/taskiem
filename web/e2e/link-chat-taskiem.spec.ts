import {
  expect,
  test,
  type Browser,
  type Page,
  type Route,
} from "@playwright/test";
import { axe, describe } from "./a11y";

// Keyboard and desktop polish (Chat, phase 7): shortcuts that stay off while
// typing, arrows through the list, Esc closing the panel and then the chat,
// right click on a bubble, focus going back to the message box, and words
// for the unread count and the ticks.
// The Taskiem conversation is the same chat as a business's (phase 8): the
// same scrolling, composer and look, with its own content: reply buttons,
// the channel a message came from, ticks, "typing…" while an answer is made,
// and an unread cue.
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

const person = async (browser: Browser) => {
  const page = await (await browser.newContext()).newPage();
  await signUp(page, `Chioma Cakes ${letters()}`);
  return page;
};
const pane = (p: Page) => p.getByTestId("taskiem-pane");
const theirs = (p: Page) =>
  pane(p).locator('[data-testid="taskiem-message"][data-mine="false"]');
const ours = (p: Page) =>
  pane(p).locator('[data-testid="taskiem-message"][data-mine="true"]');

test("Taskiem's buttons are WhatsApp's reply buttons under the bubble, the pressed one marked, and our messages have ticks", async ({
  browser,
}) => {
  const page = await person(browser);
  await page.goto("/chat?with=taskiem");
  await pane(page).locator("#taskiem-text").fill("cash in 5,000 till");
  await pane(page).locator("#taskiem-text").press("Enter");
  const asking = theirs(page).last();
  await expect(asking).toContainText("₦5,000");
  const replies = asking.getByRole("group", { name: "Replies" });
  await expect(replies.getByRole("button")).toHaveCount(2);
  await expect(
    ours(page).first().getByRole("img", { name: "Read" }),
  ).toBeVisible();
  await axeBoth(page, "reply buttons");
  await replies.getByRole("button", { name: "Yes" }).click();
  await expect(theirs(page).last()).toContainText("Recorded");
  // The old answer keeps its buttons, the one chosen marked, none pressable.
  const old = theirs(page).first().getByRole("group", { name: "Replies" });
  await expect(old.getByRole("button")).toHaveCount(0);
  await expect(old).toContainText("You chose Yes");
  await axeBoth(page, "reply buttons, answered");
});

test("while an answer is being made there is a clock on the message and Taskiem is typing…", async ({
  browser,
}) => {
  const page = await person(browser);
  await page.goto("/chat?with=taskiem");
  // The answer is held until the clock and the typing have been seen: a
  // fixed wait ran out first on a busy machine.
  let answer = () => {};
  const held = new Promise<void>((r) => (answer = r));
  await page.route("**/v1/taskiem-chat", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    await held;
    return route.continue();
  });
  await pane(page).locator("#taskiem-text").fill("status");
  await pane(page).locator("#taskiem-text").press("Enter");
  await expect(pane(page).getByTestId("taskiem-typing")).toBeVisible();
  await expect(ours(page).getByRole("img", { name: "Sending" })).toBeVisible();
  answer();
  await expect(theirs(page).last()).toBeVisible({ timeout: 8000 });
  await expect(pane(page).getByTestId("taskiem-typing")).toHaveCount(0);
  await expect(ours(page)).toHaveCount(1); // not twice
  await expect(ours(page).getByRole("img", { name: "Read" })).toBeVisible();
});

test("a message that did not go comes back to the box", async ({ browser }) => {
  const page = await person(browser);
  await page.goto("/chat?with=taskiem");
  await page.route("**/v1/taskiem-chat", (route) =>
    route.request().method() === "POST"
      ? route.fulfill({
          status: 500,
          contentType: "application/json",
          body: '{"error":"nope"}',
        })
      : route.continue(),
  );
  await pane(page).locator("#taskiem-text").fill("status");
  await pane(page).locator("#taskiem-text").press("Enter");
  await expect(pane(page).locator("#taskiem-text")).toHaveValue("status");
  await expect(ours(page)).toHaveCount(0);
});

const at = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
const serve = (all: object[]) => (route: Route) => {
  if (route.request().method() !== "GET") return route.continue();
  const after = new URL(route.request().url()).searchParams.get("after");
  const list = after
    ? all.filter((m) => (m as { created_at: string }).created_at > after)
    : all;
  return route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ messages: list }),
  });
};

test("a message that came from WhatsApp is marked, and what Taskiem said while you were away is counted and shown from where it began", async ({
  browser,
}) => {
  const page = await person(browser);
  const m = (
    id: string,
    inbound: boolean,
    text: string,
    min: number,
    channel = "whatsapp",
  ) => ({
    id,
    channel,
    inbound,
    kind: "text",
    text,
    created_at: at(min),
  });
  const all = [
    m("11111111-0000-4000-8000-000000000001", true, "status", 30),
    m(
      "11111111-0000-4000-8000-000000000002",
      false,
      "No runs in the last 24 hours.",
      30,
    ),
    m(
      "11111111-0000-4000-8000-000000000003",
      false,
      "Cash in: ₦5,000 recorded.",
      3,
    ),
    m(
      "11111111-0000-4000-8000-000000000004",
      false,
      "Your daily summary is ready.",
      2,
    ),
  ];
  await page
    .context()
    .addInitScript(
      (seen) => localStorage.setItem("taskiem-chat-seen", seen),
      at(10),
    );
  await page.route(/\/v1\/taskiem-chat(\?.*)?$/, serve(all));
  await page.route("**/v1/chat/stream", (route) => route.abort());
  await page.goto("/chat");
  const row = page.getByTestId("taskiem-chat");
  await expect(row).toContainText("2 unread messages");
  await expect(page).toHaveTitle(/^\(2\)/);
  expect(await page.locator("a.chat-row").first().getAttribute("href")).toBe(
    "/chat?with=taskiem",
  ); // pinned at the top
  await row.click();
  await expect(pane(page).getByTestId("chat-unread")).toHaveText(
    "2 unread messages",
  );
  await expect(pane(page).getByTestId("chat-unread")).toBeInViewport();
  await expect(pane(page).getByTestId("taskiem-channel").first()).toContainText(
    "on WhatsApp",
  );
  // Seen now: the count is gone, in the list and the tab.
  await expect(page).not.toHaveTitle(/^\(\d/);
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("taskiem-chat")).not.toContainText("unread");
});

test("the composer is the business chat's: a draft kept, emoji, a photo (no document), and Esc one layer at a time", async ({
  browser,
}) => {
  const page = await person(browser);
  await page.goto("/chat?with=taskiem");
  const box = pane(page).locator("#taskiem-text");
  await box.fill("half a thought");
  await page
    .getByRole("link", { name: /Taskiem/ })
    .first()
    .click()
    .catch(() => undefined);
  await page.goto("/chat");
  await expect(page.getByTestId("taskiem-chat")).toContainText(
    "Draft: half a thought",
  );
  await page.getByTestId("taskiem-chat").click();
  await expect(box).toHaveValue("half a thought");
  await pane(page).getByTestId("emoji-button").click();
  await expect(pane(page).getByTestId("emoji-panel")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(pane(page).getByTestId("emoji-panel")).toHaveCount(0);
  await expect(page).toHaveURL(/with=taskiem/);
  await pane(page).getByTestId("attach").click();
  await expect(pane(page).getByRole("button", { name: "Photo", exact: true })).toBeVisible();
  await expect(
    pane(page).getByRole("button", { name: "Document" }),
  ).toHaveCount(0);
  await page.keyboard.press("Escape");
  await box.fill("");
});

test("the Taskiem conversation has the same menu: the keys are listed from it", async ({
  browser,
}) => {
  const page = await person(browser);
  await page.goto("/chat?with=taskiem");
  await pane(page)
    .getByRole("button", { name: "More about this conversation" })
    .click();
  await pane(page).getByRole("button", { name: "Keyboard" }).click();
  await expect(page.getByRole("dialog", { name: "Keyboard" })).toBeVisible();
});

const phoneOf = async (browser: Browser) => {
  const page = await (
    await browser.newContext({
      viewport: { width: 390, height: 844 },
      hasTouch: true,
      isMobile: true,
      permissions: ["camera"],
    })
  ).newPage();
  await signUp(page, `Chioma Cakes ${letters()}`);
  return page;
};

test("Taskiem's bubbles have the menu a business's have: right click, Copy, Esc", async ({
  browser,
}) => {
  const page = await (
    await browser.newContext({
      permissions: ["clipboard-read", "clipboard-write"],
    })
  ).newPage();
  await signUp(page, `Chioma Cakes ${letters()}`);
  await page.goto("/chat?with=taskiem");
  await pane(page).locator("#taskiem-text").fill("status");
  await pane(page).locator("#taskiem-text").press("Enter");
  await expect(theirs(page).last()).toBeVisible();
  const b = ours(page).first();
  await b.click({ button: "right" });
  const menu = b.getByTestId("chat-more");
  await expect(menu).toBeVisible();
  await menu.getByRole("button", { name: "Copy" }).click();
  await expect(menu.getByRole("button", { name: "Copied" })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    "status",
  );
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
  await expect(page).toHaveURL(/with=taskiem/);
  await expect(b.getByRole("button", { name: /More for/ })).toBeFocused();
});

test("a message on its way when the page goes away is not lost: its words come back to the box", async ({
  browser,
}) => {
  const page = await person(browser);
  await page.goto("/chat?with=taskiem");
  await page.route("**/v1/taskiem-chat", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    await new Promise(() => undefined); // held: never sent, never answered
  });
  await pane(page).locator("#taskiem-text").fill("cash in 7,000 till");
  await pane(page).locator("#taskiem-text").press("Enter");
  await expect(ours(page)).toHaveCount(1);
  await page.reload();
  await page.waitForTimeout(1500);
  await expect(pane(page).locator("#taskiem-text")).toHaveValue(
    "cash in 7,000 till",
  );
  await expect(page.getByText(/may not have been sent/)).toBeVisible();
  await expect(ours(page)).toHaveCount(0);
});

test("a message the server has, when the page was away, is not put back", async ({
  browser,
}) => {
  const page = await person(browser);
  await page.goto("/chat?with=taskiem");
  // The server gets it; the page goes away before the answer.
  await page.route("**/v1/taskiem-chat", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    await route.fetch(); // the server has it
    await new Promise(() => undefined); // and the answer is never given
  });
  await pane(page).locator("#taskiem-text").fill("status");
  await pane(page).locator("#taskiem-text").press("Enter");
  await expect(ours(page)).toHaveCount(1);
  // Wait until the server really has it (not a fixed pause: a slow runner
  // may need longer, and a message it does not have is rightly put back).
  await expect
    .poll(async () => {
      const r = await page.request.get("/v1/taskiem-chat");
      const j = (await r.json()) as { messages?: { text?: string }[] };
      return (j.messages ?? []).some((m) => m.text === "status");
    })
    .toBe(true);
  await page.reload();
  await expect(ours(page)).toHaveCount(1);
  await expect(pane(page).locator("#taskiem-text")).toHaveValue("");
  await expect(page.getByText(/may not have been sent/)).toHaveCount(0);
});

test("a message sent before the history has come is not taken for the last page's, nor put back", async ({
  browser,
}) => {
  const page = await person(browser);
  // The history is asked for as the page opens, and its answer (from before
  // the message) comes while the message is still waiting for its own: the
  // server has the message, its answer is held.
  let release = () => {};
  const held = new Promise<void>((r) => (release = r));
  let answer = () => {};
  const answered = new Promise<void>((r) => (answer = r));
  let first = true;
  await page.route("**/v1/taskiem-chat", async (route) => {
    const method = route.request().method();
    if (method === "POST") {
      const res = await route.fetch();
      await answered;
      return route.fulfill({ response: res });
    }
    if (method !== "GET" || !first) return route.continue();
    first = false;
    const res = await route.fetch();
    await held;
    await route.fulfill({ response: res });
  });
  await page.goto("/chat?with=taskiem");
  await pane(page).locator("#taskiem-text").fill("status");
  await pane(page).locator("#taskiem-text").press("Enter");
  await expect(pane(page).locator("#taskiem-text")).toHaveValue("");
  await expect
    .poll(async () => {
      const r = await page.request.get("/v1/taskiem-chat");
      const j = (await r.json()) as { messages?: { text?: string; inbound?: boolean }[] };
      return (j.messages ?? []).filter((m) => m.inbound && m.text === "status").length;
    })
    .toBe(1);
  const came = page.waitForResponse(
    (r) => r.request().method() === "GET" && new URL(r.url()).pathname === "/v1/taskiem-chat",
  );
  release();
  await came; // the history, from before the message, has come
  await page.waitForTimeout(300);
  await expect(page.getByText(/may not have been sent/)).toHaveCount(0);
  await expect(pane(page).locator("#taskiem-text")).toHaveValue("");
  answer();
  await expect(ours(page)).toHaveCount(1);
  // Nor kept as a draft for the next page.
  await page.reload();
  await expect(ours(page)).toHaveCount(1);
  await expect(pane(page).locator("#taskiem-text")).toHaveValue("");
});

test("the stored copy arriving by the stream before the answer does not show the message twice", async ({
  browser,
}) => {
  const page = await person(browser);
  await page.goto("/chat?with=taskiem");
  await page.waitForTimeout(800);
  await page.route("**/v1/taskiem-chat", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    const response = await route.fetch(); // the server has it now
    await new Promise((r) => setTimeout(r, 3000)); // the answer comes late
    await route.fulfill({ response });
  });
  await pane(page).locator("#taskiem-text").fill("status");
  await pane(page).locator("#taskiem-text").press("Enter");
  // By the time the stream has told of it, there is one, and never two.
  const counts: number[] = [];
  for (let i = 0; i < 7; i++) {
    await page.waitForTimeout(500);
    counts.push(await ours(page).count());
  }
  expect(Math.max(...counts)).toBe(1);
  await expect(theirs(page).last()).toBeVisible({ timeout: 8000 });
  await expect(ours(page)).toHaveCount(1);
});

test("what Taskiem says from another tab while you are elsewhere is counted once, even after a reload, and seeing it clears it in the other tab", async ({
  browser,
}) => {
  const page = await person(browser);
  const req = page.waitForRequest("**/v1/chat/stream");
  await page.goto("/chat"); // the list: Taskiem's conversation is not in front
  await req;
  await page.waitForTimeout(800);
  const other = await page.context().newPage();
  await other.request.post("/v1/taskiem-chat", {
    headers: H,
    data: { text: "status" },
  });
  await expect(page).toHaveTitle(/^\(1\)/, { timeout: 8000 });
  // A reload: the count is read again, and the stream's look-back tells of
  // the same message: still one.
  await page.reload();
  await page.waitForTimeout(3500);
  await expect(page).toHaveTitle(/^\(1\)/);
  // The other tab opens the conversation and sees it: this tab follows.
  await other.goto("/chat?with=taskiem");
  await expect(theirs(other).last()).toBeVisible();
  await other.bringToFront();
  await other.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(page).not.toHaveTitle(/^\(\d/, { timeout: 8000 });
});

test("on a phone: the Taskiem conversation fits at 390 and 360, passes axe in both themes, and the camera is beside the microphone", async ({
  browser,
}) => {
  const page = await phoneOf(browser);
  await page.goto("/chat?with=taskiem");
  await pane(page).locator("#taskiem-text").fill("cash in 5,000 till");
  await pane(page).locator("#taskiem-text").press("Enter");
  await expect(theirs(page).last()).toContainText("₦5,000");
  for (const width of [390, 360]) {
    await page.setViewportSize({ width, height: 800 });
    await page.waitForTimeout(300);
    const wide = await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth,
    );
    expect(wide, `sideways scroll at ${width}`).toBe(false);
    await axeBoth(page, `the Taskiem conversation at ${width}`);
  }
  await theirs(page).last().getByRole("button", { name: "Yes" }).click();
  await expect(theirs(page).last()).toContainText("Recorded");
  await axeBoth(page, "the Taskiem conversation, answered, on a phone");
  const quick = pane(page).getByTestId("camera-quick");
  await expect(quick).toBeVisible();
  const chooser = page.waitForEvent("filechooser");
  await quick.click();
  await chooser; // the camera app, through the file input
});

test("on a computer the clip's Camera opens the webcam for Taskiem too", async ({
  browser,
}) => {
  const page = await (
    await browser.newContext({ permissions: ["camera"] })
  ).newPage();
  await signUp(page, `Chioma Cakes ${letters()}`);
  await page.goto("/chat?with=taskiem");
  await pane(page).getByTestId("attach").click();
  await pane(page).getByRole("button", { name: "Camera" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("a question not yet answered can still be answered after a reload, and from another tab; an answered one cannot", async ({
  browser,
}) => {
  const page = await person(browser);
  await page.goto("/chat?with=taskiem");
  await pane(page).locator("#taskiem-text").fill("cash in 5,000 till");
  await pane(page).locator("#taskiem-text").press("Enter");
  await expect(theirs(page).last()).toContainText("₦5,000");

  // Reloaded: its buttons are still there to press.
  await page.reload();
  const asking = theirs(page).last();
  await expect(asking).toContainText("₦5,000");
  const yes = asking.getByRole("button", { name: "Yes", exact: true });
  await expect(yes).toBeVisible();
  await axeBoth(page, "an unanswered question after a reload");
  await yes.click();
  await expect(theirs(page).last()).toContainText("Recorded");
  // Answered: none of its buttons press, now or after another reload.
  await expect(
    pane(page).getByRole("button", { name: "Yes", exact: true }),
  ).toHaveCount(0);
  await page.reload();
  await expect(theirs(page).last()).toContainText("Recorded");
  await expect(
    pane(page).getByRole("button", { name: "Yes", exact: true }),
  ).toHaveCount(0);

  // Asked from another tab (the same person): this tab can answer it.
  const other = await page.context().newPage();
  await other.request.post("/v1/taskiem-chat", {
    headers: H,
    data: { text: "cash in 6,000 till" },
  });
  const second = pane(page)
    .locator('[data-testid="taskiem-message"]', { hasText: "₦6,000" })
    .last();
  await expect(second).toBeVisible({ timeout: 8000 });
  await second.getByRole("button", { name: "Yes", exact: true }).click();
  await expect(theirs(page).last()).toContainText("₦6,000");
  await expect(theirs(page).last()).toContainText("Recorded");
});

test("moving on from a question (saying something else) ends its buttons", async ({
  browser,
}) => {
  const page = await person(browser);
  await page.goto("/chat?with=taskiem");
  await pane(page).locator("#taskiem-text").fill("cash in 5,000 till");
  await pane(page).locator("#taskiem-text").press("Enter");
  await expect(theirs(page).last()).toContainText("₦5,000");
  await pane(page).locator("#taskiem-text").fill("status");
  await pane(page).locator("#taskiem-text").press("Enter");
  await expect(theirs(page).last()).not.toContainText("₦5,000");
  await expect(
    pane(page)
      .locator('[data-testid="taskiem-message"]', { hasText: "₦5,000" })
      .getByRole("button", { name: "Yes", exact: true }),
  ).toHaveCount(0);
});
