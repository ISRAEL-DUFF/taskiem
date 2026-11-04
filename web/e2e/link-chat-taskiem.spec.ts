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
  await page.route("**/v1/taskiem-chat", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    await new Promise((r) => setTimeout(r, 1500));
    return route.continue();
  });
  await pane(page).locator("#taskiem-text").fill("status");
  await pane(page).locator("#taskiem-text").press("Enter");
  await expect(pane(page).getByTestId("taskiem-typing")).toBeVisible();
  await expect(ours(page).getByRole("img", { name: "Sending" })).toBeVisible();
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
  await expect(pane(page).getByRole("button", { name: "Photo" })).toBeVisible();
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
