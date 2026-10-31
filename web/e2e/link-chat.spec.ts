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

test("a bill's card, its query, and the query settled by taking an amount off", async ({ browser }) => {
  const stamp = letters();
  const sellerName = `Cherry Blocks ${stamp}`;
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, sellerName);
  const buyer = await (await browser.newContext()).newPage();
  await signUp(buyer, `Bola Builders ${stamp}`);
  expect((await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } })).status()).toBe(200);
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`cherry blocks ${stamp}`)}`, { headers: H })).json();
  expect((await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } })).status()).toBe(201);
  const link = (await (await seller.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  expect((await seller.request.post(`/v1/links/${link}/accept`, { headers: H })).status()).toBe(200);
  const buyerID = (await (await buyer.request.get("/v1/me", { headers: H })).json()).tenant_id as string;

  // Cherry invoices Bola as a linked business: a card in both conversations.
  const inv = await seller.request.post("/v1/invoices", {
    headers: { ...H, "Idempotency-Key": `inv-${stamp}` },
    data: { customer: { name: "Bola Builders", key: `link:${buyerID}` }, lines: [{ description: "Blocks", quantity: 100, unit_kobo: 45000 }] },
  });
  expect(inv.status(), await inv.text()).toBe(201);
  const x = await inv.json();
  expect((await seller.request.post(`/v1/invoices/${x.id}/send`, { headers: { ...H, "Idempotency-Key": `send-${stamp}` } })).status()).toBe(200);
  await buyer.goto(`/chat?with=${link}`);
  const card = buyer.getByTestId("chat-card");
  await expect(card).toContainText(`${sellerName} sent invoice ${x.number}`, { timeout: 20_000 });
  await expect(card.getByRole("link", { name: `Pay ${x.number}` })).toBeVisible();
  await axeBoth(buyer, "a bill's card");

  // Bola queries it; on Cherry's invoice, the query is settled with ₦5,000 off.
  expect((await buyer.request.post(`/v1/bills/${x.number}/query`, { headers: H, data: { reason: "Ten blocks broke" } })).status()).toBe(200);
  await seller.goto(`/invoices/${x.id}`);
  const q = seller.getByTestId("invoice-query");
  await expect(q).toBeVisible();
  await q.getByLabel("Take an amount off").check();
  await q.getByLabel("Amount off (₦)").fill("5,000");
  await q.getByLabel("Why").fill("Ten blocks broke");
  await axeBoth(seller, "settling a query");
  await q.getByRole("button", { name: "Settle the query" }).click();
  await expect(q).toHaveCount(0);
  await expect(seller.getByTestId("doc-thread")).toContainText("Took ₦5,000 off: Ten blocks broke", { timeout: 15_000 });
  await axeBoth(seller, "an invoice's messages");
});

test("the Taskiem conversation on the web", async ({ browser }) => {
  const page = await (await browser.newContext()).newPage();
  await signUp(page, `Chioma Cakes ${letters()}`);
  await page.goto("/chat");
  await page.getByTestId("taskiem-chat").click();
  const pane = page.getByTestId("taskiem-pane");
  await expect(pane.getByRole("heading", { name: "Taskiem" })).toBeVisible();
  await axeBoth(page, "the Taskiem conversation, empty");

  // The words of the phone: a question with buttons, answered by a press.
  await pane.getByLabel("Message to Taskiem").fill("cash in 5,000 till");
  await pane.getByLabel("Message to Taskiem").press("Enter");
  const theirs = pane.locator('[data-testid="taskiem-message"][data-mine="false"]');
  await expect(theirs.last()).toContainText("₦5,000");
  await axeBoth(page, "Taskiem asking");
  await theirs.last().getByRole("button").first().click();
  await expect(theirs.last()).toContainText("Recorded");

  // Kept: the same conversation after a reload, its old buttons no longer pressable.
  await page.reload();
  await expect(page.getByTestId("taskiem-message")).toHaveCount(4);
  await expect(page.getByTestId("taskiem-pane").getByRole("button", { name: "Yes" })).toHaveCount(0);
  await axeBoth(page, "the Taskiem conversation's history");
});

test("pin, the typing line, forward and delete for me", async ({ browser }) => {
  const stamp = letters();
  const make = async (name: string) => {
    const p = await (await browser.newContext()).newPage();
    await signUp(p, name);
    return p;
  };
  const seller = await make(`Eko Rice ${stamp}`);
  const buyer = await make(`Femi Foods ${stamp}`);
  const third = await make(`Gbenga Gas ${stamp}`);
  const link = async (to: Page, name: string) => {
    expect((await to.request.put("/v1/link-settings", { headers: H, data: { findable: true } })).status()).toBe(200);
    const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(name.toLowerCase())}`, { headers: H })).json();
    expect((await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } })).status()).toBe(201);
    const id = (await (await to.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
    expect((await to.request.post(`/v1/links/${id}/accept`, { headers: H })).status()).toBe(200);
    return id;
  };
  const withSeller = await link(seller, `Eko Rice ${stamp}`);
  const withThird = await link(third, `Gbenga Gas ${stamp}`);
  const sent = await seller.request.post(`/v1/link-chats/${withSeller}/messages`, { headers: H, data: { text: "Rice is ₦45,000 a bag this week" } });
  expect(sent.status()).toBe(201);

  await buyer.goto(`/chat?with=${withSeller}`);
  const pane = buyer.getByTestId("chat-pane");
  await expect(pane.getByTestId("chat-message")).toContainText("Rice is ₦45,000");

  // The seller types: the buyer sees it.
  await expect(async () => {
    await seller.request.post(`/v1/link-chats/${withSeller}/typing`, { headers: H });
    await expect(pane.getByTestId("chat-typing")).toBeVisible({ timeout: 4_000 });
  }).toPass({ timeout: 20_000 });

  // Pinned.
  await pane.getByRole("button", { name: "Pin" }).click();
  await expect(pane.getByRole("button", { name: "Unpin" })).toBeVisible();

  // Forwarded to Gbenga.
  const msg = pane.getByTestId("chat-message").first();
  await msg.getByRole("button", { name: /^More/ }).click();
  await axeBoth(buyer, "a message's options");
  await msg.getByRole("button", { name: "Forward" }).click();
  const sheet = buyer.getByRole("dialog");
  await sheet.getByLabel(`Gbenga Gas ${stamp}`).check();
  await axeBoth(buyer, "forwarding a message");
  await sheet.getByRole("button", { name: "Forward" }).click();
  await expect(sheet).toHaveCount(0);
  const theirs = await (await third.request.get(`/v1/link-chats/${withThird}/messages`, { headers: H })).json();
  expect(theirs.messages[0].body).toBe("Rice is ₦45,000 a bag this week");
  expect(theirs.messages[0].forwarded).toBe(true);

  // Deleted for the buyer; the seller keeps it.
  buyer.once("dialog", (d) => void d.accept());
  await msg.getByRole("button", { name: /^More/ }).click();
  await msg.getByRole("button", { name: "Delete for me" }).click();
  await expect(pane.getByTestId("chat-message")).toHaveCount(0);
  const kept = await (await seller.request.get(`/v1/link-chats/${withSeller}/messages`, { headers: H })).json();
  expect(kept.messages).toHaveLength(1);
  await axeBoth(buyer, "after delete for me");
});
