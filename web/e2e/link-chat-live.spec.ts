import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Live delivery and notifications (business links, L3): a message reaches the
// other side in a second or two over the live stream, a tick turns blue when
// it is read, the tab says how many are unread, a message that comes while
// the tab is in the background is a notification (once chosen, never for a
// muted chat or the one in front) and a ping, and with the stream blocked the
// page polls as before.
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
  // Each step says what it got: a search that misses, a request refused
  // or a list without the link fails here with the answer, not later as
  // "cannot read 'id'" (7 November 2026, under parallel workers).
  const name = `Dangote Depot ${stamp}`;
  let hit: { business: string } | undefined;
  await expect
    .poll(
      async () => {
        const r = await buyer.request.get(
          `/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`,
          { headers: H },
        );
        expect(r.status(), await r.text()).toBe(200);
        const found = (await r.json()) as { results?: { business: string; name: string }[] };
        hit = (found.results ?? []).find((c) => c.name === name);
        return hit?.business;
      },
      { message: `the search finds ${name}`, timeout: 10_000 },
    )
    .toBeTruthy();
  if (!hit) throw new Error(`the search did not find ${name}`);
  const asked = await buyer.request.post("/v1/links", {
    headers: H,
    data: { business: hit.business, role: "supplier" },
  });
  expect(asked.status(), await asked.text()).toBe(201);
  const listed = await seller.request.get("/v1/links", { headers: H });
  expect(listed.status(), await listed.text()).toBe(200);
  const links = ((await listed.json()) as { links: { id: string }[] }).links;
  expect(links, "the request reached the seller").toHaveLength(1);
  const first = links[0];
  if (!first) throw new Error("the request did not reach the seller");
  const link = first.id;
  const accepted = await seller.request.post(`/v1/links/${link}/accept`, { headers: H });
  expect([200, 204], await accepted.text()).toContain(accepted.status());
  return { seller, buyer, link, name };
}

const say = (p: Page, link: string, text: string) =>
  p.request.post(`/v1/link-chats/${link}/messages`, {
    headers: H,
    data: { text },
  });
const bubble = (p: Page, text: string) =>
  p
    .getByTestId("chat-pane")
    .locator('[data-testid="chat-message"]', { hasText: text });

/** The page is open and its stream is up (the first thing the stream says is ready). */
async function streaming(page: Page, path: string) {
  const req = page.waitForRequest("**/v1/chat/stream");
  await page.goto(path);
  await req;
  await page.waitForTimeout(600);
}

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

/** A browser with notifications and sound that the test can see and the tab in front or hidden at will. */
async function stubBrowser(
  page: Page,
  permission: "granted" | "default" | "denied",
  prefs?: { browser: boolean; sound: boolean; asked: boolean },
) {
  await page.context().addInitScript(
    ({ permission, prefs }) => {
      const w = window as unknown as Record<string, unknown>;
      w.__notes = [] as {
        title: string;
        body?: string;
        tag?: string;
        onclick?: () => void;
      }[];
      w.__asked = 0;
      w.__pings = 0;
      w.__hidden = false;
      w.__blurred = false;
      w.__resumed = 0;
      class FakeNotification {
        static permission = permission;
        static requestPermission = async () => {
          (w.__asked as number)++;
          FakeNotification.permission = "granted";
          return "granted";
        };
        title: string;
        body?: string;
        tag?: string;
        onclick?: () => void;
        constructor(title: string, o?: { body?: string; tag?: string }) {
          this.title = title;
          this.body = o?.body;
          this.tag = o?.tag;
          (w.__notes as unknown[]).push(this);
        }
        close() {}
      }
      w.Notification = FakeNotification;
      Object.defineProperty(document, "visibilityState", {
        get: () => (w.__hidden ? "hidden" : "visible"),
      });
      Object.defineProperty(document, "hidden", { get: () => !!w.__hidden });
      document.hasFocus = () => !(w.__hidden || w.__blurred);
      w.AudioContext = class {
        currentTime = 0;
        destination = {};
        state = "suspended";
        resume() {
          (w.__resumed as number)++;
          this.state = "running";
          return Promise.resolve();
        }
        createOscillator() {
          (w.__pings as number)++;
          return {
            frequency: { value: 0 },
            connect: () => ({ connect: () => undefined }),
            start() {},
            stop() {},
          };
        }
        createGain() {
          return {
            gain: { setValueAtTime() {}, exponentialRampToValueAtTime() {} },
            connect: () => ({}),
          };
        }
        close() {}
      };
      if (prefs)
        localStorage.setItem("taskiem-chat-notify", JSON.stringify(prefs));
    },
    { permission, prefs },
  );
}
const hide = (p: Page, hidden: boolean) =>
  p.evaluate((h) => {
    (window as unknown as { __hidden: boolean }).__hidden = h;
    document.dispatchEvent(new Event("visibilitychange"));
  }, hidden);
const blur = (p: Page, blurred: boolean) =>
  p.evaluate((b) => {
    (window as unknown as { __blurred: boolean }).__blurred = b;
    window.dispatchEvent(new Event(b ? "blur" : "focus"));
  }, blurred);
const notes = (p: Page) =>
  p.evaluate(() =>
    (
      window as unknown as {
        __notes: { title: string; body?: string; tag?: string }[];
      }
    ).__notes.map((n) => ({ title: n.title, body: n.body, tag: n.tag })),
  );

test("a message reaches the other side in a second or two, and the tick turns blue as it is read", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await streaming(buyer, `/chat?with=${link}`);
  await streaming(seller, `/chat?with=${link}`);

  // Dangote writes (from the page); Ada's open conversation has it, fast.
  await seller
    .getByTestId("chat-pane")
    .getByLabel("Message")
    .fill("Rice is back in stock");
  const sent = Date.now();
  await seller.getByTestId("chat-pane").getByLabel("Message").press("Enter");
  await expect(bubble(buyer, "Rice is back")).toBeVisible({ timeout: 4000 });
  const took = Date.now() - sent;
  expect(took, `it took ${took} ms`).toBeLessThan(4000);

  // Ada has it open, so it is read; Dangote's tick turns blue without a reload.
  await expect(
    bubble(seller, "Rice is back").getByRole("img", { name: "Read" }),
  ).toBeVisible({ timeout: 3500 });

  // Typing shows on the other side, and the list follows.
  await seller.request.post(`/v1/link-chats/${link}/typing`, { headers: H });
  await expect(buyer.getByTestId("chat-typing")).toBeVisible({ timeout: 4000 });
  await expect(buyer.getByTestId("chat-typing")).toBeHidden({
    timeout: 10_000,
  });

  // A reaction arrives the same way.
  await bubble(buyer, "Rice is back")
    .getByRole("button", { name: /^More/ })
    .click();
  await buyer
    .getByTestId("chat-more")
    .getByRole("button", { name: "React with thumbs up" })
    .click();
  await expect(
    bubble(seller, "Rice is back").getByRole("button", { name: /^👍 1/ }),
  ).toBeVisible({ timeout: 3500 });
});

test("with the stream blocked the page polls as before", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await buyer.route("**/v1/chat/stream", (route) =>
    route.abort("connectionfailed"),
  );
  await buyer.goto(`/chat?with=${link}`);
  await expect(buyer.getByTestId("chat-pane")).toBeVisible();
  await say(seller, link, "Still gets through");
  await expect(bubble(buyer, "Still gets through")).toBeVisible({
    timeout: 9000,
  });
  // And the list follows too.
  await buyer.goto("/chat");
  await expect(buyer.getByTestId("link-linked")).toContainText(
    "Still gets through",
  );
});

test("the tab says how many messages are unread, wherever you are in the app", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await streaming(buyer, "/home");
  await expect(buyer).not.toHaveTitle(/^\(/);
  await say(seller, link, "One");
  await say(seller, link, "Two");
  await expect(buyer).toHaveTitle(/^\(2\) /, { timeout: 5000 });
  // Opening the conversation reads them: the count goes.
  await buyer.goto(`/chat?with=${link}`);
  await expect(bubble(buyer, "Two")).toBeVisible();
  await expect(buyer).not.toHaveTitle(/^\(/, { timeout: 5000 });
});

test("a message that comes while the tab is in the background is a notification, once chosen", async ({
  browser,
}) => {
  const { seller, buyer, link, name } = await linked(browser, {
    permissions: ["notifications"],
  });
  await stubBrowser(buyer, "granted", {
    browser: true,
    sound: false,
    asked: true,
  });
  await streaming(buyer, "/home");

  // In front: nothing.
  await say(seller, link, "While you are looking");
  await expect(buyer).toHaveTitle(/^\(1\) /, { timeout: 5000 });
  expect(await notes(buyer)).toEqual([]);

  // In the background: the business and the first line, one per message.
  await hide(buyer, true);
  await say(seller, link, "Ten bags of rice\nand a bag of beans");
  await expect
    .poll(() => notes(buyer), { timeout: 5000 })
    .toEqual([{ title: name, body: "Ten bags of rice", tag: link }]);

  // A muted chat is quiet.
  await buyer.request.put(`/v1/link-chats/${link}`, {
    headers: H,
    data: { muted: true },
  });
  await buyer.waitForTimeout(1500);
  await say(seller, link, "Muted, so quiet");
  await buyer.waitForTimeout(2500);
  expect((await notes(buyer)).length).toBe(1);
  await buyer.request.put(`/v1/link-chats/${link}`, {
    headers: H,
    data: { muted: false },
  });

  // Clicking it takes the person to the conversation.
  await buyer.evaluate(() =>
    (
      window as unknown as { __notes: { onclick?: () => void }[] }
    ).__notes[0]?.onclick?.(),
  );
  await expect(buyer).toHaveURL(new RegExp(`/chat\\?with=${link}`));
});

test("it is silent until chosen: no notification, no sound", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, {
    permissions: ["notifications"],
  });
  await stubBrowser(buyer, "granted"); // nothing chosen
  await streaming(buyer, "/home");
  await hide(buyer, true);
  await say(seller, link, "Hello");
  await expect(buyer).toHaveTitle(/^\(1\) /, { timeout: 5000 });
  expect(await notes(buyer)).toEqual([]);
  expect(
    await buyer.evaluate(
      () => (window as unknown as { __pings: number }).__pings,
    ),
  ).toBe(0);
});

test("the person is asked once, when they are back at the tab after a message came, never on load", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await stubBrowser(buyer, "default");
  await streaming(buyer, "/home");
  const asked = () =>
    buyer.evaluate(() => (window as unknown as { __asked: number }).__asked);
  expect(await asked()).toBe(0);
  await expect(buyer.getByTestId("notify-ask")).toHaveCount(0);
  // A message while the tab is in front does not ask either.
  await say(seller, link, "In front");
  await expect(buyer).toHaveTitle(/^\(1\) /, { timeout: 5000 });
  await expect(buyer.getByTestId("notify-ask")).toHaveCount(0);
  // One while it is hidden: not asked then (no one is looking), but on return.
  await hide(buyer, true);
  await say(seller, link, "In the background");
  await buyer.waitForTimeout(2500);
  expect(await asked()).toBe(0);
  await hide(buyer, false);
  const banner = buyer.getByTestId("notify-ask");
  await expect(banner).toBeVisible();
  await axeBoth(buyer, "the question about notifications");
  expect(await asked()).toBe(0); // the browser is asked only when they say yes
  await banner.getByRole("button", { name: "Turn on notifications" }).click();
  await expect.poll(asked).toBe(1);
  await expect(banner).toHaveCount(0);
  expect(
    await buyer.evaluate(() =>
      JSON.parse(localStorage.getItem("taskiem-chat-notify") ?? "{}"),
    ),
  ).toMatchObject({ browser: true, asked: true });
});

test("Not now is remembered: the question does not come again", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await stubBrowser(buyer, "default");
  await streaming(buyer, "/home");
  await hide(buyer, true);
  await say(seller, link, "First");
  await buyer.waitForTimeout(2500);
  await hide(buyer, false);
  await buyer
    .getByTestId("notify-ask")
    .getByRole("button", { name: "Not now" })
    .click();
  await hide(buyer, true);
  await say(seller, link, "Second");
  await buyer.waitForTimeout(2500);
  await hide(buyer, false);
  await expect(buyer.getByTestId("notify-ask")).toHaveCount(0);
});

test("the sound is off until chosen on the settings page, and plays for a message in another page of the app", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser, {
    permissions: ["notifications"],
  });
  await stubBrowser(buyer, "granted");
  await buyer.goto("/settings/notifications");
  const settings = buyer.getByTestId("chat-notify-setting");
  await expect(
    settings.getByRole("checkbox", { name: /Play a short sound/ }),
  ).not.toBeChecked();
  await expect(
    settings.getByRole("checkbox", { name: /Show a notification/ }),
  ).not.toBeChecked();
  await axeBoth(buyer, "the chat notification settings");
  await settings.getByRole("checkbox", { name: /Play a short sound/ }).check();
  const pings = () =>
    buyer.evaluate(() => (window as unknown as { __pings: number }).__pings);
  expect(await pings()).toBe(2); // the two notes of the sample
  // A message while on this page, in front: the ping.
  await streaming(buyer, "/settings/notifications");
  expect(await pings()).toBe(0); // (the page was loaded afresh)
  await say(seller, link, "Ping");
  await expect.poll(pings, { timeout: 5000 }).toBe(2);
  // Turning on the notification asks the browser (the click is the person's).
  await settings.getByRole("checkbox", { name: /Show a notification/ }).check();
  await expect(
    settings.getByRole("checkbox", { name: /Show a notification/ }),
  ).toBeChecked();
});

test("a tab that is visible but not the window in use is away: it is told, and asked on return", async ({
  browser,
}) => {
  const { seller, buyer, link, name } = await linked(browser, {
    permissions: ["notifications"],
  });
  await stubBrowser(buyer, "granted", {
    browser: true,
    sound: false,
    asked: true,
  });
  await streaming(buyer, "/home");
  await blur(buyer, true); // visible, on another monitor, but not the window they are using
  await say(seller, link, "While you are elsewhere");
  await expect
    .poll(() => notes(buyer), { timeout: 5000 })
    .toEqual([{ title: name, body: "While you are elsewhere", tag: link }]);
  // In the window they are using, on the conversation: nothing.
  await blur(buyer, false);
  await buyer.goto(`/chat?with=${link}`);
  await expect(bubble(buyer, "While you are elsewhere")).toBeVisible();
  await say(seller, link, "While you look");
  await expect(bubble(buyer, "While you look")).toBeVisible({ timeout: 4000 });
  expect(await notes(buyer)).toEqual([]); // the page was reloaded: its own list starts empty
});

test("with several tabs open one tells, once, and a reload does not tell again", async ({
  browser,
}) => {
  const { seller, buyer, link, name } = await linked(browser, {
    permissions: ["notifications"],
  });
  await stubBrowser(buyer, "granted", {
    browser: true,
    sound: true,
    asked: true,
  });
  const second = await buyer.context().newPage();
  await streaming(buyer, "/home");
  await streaming(second, "/home");
  await hide(buyer, true);
  await hide(second, true);
  await say(seller, link, "One message, two tabs");
  await buyer.waitForTimeout(3500);
  const all = [...(await notes(buyer)), ...(await notes(second))];
  expect(all).toEqual([
    { title: name, body: "One message, two tabs", tag: link },
  ]);
  const pings = async (p: Page) =>
    p.evaluate(() => (window as unknown as { __pings: number }).__pings);
  expect((await pings(buyer)) + (await pings(second))).toBe(2); // one ping: its two notes

  // A reload within the stream's look-back replays the message: it is not told again.
  await buyer.reload();
  await hide(buyer, true);
  await buyer.waitForTimeout(2500);
  expect(await notes(buyer)).toEqual([]);
});

test("a stream that goes quiet is not trusted: the page opens another", async ({
  browser,
}) => {
  const { buyer } = await linked(browser);
  await buyer.context().addInitScript(() => {
    const w = window as unknown as Record<string, unknown>;
    w.__taskiemChatLive = { stallMs: 1500, checkMs: 300 };
    w.__es = 0;
    // A stream that says ready and then nothing (a proxy holding it).
    w.EventSource = class {
      static CLOSED = 2;
      readyState = 1;
      onerror: (() => void) | null = null;
      private on: Record<string, ((e: unknown) => void)[]> = {};
      constructor() {
        (w.__es as number)++;
        setTimeout(() => (this.on.ready ?? []).forEach((f) => f({})), 50);
      }
      addEventListener(t: string, f: (e: unknown) => void) {
        (this.on[t] ??= []).push(f);
      }
      close() {
        this.readyState = 2;
      }
    };
  });
  await buyer.goto("/home");
  const opened = () =>
    buyer.evaluate(() => (window as unknown as { __es: number }).__es);
  await expect.poll(opened, { timeout: 5000 }).toBeGreaterThanOrEqual(3); // silent: closed and opened again, and again
});

test("a stream that keeps pinging is kept", async ({ browser }) => {
  const { buyer } = await linked(browser);
  await buyer.context().addInitScript(() => {
    const w = window as unknown as Record<string, unknown>;
    w.__taskiemChatLive = { stallMs: 1500, checkMs: 300 };
    w.__es = 0;
    w.EventSource = class {
      static CLOSED = 2;
      readyState = 1;
      onerror: (() => void) | null = null;
      private on: Record<string, ((e: unknown) => void)[]> = {};
      constructor() {
        (w.__es as number)++;
        setTimeout(() => (this.on.ready ?? []).forEach((f) => f({})), 50);
        setInterval(() => (this.on.ping ?? []).forEach((f) => f({})), 400);
      }
      addEventListener(t: string, f: (e: unknown) => void) {
        (this.on[t] ??= []).push(f);
      }
      close() {
        this.readyState = 2;
      }
    };
  });
  await buyer.goto("/home");
  await buyer.waitForTimeout(4500);
  expect(
    await buyer.evaluate(() => (window as unknown as { __es: number }).__es),
  ).toBe(1);
});

test("a stream that ends is reopened, and the server's refusal is answered by polling and trying again", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await buyer.context().addInitScript(() => {
    (window as unknown as Record<string, unknown>).__taskiemChatLive = {
      retryMs: 800,
    };
  });
  const seen: (string | undefined)[] = [];
  let n = 0;
  await buyer.route("**/v1/chat/stream", async (route) => {
    seen.push((await route.request().allHeaders())["last-event-id"]);
    n += 1;
    if (n === 1)
      // the server ends the stream (as it does after half an hour), having sent one id
      return route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: "retry: 400\nid: 1700000000123456\nevent: ready\ndata: {}\n\n",
      });
    if (n === 2)
      return route.fulfill({
        status: 429,
        contentType: "application/json",
        headers: { "Retry-After": "30" },
        body: '{"error":"too many open chat streams"}',
      });
    return route.continue();
  });
  await buyer.goto(`/chat?with=${link}`);
  // 1: ended; 2: the page came back, and was refused; 3: tried again after the wait.
  await expect
    .poll(() => seen.length, { timeout: 8000 })
    .toBeGreaterThanOrEqual(3);
  // (The id the browser sends back is the browser's own; the server side of it is in the Go tests.)
  // Refused, it polled; connected again, it is live.
  await say(seller, link, "After the refusal");
  await expect(bubble(buyer, "After the refusal")).toBeVisible({
    timeout: 8000,
  });
});

test("the stream opens when the first business is linked, without a reload", async ({
  browser,
}) => {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const buyer = await (
    await browser.newContext({ viewport: { width: 1280, height: 800 } })
  ).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  await buyer.context().addInitScript(() => {
    (window as unknown as Record<string, unknown>).__taskiemChatLive = {
      listMs: 1000,
    };
  });
  let streams = 0;
  buyer.on("request", (r) => r.url().includes("/v1/chat/stream") && streams++);
  await buyer.goto("/home");
  await buyer.waitForTimeout(2500);
  expect(streams).toBe(0); // nothing to hear: no stream
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
  await expect.poll(() => streams, { timeout: 6000 }).toBe(1);
});

test("the icon carries a dot while there is something unread, and goes when it is read", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await streaming(buyer, "/home");
  const icon = () =>
    buyer.evaluate(() => {
      const l = document.querySelector<HTMLLinkElement>("#icon-svg");
      return { href: l?.href ?? "", type: l?.type ?? "" };
    });
  const plain = await icon();
  expect(plain.type).toBe("image/svg+xml");
  await say(seller, link, "Look at the icon");
  await expect
    .poll(async () => (await icon()).href.startsWith("data:image/png"), {
      timeout: 6000,
    })
    .toBe(true);
  expect((await icon()).type).toBe("image/png");
  // Read: the icon is the app's again.
  await buyer.goto(`/chat?with=${link}`);
  await expect(bubble(buyer, "Look at the icon")).toBeVisible();
  await expect
    .poll(async () => (await icon()).href, { timeout: 6000 })
    .toBe(plain.href);
  expect((await icon()).type).toBe("image/svg+xml");
});

test("the person's own conversation with Taskiem follows the stream: what is said from another tab appears", async ({
  browser,
}) => {
  const { buyer } = await linked(browser);
  await streaming(buyer, "/chat?with=taskiem");
  const pane = buyer.getByTestId("taskiem-pane");
  // Said from elsewhere (another tab, another device, WhatsApp): the same user.
  await buyer.request.post("/v1/taskiem-chat", {
    headers: H,
    data: { text: "status" },
  });
  const said = pane.locator(".text", { hasText: /^status$/ });
  await expect(said).toBeVisible({ timeout: 4000 });
  await expect(
    pane.locator(".text", { hasText: /No runs in the last 24 hours/ }),
  ).toBeVisible({ timeout: 4000 });
  expect(await said.count()).toBe(1); // not twice
});

test("a burst of messages is read with one request, not one each", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await streaming(buyer, `/chat?with=${link}`);
  let reads = 0;
  buyer.on(
    "request",
    (r) =>
      r.url().endsWith(`/v1/link-chats/${link}/read`) &&
      r.method() === "POST" &&
      reads++,
  );
  for (let i = 1; i <= 8; i++) await say(seller, link, `Burst ${i}`);
  await expect(bubble(buyer, "Burst 8")).toBeVisible({ timeout: 5000 });
  await buyer.waitForTimeout(1500);
  expect(reads).toBeGreaterThan(0);
  expect(reads).toBeLessThanOrEqual(3);
});

test("the audio is unlocked by the person's first click, so a ping can be heard", async ({
  browser,
}) => {
  const { buyer } = await linked(browser);
  await stubBrowser(buyer, "granted", {
    browser: false,
    sound: true,
    asked: true,
  });
  await buyer.goto("/home");
  const resumed = () =>
    buyer.evaluate(
      () => (window as unknown as { __resumed: number }).__resumed,
    );
  expect(await resumed()).toBe(0);
  // The first click counts once the page has started: wait for it to show.
  await expect(buyer.locator("#main")).toBeVisible();
  await expect(buyer.getByRole("heading", { level: 1 }).first()).toBeVisible();
  await buyer.mouse.click(600, 400);
  await expect.poll(resumed).toBeGreaterThan(0);
});

test("a browser that has blocked notifications is told so, in words", async ({
  browser,
}) => {
  const { buyer } = await linked(browser);
  await stubBrowser(buyer, "denied");
  await buyer.goto("/settings/notifications");
  const settings = buyer.getByTestId("chat-notify-setting");
  await expect(settings).toContainText("blocked notifications");
  await expect(
    settings.getByRole("checkbox", { name: /Show a notification/ }),
  ).toBeDisabled();
});
