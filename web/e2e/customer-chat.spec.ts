import { createServer as createHttp, type Server as HttpServer } from "node:http";
import { expect, test, type Page, type Request, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Taskiem for everyone, stage 2, the web (docs/spec/taskiem-for-everyone.md
// §8, §9, §12–§15): a business's chat link (/m/{code}), the browser chat
// started from it by phone, a person's chats on You with their tools and
// cards, and the business's side: the thread marked in the app, offers,
// blocks, and the chat link's settings. Codes go out on Taskiem's WhatsApp
// number to a fake Graph API on the port whatsapp.spec.ts and you.spec.ts
// use, so this file runs one test at a time and apart from them.
test.use({ bypassCSP: true });
test.describe.configure({ mode: "serial" });

const H = { "X-Taskiem-Request": "1" };
const graphPort = 12626;
const sent: { to: string; template?: { components?: { parameters?: { text?: string }[] }[] } }[] = [];
let graph: HttpServer;

test.beforeAll(async () => {
  graph = createHttp((req, res) => {
    let body = "";
    req.on("data", (c: Buffer) => (body += c.toString("utf8")));
    req.on("end", () => {
      res.writeHead(200, { "Content-Type": "application/json" });
      if (req.method === "POST" && req.url?.endsWith("/messages")) {
        const m = JSON.parse(body) as (typeof sent)[number];
        sent.push(m);
        res.end(JSON.stringify({ messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: m.to.replace(/^\+/, "") }], messages: [{ id: `wamid.cc${sent.length}` }] }));
        return;
      }
      res.end("{}");
    });
  });
  await new Promise<void>((resolve) => graph.listen(graphPort, "127.0.0.1", resolve));
});

test.afterAll(async () => {
  await new Promise<void>((resolve) => graph.close(() => resolve()));
});

const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

function aNumber(): { typed: string; e164: string } {
  const rest = String(Date.now()).slice(-7) + String(Math.floor(Math.random() * 10));
  const digits = `90${rest.slice(0, 8)}`;
  return { typed: `0${digits.slice(0, 3)} ${digits.slice(3, 6)} ${digits.slice(6)}`, e164: `+234${digits}` };
}

async function codeFor(e164: string, after = 0): Promise<string> {
  let code = "";
  await expect
    .poll(() => {
      const m = sent.slice(after).filter((x) => x.to === e164 || x.to === e164.slice(1)).pop();
      code = m?.template?.components?.flatMap((c) => c.parameters ?? []).find((p) => /^\d{6}$/.test(p.text ?? ""))?.text ?? "";
      return code;
    }, { message: `a code to ${e164}` })
    .toMatch(/^\d{6}$/);
  return code;
}

/** axe in the light and the dark theme, and nothing wider than the page. */
async function check(page: Page, where: string, width: number) {
  const found: string[] = [];
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await page.waitForTimeout(150);
    const vs = await axe(page);
    if (vs.length) found.push(describe(`${where} (${scheme})`, vs));
  }
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  expect(found, found.join("\n\n")).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
}

async function owner(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@cc.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

test("a customer opens a chat link, signs in by phone in the browser, and chats with the business, who answers in the app", async ({ browser }) => {
  test.setTimeout(180_000);
  const biz = await (await browser.newContext({ viewport: { width: 1280, height: 900 } })).newPage();
  const name = `Mama Kitchen ${letters()}`;
  await owner(biz, name);
  const link = (await (await biz.request.get("/v1/customers/chat-link", { headers: H })).json()) as { code: string; url: string; words: string };
  expect(link.words).toBe(`Chat with ${name} on Taskiem: ${link.url}`);

  // The link's page, signed out, on a phone.
  const person = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await person.goto(`/m/${link.code}`);
  await expect(person.getByRole("heading", { name })).toBeVisible();
  await expect(person.getByTestId("chat-here")).toHaveText("Chat here in your browser");
  await check(person, "/m/{code}", 390);
  await person.screenshot({ path: "test-results/chat-link-page.png" });

  // Chat here: the phone way in, with the link (the business pays for the code).
  let started: unknown;
  person.on("request", (r: Request) => {
    if (r.url().endsWith("/v1/auth/phone/start")) started = r.postDataJSON();
  });
  await person.getByTestId("chat-here").click();
  await expect(person.getByRole("heading", { name: `Chat with ${name}` })).toBeVisible();
  await expect(person.getByTestId("shared-phone")).toContainText("Is this your phone?");
  await check(person, "the way in from a chat link", 390);
  const n = aNumber();
  const before = sent.length;
  await person.getByLabel("Your phone number").fill(n.typed);
  await person.getByRole("button", { name: "Continue" }).click();
  await expect(person.getByRole("heading", { name: "Enter the code" })).toBeVisible();
  expect(started).toMatchObject({ phone: n.e164, chat_code: link.code });
  await person.getByLabel("6-digit code").fill(await codeFor(n.e164, before));
  await person.getByRole("button", { name: "Continue" }).click();
  await expect(person.getByRole("heading", { name: "Make your account" })).toBeVisible();
  await person.getByLabel("Your name").fill("Tolu Bello");
  await person.getByLabel("I'm 18 or over").check();
  await person.getByTestId("way-in-terms").getByRole("checkbox").check();
  await person.getByRole("button", { name: "Create" }).click();

  // The conversation opens, saying what the business sees, and asks about offers once.
  await expect(person).toHaveURL(/\/you\/chats\/[0-9a-f-]{36}$/);
  await expect(person.getByTestId("person-notice")).toContainText(`${name} will see your name and phone number`);
  await expect(person.getByTestId("offers-question")).toContainText(`Get offers from ${name}?`);
  await check(person, "the browser chat", 390);
  await person.getByTestId("offers-question").getByRole("button", { name: "Yes" }).click();
  await expect(person.getByTestId("offers-question")).toHaveCount(0);
  await person.getByLabel(`Message ${name}`).fill("Is the jollof ready?");
  await person.getByLabel(`Message ${name}`).press("Enter");
  await expect(person.getByTestId("person-message").filter({ hasText: "Is the jollof ready?" })).toBeVisible();
  await expect(person.getByTestId("person-message").filter({ hasText: "Is the jollof ready?" }).getByRole("img", { name: "Delivered" })).toBeVisible();

  // The business: the thread is in the app, with no 24-hour window.
  const chats = (await (await biz.request.get("/v1/customer-chats", { headers: H })).json()) as { chats: { id: string; line: string; name: string }[] };
  const thread = chats.chats.find((c) => c.line === "app");
  expect(thread?.name).toBe("Tolu Bello");
  await biz.goto(`/chat?with=c-${thread?.id}`);
  const pane = biz.getByTestId("customer-pane");
  await expect(pane.getByTestId("customer-message").filter({ hasText: "Is the jollof ready?" })).toBeVisible();
  // They were typing a moment ago; then they are a customer in the app.
  await expect(pane.getByText("Customer in the app")).toBeVisible({ timeout: 20_000 });
  await expect(biz.getByTestId("in-the-app")).toBeVisible();
  await expect(pane.getByTestId("customer-note")).toHaveCount(0);
  await pane.getByLabel("Reply to Tolu Bello").fill("Yes, come at 4");
  await pane.getByLabel("Reply to Tolu Bello").press("Enter");
  await expect(pane.getByTestId("customer-message").filter({ hasText: "Yes, come at 4" })).toBeVisible();
  // They said yes to offers: this one goes as an offer.
  await pane.getByLabel("Reply to Tolu Bello").fill("Free drink with every jollof today");
  await pane.getByTestId("customer-as-offer").check();
  await pane.getByLabel("Reply to Tolu Bello").press("Enter");
  await expect(pane.getByTestId("customer-message").filter({ hasText: "Free drink" }).getByText("Offer")).toBeVisible();

  // The person: the answer and the offer, with Stop offers under it.
  await expect(person.getByTestId("person-message").filter({ hasText: "Yes, come at 4" })).toBeVisible({ timeout: 15_000 });
  const offer = person.getByTestId("person-message").filter({ hasText: "Free drink" });
  await expect(offer.getByText("Offer", { exact: true })).toBeVisible({ timeout: 15_000 });
  await offer.getByRole("button", { name: `Stop offers from ${name}` }).click();
  await expect(offer.getByRole("button", { name: `Stop offers from ${name}` })).toHaveCount(0);
  // Read: the business's reply shows read.
  await expect(pane.getByTestId("customer-message").filter({ hasText: "Yes, come at 4" }).getByRole("img", { name: "Read" })).toBeVisible({ timeout: 15_000 });

  // Block: the business sees the customer has stopped messages; nothing is deleted.
  await person.getByRole("button", { name: "More about this conversation" }).click();
  await person.getByTestId("person-block").click();
  await person.getByTestId("confirm-ok").click();
  await expect(person.getByTestId("person-closed")).toContainText(`You blocked ${name}`);
  await biz.reload();
  await expect(biz.getByTestId("customer-note")).toContainText("This customer has stopped messages");
  await expect(biz.getByTestId("customer-message").filter({ hasText: "Is the jollof ready?" })).toBeVisible();
  await person.getByRole("button", { name: "More about this conversation" }).click();
  await person.getByTestId("person-block").click();
  await expect(person.getByTestId("person-closed")).toHaveCount(0);

  // Report, with consent to send the last 20 messages.
  await person.getByRole("button", { name: "More about this conversation" }).click();
  await person.getByRole("menuitem", { name: "Report" }).click();
  const report = person.getByTestId("report-sheet");
  await report.getByLabel("Messages I don't want").check();
  await expect(report.getByTestId("report-send")).toBeDisabled();
  await report.getByRole("checkbox").check();
  await check(person, "the report sheet", 390);
  await report.getByTestId("report-send").click();
  await expect(report.getByRole("status")).toContainText("Taskiem's moderators will look at this conversation");
  await person.keyboard.press("Escape");

  // You: the chat in the list, at 360 px too.
  await person.setViewportSize({ width: 360, height: 780 });
  await person.goto("/you");
  await expect(person.getByTestId("person-chat").filter({ hasText: name })).toBeVisible();
  await check(person, "/you with a chat", 360);

  // Sign out from the chat's menu (a shared phone).
  await person.getByTestId("person-chat").filter({ hasText: name }).click();
  await person.getByRole("button", { name: "More about this conversation" }).click();
  await person.getByTestId("person-sign-out").click();
  await person.getByTestId("confirm-ok").click();
  await expect(person).toHaveURL(/\/login/);
});

test("the business's chat link: settings, a new link, the old one no longer works, and an offer to customers", async ({ browser }) => {
  test.setTimeout(120_000);
  for (const width of [390, 360]) {
    const biz = await (await browser.newContext({ viewport: { width, height: 844 } })).newPage();
    await owner(biz, `Bola Stores ${letters()}`);
    await biz.goto("/settings");
    await biz.getByTestId("settings-customer-chats").getByRole("link", { name: "Open customer chats" }).click();
    await expect(biz.getByRole("heading", { name: "Customer chats", level: 1 })).toBeVisible();
    const words = await biz.getByTestId("chat-link-words").inputValue();
    expect(words).toMatch(/^Chat with Bola Stores .* on Taskiem: http.*\/m\/[a-z0-9]{10}$/);
    await expect(biz.getByTestId("chat-link-qr")).toBeVisible();
    await check(biz, "/settings/customers", width);
    await biz.screenshot({ path: `test-results/customer-settings-${width}.png`, fullPage: true });
    if (width === 360) continue;
    const old = (await biz.getByTestId("chat-link-url").textContent()) ?? "";

    // Take chats off: the link's page says so.
    await biz.getByTestId("takes-chats").uncheck();
    await expect.poll(async () => ((await (await biz.request.get("/v1/customers/chat-link", { headers: H })).json()) as { settings: { takes_chats: boolean } }).settings.takes_chats).toBe(false);
    const anon = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
    await anon.goto(new URL(old).pathname);
    await expect(anon.getByTestId("not-taking-chats")).toBeVisible();
    await biz.getByTestId("takes-chats").check();

    // The closed reply's own words.
    await biz.getByTestId("closed-words").fill("We are closed. We open at 8.");
    await biz.getByRole("button", { name: "Save the words" }).click();
    await expect.poll(async () => ((await (await biz.request.get("/v1/settings/customer-chats", { headers: H })).json()) as { closed_words: string }).closed_words).toBe("We are closed. We open at 8.");

    // Make a new link: the old one no longer works.
    await biz.getByTestId("new-chat-link").click();
    await biz.getByTestId("confirm-ok").click();
    await expect(biz.getByTestId("chat-link-url")).not.toHaveText(old);
    // A new browser: the card a minute ago may be kept by the last one (max-age 60).
    const later = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
    await later.goto(new URL(old).pathname);
    await expect(later.getByRole("heading", { name: "This link no longer works" })).toBeVisible();
    await check(later, "a link that no longer works", 390);

    // An offer with nobody who said yes: said plainly.
    await biz.getByTestId("offer-text").fill("Two for one on Saturday");
    await biz.getByTestId("send-offer").getByRole("button", { name: "Send to customers" }).click();
    await biz.getByTestId("confirm-ok").click();
    await expect(biz.getByTestId("offer-sent")).toHaveText("No customer has said yes to your offers yet.");
  }
});

// The person's side drawn from stand-ins for the server, so every tool and
// card is reached without a shop of products, services and pay links.
const PERSON = {
  tenant_id: null,
  personal: true,
  roles: [],
  permissions: [],
  auth_method: "phone",
  user: { id: "0192e2e0-0000-7000-8000-00000000c001", email: "p@phone.taskiem.invalid", email_placeholder: true, name: "Ada Obi", phone: "+2348031234567" },
  businesses: [],
};

function world() {
  const now = Date.now();
  const at = (min: number) => new Date(now - min * 60_000).toISOString();
  const chat = {
    id: "c1",
    business: "b1",
    name: "Mama's Kitchen",
    last: "We have jollof",
    last_mine: false,
    last_at: at(1),
    unread: 1,
    muted: false,
    archived: false,
    blocked: false,
    blocked_you: false,
    offers: "unasked",
    ask_offers: true,
    typing: false,
    hours: { open: true, line: "Open now, closes at 18:00" },
  };
  const messages = [
    { id: "m1", mine: true, kind: "text", body: "Hello", read_at: at(9), created_at: at(10) },
    { id: "m2", mine: false, kind: "text", body: "We have jollof", sender_name: "Ngozi", read_at: null, created_at: at(9) },
    { id: "m3", mine: false, kind: "card", body: "Jollof rice", card: { type: "product", item_id: "i1", title: "Jollof rice", line: "Party jollof", total_kobo: 250_000 }, read_at: null, created_at: at(8) },
    { id: "m4", mine: false, kind: "card", body: "Book Catering tasting", card: { type: "booking", service_id: "s1", title: "Book Catering tasting", minutes: 30, total_kobo: 500_000 }, read_at: null, created_at: at(7) },
    { id: "m5", mine: false, kind: "card", body: "Pay for MK-1", card: { type: "pay", reference: "MK-1", title: "Pay for MK-1", total_kobo: 300_000, url: "https://pay.example.test/MK-1" }, read_at: null, created_at: at(6) },
    { id: "m6", mine: false, kind: "card", body: "Order MK-1", card: { type: "order", reference: "MK-1", title: "Order MK-1", line: "1 × Jollof rice", status: "awaiting_payment", total_kobo: 300_000 }, read_at: null, created_at: at(5) },
    { id: "m7", mine: false, kind: "media", body: "Menu", attachment: { kind: "file", type: "application/pdf", size: 120_000, name: "Menu.pdf" }, read_at: null, created_at: at(4) },
    { id: "m8", mine: false, kind: "text", body: "Free drink with every jollof today", offer: true, read_at: null, created_at: at(3) },
  ];
  const calls: { method: string; path: string; body: unknown; key?: string }[] = [];
  const shop = {
    items: [
      { id: "i1", name: "Jollof rice", price_kobo: 250_000, available: true, few_left: 3 },
      {
        id: "i2",
        name: "Ankara shirt",
        price_kobo: 800_000,
        available: true,
        sizes: [
          { id: "i2a", variant: { colour: "Blue", size: "M" }, price_kobo: 800_000, available: true, few_left: 2 },
          { id: "i2b", variant: { colour: "Red", size: "L" }, price_kobo: 800_000, available: false },
        ],
      },
    ],
    open: true,
    pickup: true,
    delivery: true,
    zones: [{ name: "Ikeja", fee_kobo: 100_000 }],
    payment: "at_order",
  };
  const order = { reference: "MK-1", kind: "order", status: "awaiting_payment", summary: "1 × Jollof rice", total_kobo: 300_000, due_kobo: 300_000, pay: true, created_at: at(5) };
  const handle = async (route: Route) => {
    const r = route.request();
    const path = new URL(r.url()).pathname;
    const method = r.method();
    const body = r.postData() ? (r.headers()["content-type"]?.includes("json") ? r.postDataJSON() : "form") : undefined;
    if (method !== "GET") calls.push({ method, path, body, key: r.headers()["idempotency-key"] });
    if (path === "/v1/me/chats") return route.fulfill(json({ chats: [chat], unread: 1 }));
    if (path === "/v1/me/chats/c1" && method === "GET") return route.fulfill(json(chat));
    if (path === "/v1/me/chats/c1" && method === "PUT") {
      const b = body as { offers?: boolean };
      if (b.offers !== undefined) Object.assign(chat, { offers: b.offers ? "yes" : "no", ask_offers: false });
      return route.fulfill(json(chat));
    }
    if (path === "/v1/me/chats/c1/messages" && method === "GET") return route.fulfill(json({ messages, more: false }));
    if (path === "/v1/me/chats/c1/messages") return route.fulfill(json({ id: `m${messages.length + 1}`, mine: true, kind: "text", body: (body as { text: string }).text, read_at: null, created_at: new Date().toISOString() }, 201));
    if (path.endsWith("/read") || path.endsWith("/typing")) return route.fulfill({ status: 204 });
    if (path === "/v1/me/chats/c1/shop") return route.fulfill(json(shop));
    if (path === "/v1/me/chats/c1/orders" && method === "GET") return route.fulfill(json({ orders: [order] }));
    if (path === "/v1/me/chats/c1/orders") return route.fulfill(json({ order: { ...order, reference: "MK-9", status: "sent", summary: "2 × Jollof rice, 1 × Ankara shirt", total_kobo: 1_400_000, due_kobo: 1_400_000 } }, 201));
    if (/\/orders\/MK-\d\/pay$/.test(path)) return route.fulfill(json({ reference: "MK-9", url: "https://pay.example.test/MK-9", due_kobo: 1_400_000 }));
    if (path === "/v1/me/chats/c1/services") return route.fulfill(json({ services: [{ id: "s1", name: "Catering tasting", minutes: 30, price_kobo: 500_000 }], open: true }));
    if (path.startsWith("/v1/me/chats/c1/services/s1/times"))
      return route.fulfill(json({ service: { id: "s1", name: "Catering tasting", minutes: 30 }, days: ["2026-11-20", "2026-11-21"], day: "2026-11-20", times: ["2026-11-20T09:00:00Z", "2026-11-20T10:00:00Z"] }));
    if (path === "/v1/me/chats/c1/bookings") return route.fulfill(json({ booking: { reference: "MK-B1", service_id: "s1", service_name: "Catering tasting", starts_at: "2026-11-20T10:00:00Z", ends_at: "2026-11-20T10:30:00Z" } }, 201));
    if (path === "/v1/me/chats/c1/report") return route.fulfill(json({ id: "r1", messages_sent: true, said: "Thank you. Taskiem's moderators will look at this conversation. You can also block this business." }, 201));
    if (path === "/v1/me/chats/c1/block") return route.fulfill(json({ ...chat, blocked: method === "POST" }));
    if (path.startsWith("/v1/me/chats/c1/files/")) return route.fulfill({ status: 200, contentType: "application/pdf", body: "%PDF-1.4" });
    return route.fallback();
  };
  return { calls, chat, handle };
}

for (const width of [390, 360])
  test(`a person's chat at ${width} px: the tools, the cards, offers and the report, in both themes`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 800 });
    const w = world();
    await page.route("**/v1/me", (r) => r.fulfill(json(PERSON)));
    await page.route("**/v1/me/invitations", (r) => r.fulfill(json({ invitations: [] })));
    await page.route("**/v1/me/storefront-orders?*", (r) =>
      r.fulfill(
        json({
          orders: [
            { reference: "BS-7Q2R", state: "completed", total_kobo: 1_250_000, made_at: new Date().toISOString(), business: "Bola Stores", business_id: "b2", waiting: false },
            { reference: "BS-1A1A", state: "received", total_kobo: 450_000, made_at: new Date().toISOString(), business: "Bola Stores", business_id: "b2", waiting: true },
          ],
          waiting: true,
        }),
      ),
    );
    await page.route("**/v1/me/storefront-orders/confirm", (r) => r.fulfill(json({ confirmed: true })));
    await page.route(/\/v1\/me\/chats(\/|$|\?)/, w.handle);

    await page.goto("/you");
    await expect(page.getByTestId("person-chat")).toContainText("Mama's Kitchen");
    await expect(page.getByTestId("shop-orders")).toContainText("Chat with Bola Stores about this order");
    await expect(page.getByTestId("these-are-mine")).toBeVisible();
    await check(page, "/you", width);
    await page.screenshot({ path: `test-results/person-you-${width}.png` });

    await page.getByTestId("person-chat").click();
    await expect(page.getByTestId("person-head-line")).toHaveText("Open now, closes at 18:00");
    await expect(page.getByTestId("app-card")).toHaveCount(4);
    await check(page, "a conversation", width);
    await page.screenshot({ path: `test-results/person-chat-${width}.png` });
    await page.getByTestId("offers-question").getByRole("button", { name: "No" }).click();
    expect(w.calls.find((c) => c.method === "PUT")?.body).toEqual({ offers: false });

    // Their shop: few left, sizes and colours, delivery, and the order once.
    await page.getByTestId("person-tools-button").click();
    await page.getByTestId("person-tool-shop").click();
    const shop = page.getByTestId("shop-sheet");
    await expect(shop.getByText("Only 3 left")).toBeVisible();
    await check(page, "their shop", width);
    await page.screenshot({ path: `test-results/person-shop-${width}.png` });
    await shop.getByRole("button", { name: "One more Jollof rice" }).click();
    await shop.getByRole("button", { name: "One more Jollof rice" }).click();
    await shop.getByLabel("Size and colour of Ankara shirt").selectOption("i2a");
    await expect(shop.getByText("Only 2 left")).toBeVisible();
    await shop.getByRole("button", { name: "One more Ankara shirt (Blue, M)" }).click();
    await shop.getByTestId("shop-check").click();
    await shop.getByLabel("Deliver it to me").check();
    await shop.getByLabel("Where to").selectOption("Ikeja");
    await shop.getByLabel("Your address").fill("12 Allen Avenue");
    await expect(shop.getByTestId("shop-place")).toHaveText("Place order · ₦14,000");
    await check(page, "checking an order", width);
    await page.screenshot({ path: `test-results/person-order-${width}.png` });
    await shop.getByTestId("shop-place").click();
    await expect(shop.getByTestId("shop-done")).toContainText("MK-9");
    const placed = w.calls.find((c) => c.path.endsWith("/orders") && c.method === "POST");
    expect(placed?.key).toMatch(/^order-/);
    expect(placed?.body).toEqual({ lines: [{ item_id: "i1", quantity: 2 }, { item_id: "i2a", quantity: 1 }], fulfilment: "delivery", zone: "Ikeja", address: "12 Allen Avenue", note: "" });
    await shop.getByTestId("pay-button").click();
    await expect(shop.getByTestId("pay-open")).toHaveAttribute("href", "https://pay.example.test/MK-9");
    await shop.getByRole("button", { name: "Back to the chat" }).click();
    await expect(shop).toHaveCount(0);

    // A product card's Order opens their shop with it in the basket.
    await page.getByTestId("app-card").filter({ hasText: "Jollof rice" }).getByRole("button", { name: /^Order/ }).click();
    await expect(page.getByTestId("shop-check")).toContainText("1 item");
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("shop-sheet")).toHaveCount(0);

    // A booking offer's Book: a free time, then the booking once.
    await page.getByTestId("app-card").filter({ hasText: "Catering tasting" }).getByRole("button", { name: /^Book/ }).click();
    const book = page.getByTestId("book-sheet");
    await book.getByRole("button", { name: "11:00" }).click();
    await book.getByRole("button", { name: "Continue" }).click();
    await check(page, "a booking", width);
    await book.getByTestId("book-confirm").click();
    await expect(book.getByTestId("book-done")).toBeVisible();
    const booked = w.calls.find((c) => c.path.endsWith("/bookings"));
    expect(booked?.key).toMatch(/^book-/);
    expect(booked?.body).toEqual({ service_id: "s1", start: "2026-11-20T10:00:00Z", note: "" });
    await book.getByRole("button", { name: "Back to the chat" }).click();

    // The pay card links to its page; the order card has Pay.
    await expect(page.getByTestId("app-card").filter({ hasText: "Pay for MK-1" }).getByRole("link", { name: /^Pay/ })).toHaveAttribute("href", "https://pay.example.test/MK-1");
    await expect(page.getByTestId("app-card").filter({ hasText: "Order MK-1" }).getByTestId("pay-button")).toBeVisible();

    // Your orders, and Pay.
    await page.getByTestId("person-tools-button").click();
    await page.getByTestId("person-tool-orders").click();
    await expect(page.getByTestId("person-order")).toContainText("To pay");
    await check(page, "your orders", width);
    await page.keyboard.press("Escape");

    // A file from the business is its own copy.
    await expect(page.getByRole("link", { name: /Menu\.pdf/ })).toHaveAttribute("href", "/v1/me/chats/c1/files/m7");
  });

test("a link that no longer works, and a business not taking chats, pass axe in both themes", async ({ page }) => {
  for (const width of [390, 360]) {
    await page.setViewportSize({ width, height: 800 });
    await page.route("**/v1/chat-links/gonegonego", (r) => r.fulfill(json({ error: "This link no longer works.", gone: true }, 410)));
    await page.route("**/v1/chat-links/offoffoffo", (r) =>
      r.fulfill(
        json({
          name: "Bola Stores",
          area: "Ikeja",
          takes_chats: false,
          open_now: false,
          hours: { days: [[{ open: "08:00", close: "18:00" }], [], [], [], [], [], []] },
          code: "offoffoffo",
          link: "http://localhost/m/offoffoffo",
          words: "",
          shop: "https://bola.example.test",
        }),
      ),
    );
    await page.goto("/m/gonegonego");
    await expect(page.getByRole("heading", { name: "This link no longer works" })).toBeVisible();
    await check(page, "a replaced link", width);
    await page.goto("/m/offoffoffo");
    await expect(page.getByTestId("not-taking-chats")).toContainText("Bola Stores is not taking chats in Taskiem right now.");
    await expect(page.getByText("Ikeja · Closed now")).toBeVisible();
    await check(page, "not taking chats", width);
  }
});
