import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// The composer, as WhatsApp Web's: a message shows at once with a clock and
// is retried without ever being sent twice (instant send); and the rest of
// what a WhatsApp user does at the bottom of the screen.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

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

/** Two linked businesses; Ada (the buyer) is the page under test. */
async function linked(browser: Browser) {
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
  return { seller, buyer, link };
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

const messagesOf = async (page: Page, link: string) =>
  (
    (
      await (
        await page.request.get(`/v1/link-chats/${link}/messages`, {
          headers: H,
        })
      ).json()
    ).messages as { body: string }[]
  ).map((m) => m.body);

test("a message shows at once with a clock, then the server's copy takes its place", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  let release: () => void = () => undefined;
  const gate = new Promise<void>((r) => (release = r));
  await buyer.route(`**/v1/link-chats/${link}/messages`, async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    await gate; // the connection is slow
    await route.continue();
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  await pane.getByLabel("Message").fill("Ten bags of rice");
  await pane.getByLabel("Message").press("Enter");
  // At once: in the thread, with a clock, and the box already empty.
  const mine = pane.locator('[data-testid="chat-message"][data-mine="true"]');
  await expect(mine).toContainText("Ten bags of rice");
  await expect(mine.getByRole("img", { name: "Sending" })).toBeVisible();
  await expect(pane.getByLabel("Message")).toHaveValue("");
  await axeBoth(buyer, "a message being sent");
  release();
  await expect(mine.getByRole("img", { name: "Delivered" })).toBeVisible();
  await expect(mine).toHaveCount(1);
  expect(await messagesOf(buyer, link)).toEqual(["Ten bags of rice"]);
});

test("a message that cannot be sent says so, and Retry never sends it twice", async ({
  browser,
}) => {
  const { buyer, link } = await linked(browser);
  const ids: string[] = [];
  let mode: "offline" | "lost" | "ok" = "offline";
  await buyer.route(`**/v1/link-chats/${link}/messages`, async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    ids.push(JSON.parse(route.request().postData() ?? "{}").client_id);
    if (mode === "offline") return route.abort("connectionfailed");
    if (mode === "lost") {
      await route.fetch(); // it reaches the server, and the answer is lost
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const mine = pane.locator('[data-testid="chat-message"][data-mine="true"]');

  // Offline: Not sent, with Retry and Delete; the server has nothing.
  await pane.getByLabel("Message").fill("Send 20 bags");
  await pane.getByLabel("Message").press("Enter");
  const failed = pane.getByTestId("chat-failed");
  await expect(failed).toContainText("Not sent");
  await expect(failed).toContainText("no connection");
  await axeBoth(buyer, "a message that was not sent");
  expect(await messagesOf(buyer, link)).toEqual([]);

  // The request got there but the answer did not: Retry (the same send)
  // finds it at the server, and there is one message, not two.
  mode = "lost";
  await failed.getByRole("button", { name: "Retry" }).click();
  await expect(failed).toContainText("Not sent");
  mode = "ok";
  await failed.getByRole("button", { name: "Retry" }).click();
  await expect(mine.getByRole("img", { name: "Delivered" })).toBeVisible();
  await expect(mine).toHaveCount(1);
  await expect(failed).toHaveCount(0);
  expect(await messagesOf(buyer, link)).toEqual(["Send 20 bags"]);
  expect(new Set(ids).size).toBe(1); // every try carried the same id
  expect(ids.length).toBe(3);

  // Delete gives a failed message up.
  mode = "offline";
  await pane.getByLabel("Message").fill("Never mind");
  await pane.getByLabel("Message").press("Enter");
  await expect(failed).toContainText("Not sent");
  await failed.getByRole("button", { name: "Delete" }).click();
  await expect(failed).toHaveCount(0);
  await expect(pane.getByText("Never mind")).toHaveCount(0);
  expect(await messagesOf(buyer, link)).toEqual(["Send 20 bags"]);
});
