import { execFileSync } from "node:child_process";
import { createServer as createHttp, type Server as HttpServer } from "node:http";
import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Taskiem for everyone, stage 3, the web (docs/spec/taskiem-for-everyone.md
// §7, §10, §13), on the real server: a business with a published shop is
// in Discover by default; a person finds it by name and category, opens
// its profile, chats from Discover and orders in Their shop with an address
// saved on the way; the business accepts; the order is on You with Pay and
// Chat about this, and its page shows the address; the address is in the
// person's settings; the business turns Discover off and is not found.
// The person's code goes out on Taskiem's WhatsApp number to a fake Graph
// API on the port customer-chat.spec.ts and you.spec.ts use, so this file
// runs one test at a time and apart from them.
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
        res.end(JSON.stringify({ messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: m.to.replace(/^\+/, "") }], messages: [{ id: `wamid.do${sent.length}` }] }));
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

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
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

async function must<T>(r: Promise<{ status(): number; text(): Promise<string>; json(): Promise<unknown> }>, status: number): Promise<T> {
  const res = await r;
  expect(res.status(), await res.text()).toBe(status);
  return (await res.json()) as T;
}

/** Open all day, every day (to 23:59, then past midnight). */
const allDay = { days: Array.from({ length: 7 }, () => [{ open: "00:00", close: "23:59" }, { open: "23:59", close: "00:30" }]) };

/** A business with a published shop (two items, pickup and delivery to
 * Yaba, paid after it accepts), a category and an area in its profile and
 * a logo: everything a listing needs. */
async function shop(req: APIRequestContext, name: string) {
  await must(req.post("/v1/signup", { headers: H, data: { tenant: name, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@do.test`, name: "Ada Owner", password: "correct horse battery" } }), 201);
  const me = await must<{ tenant_id: string }>(req.get("/v1/me"), 200);
  const tenant = me.tenant_id;
  // A checked phone and business, as an agent or Taskiem would give them.
  sql(`INSERT INTO tenant_verifications (id, tenant_id, level, method, actor) VALUES (gen_random_uuid(), '${tenant}', 'phone', 'test', 'test'), (gen_random_uuid(), '${tenant}', 'business', 'test', 'test')`);
  await must(req.put("/v1/storefront", { headers: H, data: { slug: `ada-${letters()}`, draft: { name, area: "Yaba", address: "3 Montgomery Road, Yaba", hours: allDay } } }), 200);
  // Orders: the default workflow (accept or decline), published.
  const made = await must<{ id: string }>(req.post("/v1/templates/storefront-order/instantiate", { headers: H, data: { params: {}, name: "Orders" } }), 201);
  await must(req.post(`/v1/workflows/${made.id}/versions/1/publish`, { headers: H }), 200);
  await must(
    req.put("/v1/storefront/actions/order", {
      headers: H,
      data: { enabled: true, payment: "after_accept", options: { pickup: true, delivery: true, zones: [{ name: "Yaba", fee_kobo: 100_000 }] } },
    }),
    200,
  );
  const published = await must<{ status: string }>(req.post("/v1/storefront/publish", { headers: H }), 200);
  expect(published.status).toBe("published");
  // Its items: the test server has no data tables (no Xenibase), so the
  // shop's copy of its rows is written as a sync would leave it, with no
  // table behind it. (Without a table the order's price check finds the
  // items gone, so an order comes in as "New prices to confirm".)
  sql(`UPDATE storefronts SET catalogue_synced_at = now() WHERE tenant_id = '${tenant}'`);
  sql(
    `INSERT INTO storefront_catalogue (storefront_id, row_id, tenant_id, position, item, table_name) SELECT s.id, x.row_id, s.tenant_id, x.position, x.item::jsonb, '' FROM storefronts s,
      (VALUES ('1', 0, '{"id":"1","name":"Jollof rice","price_kobo":450000,"available":true}'), ('2', 1, '{"id":"2","name":"Chapman","price_kobo":150000,"available":true}')) AS x(row_id, position, item)
      WHERE s.tenant_id = '${tenant}'`,
  );
  // …and the published page has a catalogue, as it would have been published with one.
  sql(`UPDATE storefront_snapshots SET model = jsonb_set(model, '{has_catalogue}', 'true') WHERE tenant_id = '${tenant}'`);
  await must(req.put("/v1/business-profile", { headers: H, data: { area: "Yaba", categories: ["food"], tagline: "Hot jollof every day" } }), 200);
  // The test server keeps no photos: a logo whose sizes are the app's own file.
  const media = sql(
    `INSERT INTO storefront_media (id, tenant_id, hash, width, height, type, urls, paths, moderation, created_by) VALUES (gen_random_uuid(), '${tenant}', 'e2e-${letters()}', 512, 512, 'image/png', '{"160":"/brand/seal/mark-512.png","320":"/brand/seal/mark-512.png"}', '{}', 'approved', 'e2e') RETURNING id`,
  );
  sql(`UPDATE business_profile SET logo_media = '${media}' WHERE tenant_id = '${tenant}'`);
  return tenant;
}

/** A person signed in by phone on their own side (the code on WhatsApp). */
async function person(page: Page) {
  const rest = String(Date.now()).slice(-7) + String(Math.floor(Math.random() * 10));
  const e164 = `+23491${rest.slice(0, 8)}`;
  const before = sent.length;
  await must(page.request.post("/v1/auth/phone/start", { headers: H, data: { phone: e164 } }), 202);
  let code = "";
  await expect
    .poll(() => {
      const m = sent.slice(before).filter((x) => x.to === e164 || x.to === e164.slice(1)).pop();
      code = m?.template?.components?.flatMap((c) => c.parameters ?? []).find((p) => /^\d{6}$/.test(p.text ?? ""))?.text ?? "";
      return code;
    })
    .toMatch(/^\d{6}$/);
  const v = await must<{ status: string; ticket: string }>(page.request.post("/v1/auth/phone/verify", { headers: H, data: { phone: e164, code } }), 200);
  expect(v.status).toBe("new");
  await must(page.request.post("/v1/auth/phone/create", { headers: H, data: { ticket: v.ticket, name: "Tolu Bello", adult: true, terms: true } }), 201);
}

test("a person finds a business in Discover, orders with a saved address, and finds the order on You; the business turns Discover off", async ({ browser }) => {
  test.setTimeout(240_000);
  const bizCtx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const biz = await bizCtx.newPage();
  const name = `Ada Kitchen ${letters()}`;
  await shop(biz.request, name);

  // Listed by default with a published shop, and shown: the settings say so.
  await biz.goto("/settings/discover");
  await expect(biz.getByTestId("discover-tag")).toHaveText("Listed");
  await expect(biz.getByTestId("discover-listed")).toBeChecked();
  await expect(biz.getByTestId("discover-default")).toHaveText("On by default, because your shop's page is published.");
  await expect(biz.getByTestId("discover-preview-row")).toContainText(name);
  await expect(biz.getByTestId("discover-preview-chat")).toBeVisible();
  await check(biz, "/settings/discover", 1280);

  // The person, on a phone: Discover finds it by name, and by its category.
  const ctx = await browser.newContext({ viewport: { width: 390, height: 844 } });
  const page = await ctx.newPage();
  await person(page);
  const searches: unknown[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/v1/discover/search")) {
      expect(r.url()).not.toContain("lat");
      searches.push(r.postDataJSON());
    }
  });
  await page.goto("/you");
  await page.getByRole("navigation", { name: "You" }).getByRole("link", { name: "Discover" }).click();
  await expect(page.getByRole("heading", { name: "Discover", level: 1 })).toBeVisible();
  await page.getByTestId("discover-search").fill(name);
  const row = page.getByTestId("discover-result").filter({ hasText: name });
  await expect(row).toContainText("Food and drinks · Yaba");
  await expect(row).toContainText("Open now");
  await expect.poll(() => searches).toContainEqual({ q: name });
  await check(page, "/you/discover", 390);
  await page.getByTestId("discover-search").fill("");
  await page.getByTestId("discover-categories").getByRole("button", { name: "Food and drinks" }).click();
  await expect(page.getByTestId("discover-result").filter({ hasText: name })).toBeVisible();
  await page.screenshot({ path: "test-results/discover-390.png" });

  // Its profile: the logo, its hours and address, Chat, Their shop and Book.
  await page.getByTestId("discover-result").filter({ hasText: name }).getByRole("link", { name, exact: true }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
  await expect(page.getByText("Hot jollof every day")).toBeVisible();
  await expect(page.getByTestId("discover-address")).toHaveText("3 Montgomery Road, Yaba");
  await expect(page.getByTestId("discover-hours")).toContainText("Monday");
  await expect(page.getByTestId("discover-shop")).toBeVisible();
  await check(page, "/you/discover/{business}", 390);
  await page.screenshot({ path: "test-results/discover-profile-390.png" });

  // Chat from Discover: the chat opens (started with from: discover).
  const started = page.waitForRequest((r) => r.url().endsWith("/v1/me/chats") && r.method() === "POST");
  await page.getByTestId("discover-chat").click();
  expect((await started).postDataJSON()).toMatchObject({ from: "discover" });
  await expect(page).toHaveURL(/\/you\/chats\/[0-9a-f-]{36}$/);
  await expect(page.getByRole("heading", { name: `Chat with ${name}` })).toBeAttached();

  // Back in Discover, Their shop opens the same chat, with the shop open.
  await page.goBack();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
  await page.reload();
  await page.getByTestId("discover-shop").click();
  const sheet = page.getByTestId("shop-sheet");
  await expect(sheet).toBeVisible();
  await sheet.getByRole("button", { name: "One more Jollof rice" }).click();
  await sheet.getByTestId("shop-check").click();
  await sheet.getByLabel("Deliver it to me").check();
  await sheet.getByLabel("Where to").selectOption("Yaba");
  await sheet.getByLabel("Your address").fill("12 Herbert Macaulay Way");
  await sheet.getByLabel("Landmark").fill("Opposite the bank");
  await sheet.getByTestId("shop-keep-address").check();
  await expect(sheet.getByLabel("Name it")).toHaveValue("Home");
  await check(page, "checking an order with an address to save", 390);
  const placing = page.waitForRequest((r) => /\/v1\/me\/chats\/[^/]+\/orders$/.test(r.url()) && r.method() === "POST");
  await sheet.getByTestId("shop-place").click();
  const body = (await placing).postDataJSON() as { address_id?: string; address: string };
  expect(body.address_id).toMatch(/^[0-9a-f-]{36}$/);
  expect(body.address).toBe("");
  await expect(sheet.getByTestId("shop-done")).toBeVisible();
  const ref = /Your order (\S+) is with/.exec((await sheet.getByTestId("shop-done").textContent()) ?? "")?.[1] ?? "";
  expect(ref).not.toBe("");
  await sheet.getByRole("button", { name: "Back to the chat" }).click();

  // Orders on You: the order (once the business's inbox takes it in), with
  // Chat about this. Pay is for an order with something due: the next test.
  await page.goto("/you/orders");
  const order = page.getByTestId("you-order").filter({ hasText: ref });
  await expect
    .poll(
      async () => {
        await page.reload();
        return order.count();
      },
      { timeout: 30_000, message: "the order on You" },
    )
    .toBe(1);
  await expect(order).toContainText(name);
  await expect(order).toContainText("1 × Jollof rice");
  await expect(order.getByTestId("order-pay")).toHaveCount(0);
  await check(page, "/you/orders", 390);
  await page.screenshot({ path: "test-results/you-orders-390.png" });

  // One order's page: the address as it was copied onto the order.
  await order.getByRole("link", { name: /See the order/ }).click();
  await expect(page.getByTestId("you-order-hero")).toBeVisible();
  await expect(page.getByTestId("you-order-address")).toContainText("12 Herbert Macaulay Way, Yaba");
  await expect(page.getByTestId("you-order-address")).toContainText("Opposite the bank");
  await check(page, "/you/orders/{business}/{reference}", 390);

  // Chat about this: the chat with the business, the words ready in the box.
  await page.getByTestId("order-chat-about").click();
  await expect(page).toHaveURL(/\/you\/chats\/[0-9a-f-]{36}$/);
  await expect(page.getByLabel(`Message ${name}`)).toHaveValue(`Hello, about my order ${ref}: `);

  // The address saved on the way is in the person's settings, at 360 px too.
  await page.setViewportSize({ width: 360, height: 800 });
  await page.goto("/you/settings");
  await expect(page.getByTestId("saved-address")).toContainText("Home");
  await expect(page.getByTestId("saved-address")).toContainText("12 Herbert Macaulay Way, Yaba");
  await check(page, "/you/settings with an address", 360);
  // Another, made the default; a phone that is not one is said plainly.
  await page.getByTestId("address-add").click();
  await page.getByLabel("Name it").fill("Work");
  await page.getByLabel("Street and number").fill("5 Broad Street");
  await page.getByLabel("Phone for the rider").fill("12");
  await page.getByLabel("Make this my default address").check();
  await check(page, "adding an address", 360);
  await page.getByTestId("address-save").click();
  await expect(page.getByTestId("address-form").getByRole("alert")).toContainText("That phone number does not look right.");
  await page.getByLabel("Phone for the rider").fill("0803 123 4567");
  await page.getByTestId("address-save").click();
  await expect(page.getByTestId("saved-address")).toHaveCount(2);
  await expect(page.getByTestId("saved-address").first()).toContainText("Work");
  await expect(page.getByTestId("saved-address").first()).toContainText("Default");

  // At checkout the saved ones are offered, the default chosen.
  await page.goto("/you/discover");
  await page.getByTestId("discover-search").fill(name);
  await page.getByTestId("discover-result").filter({ hasText: name }).getByRole("link", { name, exact: true }).click();
  await page.getByTestId("discover-shop").click();
  await page.getByTestId("shop-sheet").getByRole("button", { name: "One more Chapman" }).click();
  await page.getByTestId("shop-check").click();
  await page.getByTestId("shop-sheet").getByLabel("Deliver it to me").check();
  await expect(page.getByTestId("shop-saved-addresses").getByRole("radio", { name: /Work/ })).toBeChecked();
  await expect(page.getByTestId("shop-sheet").getByLabel("Your address")).toHaveCount(0);
  await check(page, "a saved address at checkout", 360);
  await page.keyboard.press("Escape");

  // The business turns Discover off: it is not found any more.
  await biz.getByTestId("discover-listed").uncheck();
  await expect(biz.getByTestId("discover-tag")).toHaveText("Off");
  await expect(biz.getByTestId("discover-default")).toContainText("Off, as you chose.");
  await expect(biz.getByTestId("discover-default-back")).toBeVisible();
  await page.goto("/you/discover");
  await page.getByTestId("discover-search").fill(name);
  await expect(page.getByText(`No business found for “${name}”`)).toBeVisible();
  await expect(page.getByTestId("discover-result").filter({ hasText: name })).toHaveCount(0);
  await check(page, "/you/discover, nothing found", 360);
  // Back to the default: found again.
  await biz.getByTestId("discover-default-back").click();
  await expect(biz.getByTestId("discover-tag")).toHaveText("Listed");
  await page.reload();
  await page.getByTestId("discover-search").fill(name);
  await expect(page.getByTestId("discover-result").filter({ hasText: name })).toBeVisible();
});

const PERSON = {
  tenant_id: null,
  personal: true,
  roles: [],
  permissions: [],
  auth_method: "phone",
  user: { id: "0192e2e0-0000-7000-8000-00000000d001", email: "p@phone.taskiem.invalid", email_placeholder: true, name: "Ada Obi", phone: "+2348031234567" },
  businesses: [],
};
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
const B1 = "0192e2e0-0000-7000-8000-00000000b101";
const B2 = "0192e2e0-0000-7000-8000-00000000b102";

for (const width of [390, 360])
  test(`Orders on You at ${width} px: These are mine, Pay, Chat about this, older orders and one order, in both themes`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 });
    const now = Date.now();
    const at = (min: number) => new Date(now - min * 60_000).toISOString();
    const first = [
      { business_id: B1, business: "Mama's Kitchen", reference: "MK-7Q2R", kind: "order", state: "accepted", summary: "2 × Jollof rice, 1 × Chapman", total_kobo: 1_050_000, due_kobo: 1_050_000, pay: true, made_at: at(5), chat_id: "c1" },
      { business_id: B2, business: "Bola Salon", reference: "BS-1A1A", kind: "booking", state: "accepted", summary: "Braids", total_kobo: 800_000, due_kobo: 0, pay: false, made_at: at(60 * 26), chat_id: null },
    ];
    const older = [{ business_id: B1, business: "Mama's Kitchen", reference: "MK-0001", kind: "order", state: "completed", summary: "1 × Puff-puff", total_kobo: 60_000, due_kobo: 0, pay: false, made_at: at(60 * 24 * 9), chat_id: "c1" }];
    let waiting = 2;
    const asked: string[] = [];
    const posted: { path: string; body: unknown }[] = [];
    await page.route("**/v1/me", (r) => r.fulfill(json(PERSON)));
    await page.route("**/v1/me/invitations", (r) => r.fulfill(json({ invitations: [] })));
    await page.route(/\/v1\/me\/orders(\?|$)/, (r) => {
      const u = new URL(r.request().url());
      asked.push(u.search);
      if (u.searchParams.get("before") === "cursor-2") return r.fulfill(json({ orders: older, next: null, waiting_count: waiting }));
      return r.fulfill(json({ orders: first, next: "cursor-2", waiting_count: waiting }));
    });
    await page.route("**/v1/me/storefront-orders/confirm", (r) => {
      waiting = 0;
      return r.fulfill(json({ confirmed: true }));
    });
    await page.route(`**/v1/me/orders/${B1}/MK-7Q2R/pay`, (r) => r.fulfill(json({ reference: "MK-7Q2R", url: "https://pay.example.test/MK-7Q2R", due_kobo: 1_050_000 })));
    await page.route(`**/v1/me/orders/${B1}/MK-7Q2R`, (r) =>
      r.fulfill(
        json({
          ...first[0],
          lines: [
            { name: "Jollof rice", quantity: 2, price_kobo: 450_000, line_kobo: 900_000 },
            { name: "Chapman", quantity: 1, price_kobo: 50_000, line_kobo: 50_000 },
          ],
          fulfilment: "delivery",
          zone: "Yaba",
          delivery_kobo: 100_000,
          paid_kobo: 0,
          address: { address: "12 Herbert Macaulay Way, Yaba", landmark: "Opposite the bank\nPhone for delivery: +2348031234567" },
          delivery: { status: "picked_up", line: "On its way to you" },
        }),
      ),
    );
    await page.route(`**/v1/me/orders/${B2}/BS-1A1A`, (r) =>
      r.fulfill(json({ ...first[1], lines: [], paid_kobo: 800_000, service_name: "Braids", starts_at: "2026-11-20T09:00:00Z", ends_at: "2026-11-20T11:00:00Z" })),
    );
    await page.route("**/v1/me/chats", async (r) => {
      if (r.request().method() !== "POST") return r.fallback();
      posted.push({ path: "/v1/me/chats", body: r.request().postDataJSON() });
      return r.fulfill(json({ id: "c2", business: B2, name: "Bola Salon" }, 201));
    });
    // The chats the buttons open are not drawn here (customer-chat.spec.ts).
    await page.route(/\/v1\/me\/chats\/c\d/, (r) => r.fulfill(json({ error: "not here" }, 404)));

    await page.goto("/you/orders");
    await expect(page.getByTestId("these-are-mine")).toContainText("We found 2 orders made with your number before it was on your account. Are they yours?");
    await expect(page.getByTestId("you-order")).toHaveCount(2);
    const mk = page.getByTestId("you-order").filter({ hasText: "MK-7Q2R" });
    await expect(mk).toContainText("Mama's Kitchen");
    await expect(mk).toContainText("₦10,500");
    await expect(mk).toContainText("Accepted");
    await expect(mk).toContainText("To pay");
    await expect(page.getByTestId("you-order").filter({ hasText: "BS-1A1A" }).getByTestId("order-pay")).toHaveCount(0);
    await check(page, "/you/orders", width);
    await page.screenshot({ path: `test-results/you-orders-${width}.png` });

    // These are mine: confirmed, the list read again.
    await page.getByTestId("these-are-mine").getByRole("button", { name: "These are mine" }).click();
    await expect(page.getByTestId("these-are-mine")).toHaveCount(0);

    // Pay: the link asked for, then opened by the person.
    await mk.getByTestId("order-pay").click();
    await expect(mk.getByTestId("order-pay-open")).toHaveAttribute("href", "https://pay.example.test/MK-7Q2R");

    // Older orders: next goes back as before.
    await page.getByTestId("orders-more").click();
    await expect(page.getByTestId("you-order")).toHaveCount(3);
    await expect(page.getByTestId("orders-more")).toHaveCount(0);
    expect(asked.some((q) => q.includes("before=cursor-2"))).toBe(true);

    // One order: the hero, its lines, how it comes, the address, the rider.
    await mk.getByRole("link", { name: /See the order/ }).click();
    await expect(page.getByRole("heading", { name: "Order MK-7Q2R" })).toBeVisible();
    await expect(page.getByTestId("you-order-hero")).toContainText("₦10,500");
    await expect(page.getByText("Delivered to you in Yaba")).toBeVisible();
    await expect(page.getByTestId("you-order-address")).toContainText("12 Herbert Macaulay Way, Yaba");
    await expect(page.getByTestId("you-order-delivery")).toHaveText("On its way to you");
    await expect(page.getByRole("img", { name: "On its way to you: step 2 of 4" })).toBeVisible();
    await check(page, "/you/orders/{business}/{reference}", width);
    await page.screenshot({ path: `test-results/you-order-${width}.png` });

    // A booking: its times, nothing to pay.
    await page.goto(`/you/orders/${B2}/BS-1A1A`);
    await expect(page.getByRole("heading", { name: "Booking BS-1A1A" })).toBeVisible();
    await expect(page.getByText("(Lagos time)")).toBeVisible();
    await expect(page.getByTestId("order-pay")).toHaveCount(0);
    await check(page, "a booking on You", width);

    // Chat about this with no chat yet: started from the order.
    await page.getByTestId("order-chat-about").click();
    await expect.poll(() => posted[0]?.body).toEqual({ business_id: B2, from: "order" });
    await expect(page).toHaveURL(/\/you\/chats\/c2$/);
  });

test("Discover with stand-ins: Near me sends the place in the body only, more results, a refused search, a business not taking chats, and a report", async ({ browser }) => {
  const ctx = await browser.newContext({ viewport: { width: 360, height: 800 }, geolocation: { latitude: 6.5095, longitude: 3.3711 }, permissions: ["geolocation"] });
  const page = await ctx.newPage();
  const bodies: Record<string, unknown>[] = [];
  const urls: string[] = [];
  let refuse = false;
  const result = (n: number) => ({
    business: `0192e2e0-0000-7000-8000-0000000c${String(n).padStart(4, "0")}`,
    name: `Shop ${n}`,
    photo: {},
    category: "food",
    category_name: "Food and drinks",
    area: "Yaba",
    open_now: n % 2 === 0,
    line: n % 2 === 0 ? "Open now, closes at 18:00" : "Closed now. Opens tomorrow at 09:00",
    can_chat: n !== 2,
    km: n * 0.7,
  });
  await page.route("**/v1/me", (r) => r.fulfill(json(PERSON)));
  await page.route("**/v1/discover/categories", (r) => r.fulfill(json({ categories: [{ key: "food", name: "Food and drinks" }, { key: "beauty", name: "Hair and beauty" }] })));
  await page.route("**/v1/discover/search", (r) => {
    urls.push(r.request().url());
    const b = r.request().postDataJSON() as Record<string, unknown>;
    bodies.push(b);
    if (refuse) return r.fulfill(json({ error: "You are searching too fast. Wait a moment and try again." }, 429));
    const offset = (b.offset as number | undefined) ?? 0;
    return r.fulfill(json({ results: Array.from({ length: offset ? 1 : 20 }, (_, i) => result(offset + i + 1)), more: !offset }));
  });
  const profile = {
    business: result(2).business,
    name: "Shop 2",
    tagline: "Fresh bread",
    category: "food",
    category_name: "Food and drinks",
    area: "Yaba",
    photos: [],
    hours: { days: [[{ open: "08:00", close: "18:00" }], null, null, null, null, null, null] },
    open_now: true,
    line: "Open now, closes at 18:00",
    shop: "https://shop2.example.test",
    can_chat: false,
    has_shop: false,
    has_book: false,
  };
  await page.route(`**/v1/discover/${result(2).business}`, (r) => r.fulfill(json(profile)));
  await page.route(`**/v1/discover/${result(2).business}/report`, (r) => r.fulfill(json({ reported: true, words: "Thank you. Taskiem's moderators will look at this business." }, 202)));

  await page.goto("/you/discover");
  await expect(page.getByTestId("discover-result")).toHaveCount(20);
  await page.getByTestId("discover-near").click();
  await expect(page.getByTestId("discover-near-line")).toBeVisible();
  await expect.poll(() => bodies.at(-1)).toEqual({ near: { lat: 6.5095, lng: 3.3711 } });
  expect(urls.every((u) => u.endsWith("/v1/discover/search"))).toBe(true);
  await expect(page.getByTestId("discover-result").first()).toContainText("700 m away");
  await check(page, "Discover near me", 360);
  await page.getByTestId("discover-more").click();
  await expect(page.getByTestId("discover-result")).toHaveCount(21);
  expect(bodies.at(-1)).toEqual({ near: { lat: 6.5095, lng: 3.3711 }, offset: 20 });
  // Near me again: the place is let go.
  await page.getByTestId("discover-near").click();
  await expect.poll(() => bodies.at(-1)).toEqual({});

  // Too many searches: said plainly.
  refuse = true;
  await page.getByTestId("discover-area").fill("Ikeja");
  await expect(page.getByTestId("discover-problem")).toHaveText("You are searching too fast. Wait a moment and try again.");
  await check(page, "Discover refusing a search", 360);
  refuse = false;

  // A business not taking chats: its hours and page, no Chat; and a report.
  await page.goto(`/you/discover/${result(2).business}`);
  await expect(page.getByTestId("discover-no-chat")).toContainText("Shop 2 is not taking chats in Taskiem right now. You can still visit their page.");
  await expect(page.getByTestId("discover-chat")).toHaveCount(0);
  await expect(page.getByTestId("discover-hours")).toContainText("Monday 08:00 to 18:00");
  await page.getByTestId("discover-report").click();
  await page.getByLabel("They sell something not allowed").check();
  await page.getByLabel("Tell us more").fill("Selling medicines");
  await check(page, "reporting a business", 360);
  const sending = page.waitForRequest((r) => r.url().endsWith("/report"));
  await page.getByTestId("discover-report-send").click();
  expect((await sending).postDataJSON()).toEqual({ reason: "prohibited", note: "Selling medicines" });
  await expect(page.getByTestId("discover-reported")).toHaveText("Thank you. Taskiem's moderators will look at this business.");
  await ctx.close();
});
