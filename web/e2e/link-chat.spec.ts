import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// The conversation between linked businesses (business links L3, §5.5):
// Dangote and Ada are linked; Dangote writes from the API; Ada opens the
// conversation on her phone's width, reads it (Dangote sees the double
// tick), replies by quoting it, and archives the conversation; all of it
// passing axe in both themes, with nothing scrolling sideways.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@chat.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
}

test("two linked businesses talk", async ({ browser }) => {
  const stamp = letters();
  const sellerName = `Dangote Depot ${stamp}`;
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, sellerName);
  const buyer = await (await browser.newContext()).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  expect((await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } })).status()).toBe(200);
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`, { headers: H })).json();
  expect((await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } })).status()).toBe(201);
  const pending = await (await seller.request.get("/v1/links", { headers: H })).json();
  const link = pending.links[0].id as string;
  expect((await seller.request.post(`/v1/links/${link}/accept`, { headers: H })).status()).toBe(200);

  // Dangote writes.
  const sent = await seller.request.post(`/v1/link-chats/${link}/messages`, { headers: H, data: { text: "Good morning, rice is back in stock" } });
  expect(sent.status(), await sent.text()).toBe(201);

  // Ada's list: the conversation with its last message and one unread.
  await buyer.goto("/chat");
  const row = buyer.getByTestId("link-linked");
  await expect(row).toContainText(sellerName);
  await expect(row).toContainText("Good morning, rice is back in stock");
  await expect(row).toContainText("1");
  await axeBoth(buyer, "/chat with a conversation");

  // Opened: the bubble, read; Dangote's double tick.
  await row.click();
  const pane = buyer.getByTestId("chat-pane");
  await expect(pane.getByTestId("chat-message")).toContainText("Good morning, rice is back in stock");
  await expect(pane.getByTestId("chat-message")).toContainText("Dangote");
  await axeBoth(buyer, "an open conversation");
  await expect(async () => {
    const out = await (await seller.request.get(`/v1/link-chats/${link}/messages`, { headers: H })).json();
    expect(out.messages[0].read_at).toBeTruthy();
  }).toPass({ timeout: 10_000 });

  // A reply quoting it, sent with Enter.
  await pane.getByRole("button", { name: /^Reply/ }).click();
  await expect(pane.getByTestId("chat-replying")).toContainText("Good morning");
  await pane.getByLabel("Message").fill("Send 20 bags, please");
  await pane.getByLabel("Message").press("Enter");
  const mine = pane.locator('[data-testid="chat-message"][data-mine="true"]');
  await expect(mine).toContainText("Send 20 bags, please");
  await expect(mine.getByTestId("chat-quote")).toContainText("Good morning");
  await expect(mine.getByRole("img", { name: "Delivered" })).toBeVisible();
  const theirs = await (await seller.request.get(`/v1/link-chats/${link}/messages`, { headers: H })).json();
  expect(theirs.messages[1].body).toBe("Send 20 bags, please");
  expect(theirs.messages[1].reply.mine).toBe(true);

  // Dangote's next message arrives while it is open.
  await seller.request.post(`/v1/link-chats/${link}/messages`, { headers: H, data: { text: "On its way tomorrow" } });
  await expect(pane.getByTestId("chat-message").last()).toContainText("On its way tomorrow", { timeout: 10_000 });

  // Search in the conversation; archive it.
  await pane.getByTestId("chat-search").fill("bags");
  await expect(pane.getByTestId("chat-message")).toHaveCount(1);
  await pane.getByTestId("chat-search").fill("");
  await pane.getByRole("button", { name: "Archive" }).click();
  await pane.getByRole("button", { name: "Back to the chats" }).click();
  await expect(buyer.getByRole("button", { name: "Archived (1)" })).toBeVisible();
  await expect(buyer.getByTestId("link-linked")).toHaveCount(0);
});
