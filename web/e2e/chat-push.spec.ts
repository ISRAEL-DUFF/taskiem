import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Chat when Taskiem is closed (docs/spec/chat-push.md, step 2): the switch and
// the device list on the Notifications page, the iPhone note, the blocked
// words, signing out, and the service worker showing what is pushed. The
// browser's own push service is not reached from a test machine, so
// `PushManager` is a stand-in that hands out a subscription for an address on
// a push service's host (the server keeps it as it would a real one); the
// worker and the page are the real ones.
// serviceWorkers: the config blocks them for every other spec.
test.use({ bypassCSP: true, viewport: { width: 1280, height: 900 }, serviceWorkers: "allow" });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const PASSWORD = "correct horse battery";

async function signUp(page: Page) {
  const email = `${letters()}-${Math.random().toString(36).slice(2, 8)}@push.test`;
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Ada Kitchen ${letters()}`,
      email,
      name: "Ada Owner",
      password: PASSWORD,
    },
  });
  expect(r.status(), await r.text()).toBe(201);
  return email;
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

/** The browser's push, standing in: one subscription kept in this browser's storage, as a real one is. */
const STAND_IN = () => {
  const KEY = "e2e-push-sub";
  const b64 = (bytes: number[]) =>
    btoa(String.fromCharCode(...bytes))
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");
  const read = () => {
    try {
      return JSON.parse(localStorage.getItem(KEY) ?? "null") as {
        endpoint: string;
        keys: { p256dh: string; auth: string };
      } | null;
    } catch {
      return null;
    }
  };
  const wrap = (j: {
    endpoint: string;
    keys: { p256dh: string; auth: string };
  }) => ({
    endpoint: j.endpoint,
    expirationTime: null,
    options: {},
    toJSON: () => j,
    unsubscribe: async () => {
      localStorage.removeItem(KEY);
      return true;
    },
  });
  PushManager.prototype.getSubscription = async () => {
    const j = read();
    return j ? (wrap(j) as unknown as PushSubscription) : null;
  };
  PushManager.prototype.subscribe = async () => {
    let j = read();
    if (!j) {
      j = {
        endpoint: `https://fcm.googleapis.com/fcm/send/e2e-${Math.random().toString(36).slice(2)}`,
        keys: {
          p256dh: b64([
            4,
            ...Array.from({ length: 64 }, (_, i) => (i * 7 + 3) % 256),
          ]),
          auth: b64(Array.from({ length: 16 }, (_, i) => i + 1)),
        },
      };
      localStorage.setItem(KEY, JSON.stringify(j));
    }
    return wrap(j) as unknown as PushSubscription;
  };
};

/** The browser's notification permission, standing in: a test machine's browser does not always grant it (the chat tests of this app stub it too). */
const GRANTED = () => {
  Object.defineProperty(Notification, "permission", {
    get: () => "granted",
    configurable: true,
  });
  Notification.requestPermission = async () => "granted";
};

async function owner(
  browser: Browser,
  opts: Parameters<Browser["newContext"]>[0] = {},
) {
  const ctx = await browser.newContext({
    permissions: ["notifications"],
    ...opts,
  });
  await ctx.addInitScript(STAND_IN);
  await ctx.addInitScript(GRANTED);
  const page = await ctx.newPage();
  const email = await signUp(page);
  return { ctx, page, email };
}

const devices = async (page: Page) =>
  (
    (await (await page.request.get("/v1/me/push", { headers: H })).json()) as {
      devices: { id: string; label: string; preview: boolean }[];
    }
  ).devices;

test("the switch turns notifications on for this device, with its own switch for the words, and off again; other devices are listed and can be removed; signing out takes this one off", async ({
  browser,
}) => {
  const { ctx, page, email } = await owner(browser);
  await page.goto("/settings/notifications");
  const section = page.getByTestId("chat-push");
  await expect(section).toBeVisible();
  const sw = section.getByTestId("chat-push-switch");
  await expect(sw).not.toBeChecked();
  await expect(section.getByTestId("chat-push-preview")).toHaveCount(0);

  await sw.click();
  await expect(sw).toBeChecked();
  const mine = await devices(page);
  expect(mine).toHaveLength(1);
  expect(mine[0]?.label).toMatch(/^Chrome on /);
  expect(mine[0]?.preview).toBe(true);

  // The words: its own switch, kept on the server.
  const preview = section.getByTestId("chat-push-preview");
  await expect(preview).toBeChecked();
  await preview.click();
  await expect(section).toContainText(
    "Off: the notification says only that a business wrote.",
  );
  await expect.poll(async () => (await devices(page))[0]?.preview).toBe(false);

  // It is still on after a reload (the browser's subscription and the id kept).
  await page.reload();
  await expect(section.getByTestId("chat-push-switch")).toBeChecked();
  await expect(section.getByTestId("chat-push-preview")).not.toBeChecked();
  await axeBoth(page, "the notifications page with chat push on");

  // Another device, made by the person on another browser: listed, and removed here.
  const other = await page.request.put("/v1/me/push", {
    headers: H,
    data: {
      endpoint:
        "https://updates.push.services.mozilla.com/wpush/v2/other-device",
      keys: { p256dh: "BAAA", auth: "AAAA" },
      label: "Firefox on Linux",
    },
  });
  expect(other.ok()).toBe(true);
  await page.reload();
  const row = section.getByTestId("chat-push-device");
  await expect(row).toHaveCount(1);
  await expect(row).toContainText("Firefox on Linux");
  await expect(row).toContainText("Not notified yet");
  await row.getByRole("button", { name: /^Remove/ }).click();
  await expect(section.getByTestId("chat-push-device")).toHaveCount(0);
  expect(await devices(page)).toHaveLength(1);

  // Off: the server forgets this browser, and so does the browser.
  await section.getByTestId("chat-push-switch").click();
  await expect(section.getByTestId("chat-push-switch")).not.toBeChecked();
  expect(await devices(page)).toHaveLength(0);
  expect(
    await page.evaluate(() => localStorage.getItem("e2e-push-sub")),
  ).toBeNull();

  // On again, then signing out: this browser is taken off before the session ends.
  await section.getByTestId("chat-push-switch").click();
  await expect(section.getByTestId("chat-push-switch")).toBeChecked();
  expect(await devices(page)).toHaveLength(1);
  await page.getByRole("button", { name: "Sign out" }).first().click();
  await expect(
    page.getByRole("button", { name: "Sign in", exact: true }),
  ).toBeVisible();
  const again = await page.request.post("/v1/auth/login", {
    headers: H,
    data: { email, password: PASSWORD },
  });
  expect(again.ok(), await again.text()).toBe(true);
  expect(await devices(page)).toHaveLength(0);
  await ctx.close();
});

test("on an iPhone it says to add Taskiem to the Home Screen first, and offers the switch once it is", async ({
  browser,
}) => {
  const UA =
    "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1";
  const first = await owner(browser, {
    userAgent: UA,
    viewport: { width: 390, height: 800 },
  });
  await first.page.goto("/settings/notifications");
  const section = first.page.getByTestId("chat-push");
  await expect(section.getByTestId("chat-push-ios")).toContainText(
    "add Taskiem to your Home Screen first",
  );
  await expect(section.getByTestId("chat-push-switch")).toHaveCount(0);
  await axeBoth(first.page, "the iPhone note");
  await first.ctx.close();

  // Added to the Home Screen (the page then says it is standalone).
  const installed = await browser.newContext({
    permissions: ["notifications"],
    userAgent: UA,
    viewport: { width: 390, height: 800 },
  });
  await installed.addInitScript(STAND_IN);
  await installed.addInitScript(GRANTED);
  await installed.addInitScript(() =>
    Object.defineProperty(navigator, "standalone", { value: true }),
  );
  const page = await installed.newPage();
  await signUp(page);
  await page.goto("/settings/notifications");
  await expect(page.getByTestId("chat-push-ios")).toHaveCount(0);
  await expect(page.getByTestId("chat-push-switch")).toBeVisible();
  await installed.close();
});

test("a browser that blocked notifications says so, and the switch waits; on a phone it fits", async ({
  browser,
}) => {
  const ctx = await browser.newContext({
    viewport: { width: 360, height: 740 },
  });
  await ctx.addInitScript(STAND_IN);
  await ctx.addInitScript(() =>
    Object.defineProperty(Notification, "permission", { get: () => "denied" }),
  );
  const page = await ctx.newPage();
  await signUp(page);
  await page.goto("/settings/notifications");
  const section = page.getByTestId("chat-push");
  await expect(section).toContainText(
    "Notifications are blocked for Taskiem in this browser",
  );
  await expect(section.getByTestId("chat-push-switch")).toBeDisabled();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth + 1,
    ),
  ).toBe(false);
  await axeBoth(page, "the blocked words on a phone");
  await ctx.close();
});

test("the worker shows what is pushed: one notification a conversation, only a place in the app to open; nothing while a window of the app is in front", async ({
  browser,
}) => {
  const { ctx, page } = await owner(browser);
  await page.goto("/settings/notifications");
  await page.getByTestId("chat-push-switch").click();
  await expect(page.getByTestId("chat-push-switch")).toBeChecked();
  await page.evaluate(() => navigator.serviceWorker.ready);
  // The worker may register before or after this line: wait for it to be there,
  // not for an event that may already have happened.
  const isWorker = (w: { url(): string }) => w.url().endsWith("/sw.js");
  await expect.poll(() => ctx.serviceWorkers().some(isWorker)).toBe(true);
  const worker = ctx.serviceWorkers().find(isWorker);
  if (!worker) throw new Error("the worker is not there");

  const cdp = await ctx.newCDPSession(page);
  const regs: { registrationId: string; scopeURL: string }[] = [];
  cdp.on("ServiceWorker.workerRegistrationUpdated", (e) =>
    regs.push(...(e as { registrations: typeof regs }).registrations),
  );
  await cdp.send("ServiceWorker.enable");
  await expect.poll(() => regs.length).toBeGreaterThan(0);
  const registrationId = regs[regs.length - 1]?.registrationId ?? "";
  const origin = new URL(page.url()).origin;
  const push = (data: unknown) =>
    cdp.send("ServiceWorker.deliverPushMessage", {
      origin,
      registrationId,
      data: JSON.stringify(data),
    });
  type Shown = {
    title: string;
    body: string;
    tag: string;
    link: string;
    icon: string;
  };
  // What the worker asks the browser to show, recorded (the browser replaces a
  // notification that has the same tag, as the stand-in does). The browser's own
  // notifications are not used: a test machine does not always have them.
  await worker.evaluate(() => {
    const g = self as unknown as {
      __shown: Record<string, unknown>[];
      registration: { showNotification: unknown };
    };
    g.__shown = [];
    g.registration.showNotification = (
      title: string,
      o: { body: string; tag: string; icon: string; data: { link: string } },
    ) => {
      g.__shown = g.__shown.filter((n) => n.tag !== o.tag);
      g.__shown.push({
        title,
        body: o.body,
        tag: o.tag,
        link: o.data.link,
        icon: o.icon,
      });
      return Promise.resolve();
    };
  });
  const shown = (): Promise<Shown[]> =>
    worker.evaluate(() => (self as unknown as { __shown: Shown[] }).__shown);

  // With a window of the app in front, as the worker sees it, the page has the
  // message and nothing is shown. (Whether a test browser reports its window as
  // focused is its own business: the rule itself is run by lib/push.test.ts.)
  const inFront = await worker.evaluate(async () => {
    const list = await (
      self as unknown as {
        clients: {
          matchAll(
            o: unknown,
          ): Promise<{ visibilityState: string; focused: boolean }[]>;
        };
      }
    ).clients.matchAll({ type: "window", includeUncontrolled: true });
    return list.some((w) => w.visibilityState === "visible" && w.focused);
  });
  await push({
    v: 1,
    title: "Dangote Stores",
    body: "Musa: Rice is 45k",
    link: "/chat?with=abc",
    tag: "abc",
    unread: 1,
  });
  await page.waitForTimeout(1500); // long enough for a loaded machine to have handled it
  if (inFront) expect(await shown()).toEqual([]);
  else await expect.poll(async () => (await shown()).length).toBe(1);
  await worker.evaluate(() => {
    (self as unknown as { __shown: unknown[] }).__shown = [];
  });

  // With nothing of the app in front it is shown.
  const away = await ctx.newPage();
  await away.goto("about:blank");
  await away.bringToFront();
  await page.evaluate(() => (window.location.href = "about:blank"));
  await page.waitForTimeout(300);
  await push({
    v: 1,
    title: "Dangote Stores",
    body: "Musa: Rice is 45k",
    link: "/chat?with=abc",
    tag: "abc",
    unread: 1,
  });
  await expect.poll(shown).toEqual([
    {
      title: "Dangote Stores",
      body: "Musa: Rice is 45k",
      tag: "abc",
      link: "/chat?with=abc",
      icon: expect.stringMatching(/\/brand\/relay\/icon-192\.png$/), // no logo: the app's own
    },
  ]);
  // The next for the same conversation replaces it; another conversation is its own.
  await push({
    v: 1,
    title: "Dangote Stores",
    body: "Musa: And 46k for two",
    link: "/chat?with=abc",
    tag: "abc",
    unread: 1,
  });
  await push({
    v: 1,
    title: "Cherry Blocks",
    body: "Hello",
    link: "/chat?with=def",
    tag: "def",
    unread: 2,
  });
  await expect
    .poll(async () =>
      (await shown()).map((n: Shown) => `${n.tag}:${n.body}`).sort(),
    )
    .toEqual(["abc:Musa: And 46k for two", "def:Hello"]);
  // A link outside the app, and what cannot be read, are only the chat page / a plain message.
  await push({
    v: 1,
    title: "Evil",
    body: "Click",
    link: "https://evil.example/",
    tag: "evil",
    icon: "https://evil.example/logo.png",
  });
  await expect
    .poll(
      async () => (await shown()).find((n: Shown) => n.tag === "evil")?.link,
    )
    .toBe("/chat");
  // The icon is the app's own: an address outside the app is never fetched,
  // and a sender's logo that the server does not have (a 404) is the same.
  const evil = (await shown()).find((n: Shown) => n.tag === "evil");
  expect(evil?.icon).toMatch(/\/brand\/relay\/icon-192\.png$/);
  await push({
    v: 1,
    title: "Dangote Stores",
    body: "Hello",
    link: "/chat?with=abc",
    tag: "logo",
    icon: "/v1/links/0192e2e0-0000-7000-8000-000000000001/logo?size=320",
  });
  await expect
    .poll(
      async () => (await shown()).find((n: Shown) => n.tag === "logo")?.icon,
    )
    .toMatch(/\/brand\/relay\/icon-192\.png$/);
  await ctx.close();
});

test("the app's icon shows how many conversations have something unread, and none once they are read", async ({
  browser,
}) => {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  const ownerCtx = await browser.newContext();
  const buyer = await ownerCtx.newPage();
  // A device with a badge: what the page asks of it is kept to be seen.
  await ownerCtx.addInitScript(() => {
    const calls: string[] = [];
    (window as unknown as { __badge: string[] }).__badge = calls;
    Object.defineProperty(navigator, "setAppBadge", {
      value: async (n?: number) => void calls.push(`set ${n}`),
    });
    Object.defineProperty(navigator, "clearAppBadge", {
      value: async () => void calls.push("clear"),
    });
  });
  const mk = async (page: Page, tenant: string) => {
    const r = await page.request.post("/v1/signup", {
      headers: H,
      data: {
        tenant,
        email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@push.test`,
        name: "Owner",
        password: PASSWORD,
      },
    });
    expect(r.status(), await r.text()).toBe(201);
  };
  await mk(seller, `Dangote Depot ${stamp}`);
  await mk(buyer, `Ada Kitchen ${stamp}`);
  await seller.request.put("/v1/link-settings", {
    headers: H,
    data: { findable: true },
  });
  const sellerId = (
    (await (await seller.request.get("/v1/me")).json()) as { tenant_id: string }
  ).tenant_id;
  expect(
    (
      await buyer.request.post("/v1/links", {
        headers: H,
        data: { business: sellerId, role: "both" },
      })
    ).ok(),
  ).toBe(true);
  const asked = (await (
    await seller.request.get("/v1/links", { headers: H })
  ).json()) as { links: { id: string }[] };
  expect(
    (
      await seller.request.post(`/v1/links/${asked.links[0]?.id}/accept`, {
        headers: H,
      })
    ).ok(),
  ).toBe(true);
  const link = (
    (await (
      await buyer.request.get("/v1/link-chats", { headers: H })
    ).json()) as { conversations: { link: string }[] }
  ).conversations[0]?.link;

  await buyer.goto("/chat");
  const badge = () =>
    buyer.evaluate(() =>
      (window as unknown as { __badge: string[] }).__badge.at(-1),
    );
  await expect.poll(badge).toBe("clear"); // nothing unread yet
  expect(
    (
      await seller.request.post(`/v1/link-chats/${link}/messages`, {
        headers: H,
        data: { text: "Rice is 45k" },
      })
    ).status(),
  ).toBe(201);
  await expect.poll(badge, { timeout: 15000 }).toBe("set 1");
  // Opening the conversation reads it: the count comes off.
  await buyer.goto(`/chat?with=${link}`);
  await expect(buyer.getByTestId("chat-message")).toHaveCount(1);
  await expect.poll(badge, { timeout: 15000 }).toBe("clear");
  await ownerCtx.close();
});
