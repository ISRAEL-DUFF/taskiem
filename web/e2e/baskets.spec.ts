import { execFileSync } from "node:child_process";
import { createServer as createHttp, type Server as HttpServer } from "node:http";
import { expect, test, type APIRequestContext, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Ordering together, the web (docs/spec/people-chatting.md §10.3, §11 A
// and C; people-chatting-5b.md C, C1 to C3), on the real server: in a chat
// between two people (C3), Ada starts "Order together" from a business's
// shop; each adds their own items in the basket's sheet and sees the
// other's live; Ada chooses pick-up, closes, reopens and closes, and
// places it as her own order paying all (the business takes no payment in
// the app); the business has one order from Ada with whose lines are
// whose; the card follows the order; Ada sends Bola a "Your share" card in
// one step. Phones at 390 and 360 px, both themes, axe. The people's codes
// go out on a fake Graph API on the port people.spec.ts uses, so this file
// runs one test at a time and apart from it.
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
        res.end(JSON.stringify({ messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: m.to.replace(/^\+/, "") }], messages: [{ id: `wamid.pp${sent.length}` }] }));
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

// Letters for the digits that spell no word: "abcdefghij" once spelled "efcc", a protected name.
const letters = () => String(Date.now()).replace(/\d/g, (d) => "kmpqrtvwxz".charAt(Number(d)));

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

async function must<T>(r: Promise<{ status(): number; text(): Promise<string>; json(): Promise<unknown> }>, status: number): Promise<T> {
  const res = await r;
  expect(res.status(), await res.text()).toBe(status);
  return (await res.json()) as T;
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

let n = 0;
/** A person signed in by phone on their own side, on a phone of `width`. */
async function person(
  browser: Browser,
  name: string,
  width: number,
  opts: Parameters<Browser["newContext"]>[0] = {},
  scripts: (() => void)[] = [],
): Promise<{ page: Page; e164: string; typed: string }> {
  const ctx = await browser.newContext({ viewport: { width, height: 800 }, ...opts });
  for (const sc of scripts) await ctx.addInitScript(sc);
  const page = await ctx.newPage();
  const rest = (String(Date.now()).slice(-6) + String(++n % 10) + String(Math.floor(Math.random() * 10))).slice(-8);
  const e164 = `+23470${rest}`;
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
  const v = await must<{ ticket: string }>(page.request.post("/v1/auth/phone/verify", { headers: H, data: { phone: e164, code } }), 200);
  await must(page.request.post("/v1/auth/phone/create", { headers: H, data: { ticket: v.ticket, name, adult: true, terms: true } }), 201);
  const d = e164.slice(4);
  return { page, e164, typed: `0${d.slice(0, 3)} ${d.slice(3, 6)} ${d.slice(6)}` };
}

/** A business with a published shop (jollof and chapman) in Discover. */
async function shop(req: APIRequestContext, name: string) {
  await must(req.post("/v1/signup", { headers: H, data: { tenant: name, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@pp.test`, name: "Ada Owner", password: "correct horse battery" } }), 201);
  const me = await must<{ tenant_id: string }>(req.get("/v1/me"), 200);
  const tenant = me.tenant_id;
  // A checked phone and business, as an agent or Taskiem would give them.
  sql(`INSERT INTO tenant_verifications (id, tenant_id, level, method, actor) VALUES (gen_random_uuid(), '${tenant}', 'phone', 'test', 'test'), (gen_random_uuid(), '${tenant}', 'business', 'test', 'test')`);
  const allDay = { days: Array.from({ length: 7 }, () => [{ open: "00:00", close: "23:59" }, { open: "23:59", close: "00:30" }]) };
  await must(req.put("/v1/storefront", { headers: H, data: { slug: `pp-${letters()}`, draft: { name, area: "Yaba", address: "3 Montgomery Road, Yaba", hours: allDay } } }), 200);
  const made = await must<{ id: string }>(req.post("/v1/templates/storefront-order/instantiate", { headers: H, data: { params: {}, name: "Orders" } }), 201);
  await must(req.post(`/v1/workflows/${made.id}/versions/1/publish`, { headers: H }), 200);
  await must(req.put("/v1/storefront/actions/order", { headers: H, data: { enabled: true, payment: "after_accept", options: { pickup: true } } }), 200);
  await must(req.post("/v1/storefront/publish", { headers: H }), 200);
  sql(`UPDATE storefronts SET catalogue_synced_at = now() WHERE tenant_id = '${tenant}'`);
  sql(
    `INSERT INTO storefront_catalogue (storefront_id, row_id, tenant_id, position, item, table_name) SELECT s.id, x.row_id, s.tenant_id, x.position, x.item::jsonb, '' FROM storefronts s,
      (VALUES ('1', 0, '{"id":"1","name":"Jollof rice","price_kobo":450000,"available":true}'), ('2', 1, '{"id":"2","name":"Chapman","price_kobo":150000,"available":true}')) AS x(row_id, position, item)
      WHERE s.tenant_id = '${tenant}'`,
  );
  sql(`UPDATE storefront_snapshots SET model = jsonb_set(model, '{has_catalogue}', 'true') WHERE tenant_id = '${tenant}'`);
  await must(req.put("/v1/business-profile", { headers: H, data: { area: "Yaba", categories: ["food"], tagline: "Hot jollof every day" } }), 200);
  // The test server keeps no photos: a logo whose sizes are the app's own file.
  const media = sql(
    `INSERT INTO storefront_media (id, tenant_id, hash, width, height, type, urls, paths, moderation, created_by) VALUES (gen_random_uuid(), '${tenant}', 'pp-${letters()}', 512, 512, 'image/png', '{"160":"/brand/seal/mark-512.png","320":"/brand/seal/mark-512.png"}', '{}', 'approved', 'e2e') RETURNING id`,
  );
  sql(`UPDATE business_profile SET logo_media = '${media}' WHERE tenant_id = '${tenant}'`);
  // Its Discover settings read: the listing is written from all of it now.
  await must(req.get("/v1/settings/discover"), 200);
  return tenant;
}

/** Shared between the tests, in order. */

test("order together in a chat: a shared basket, each their own items, placed by one, share cards", async ({ browser }) => {
  test.setTimeout(300_000);
  const ada = await person(browser, "Ada Obi", 390);
  const bola = await person(browser, "Bola Ade", 360);
  const bolaID = (await must<{ user: { id: string } }>(bola.page.request.get("/v1/me"), 200)).user.id;

  // Ada and Bola chat (a request accepted).
  const adaChat = (await must<{ id: string }>(ada.page.request.post("/v1/me/people", { headers: H, data: { person_id: bolaID } }), 201)).id;
  await must(ada.page.request.post(`/v1/me/people/${adaChat}/messages`, { headers: H, data: { text: "Lunch together?" } }), 201);
  const req = await must<{ requests: { id: string }[] }>(bola.page.request.get("/v1/me/people/requests"), 200);
  const bolaChat = req.requests[0]?.id ?? "";
  await must(bola.page.request.post(`/v1/me/people/${bolaChat}/accept`, { headers: H }), 200);

  // A business with a shop, which Ada chats with.
  const biz = await (await browser.newContext()).newPage();
  const name = `Iya Bk Kitchen ${letters()}`;
  const tenant = await shop(biz.request, name);
  // It takes payment outside the app: the placer pays all (C2).
  sql(`UPDATE storefront_actions SET payment = 'none' WHERE tenant_id = '${tenant}' AND kind = 'order'`);
  const bizChat = (await must<{ id: string }>(ada.page.request.post("/v1/me/chats", { headers: H, data: { business_id: tenant, from: "shop" } }), 201)).id;
  await must(ada.page.request.post(`/v1/me/chats/${bizChat}/messages`, { headers: H, data: { text: "Hello" } }), 201);

  // Share → Order together → the business: a basket card in the chat.
  await ada.page.goto(`/you/people/${adaChat}`);
  await ada.page.getByTestId("people-share-button").click();
  const share = ada.page.getByTestId("people-share-sheet");
  await share.getByTestId("share-together").click();
  await check(ada.page, "Order together from…", 390);
  await share.getByTestId("share-business-row").filter({ hasText: name }).getByTestId("together-business").click();
  const adaCard = ada.page.locator('[data-testid="people-card"][data-type="basket"]').last();
  await expect(adaCard).toContainText(`Order together from ${name}`, { timeout: 15_000 });
  await expect(adaCard.getByTestId("basket-card-state")).toHaveText("Open: add what you want");

  // Ada adds her own items.
  await adaCard.getByTestId("basket-open").click();
  const sheet = ada.page.getByTestId("basket-sheet");
  await expect(sheet.getByTestId("basket-holder")).toContainText("You hold this basket");
  await check(ada.page, "a basket, empty", 390);
  await sheet.getByTestId("basket-add").click();
  await sheet.getByRole("button", { name: "One more Jollof rice" }).click();
  await check(ada.page, "the basket's shop", 390);
  await sheet.getByTestId("basket-save-mine").click();
  await expect(sheet.getByTestId("basket-person")).toHaveCount(1);
  await expect(sheet.getByTestId("basket-total")).toHaveText("₦4,500");

  // Bola sees the card and adds Chapman; Ada sees it live.
  await bola.page.goto(`/you/people/${bolaChat}`);
  const bolaCard = bola.page.locator('[data-testid="people-card"][data-type="basket"]').last();
  await expect(bolaCard).toContainText("1 person", { timeout: 15_000 });
  await bolaCard.getByTestId("basket-open").click();
  const bSheet = bola.page.getByTestId("basket-sheet");
  await expect(bSheet.getByTestId("basket-holder")).toContainText("holds this basket");
  await bSheet.getByTestId("basket-add").click();
  await bSheet.getByRole("button", { name: "One more Chapman" }).click();
  await bSheet.getByRole("button", { name: "One more Chapman" }).click();
  await bSheet.getByTestId("basket-save-mine").click();
  await expect(bSheet.getByTestId("basket-person")).toHaveCount(2);
  await check(bola.page, "a basket, as a member", 360);
  await expect(sheet.getByTestId("basket-person")).toHaveCount(2, { timeout: 20_000 });
  await expect(sheet.getByTestId("basket-total")).toHaveText("₦7,500");

  // C1: Ada chooses pick-up and closes; Bola can't change it; reopened and closed.
  await sheet.getByTestId("basket-fulfilment").selectOption("pickup");
  await sheet.getByTestId("basket-close").click();
  await expect(sheet.getByTestId("basket-reopen")).toBeVisible();
  await expect(bSheet.getByTestId("basket-add")).toHaveCount(0, { timeout: 20_000 });
  await sheet.getByTestId("basket-reopen").click();
  await expect(sheet.getByTestId("basket-close")).toBeVisible();
  await sheet.getByTestId("basket-close").click();

  // Placed as Ada's own order; the business takes no payment in the app: Ada pays all.
  await sheet.getByTestId("basket-place").click();
  const form = ada.page.getByTestId("basket-place-form");
  await expect(form).toContainText("takes no payment in the app, so you pay for everyone");
  await expect(form.getByTestId("basket-pay-each")).toHaveCount(0);
  await check(ada.page, "placing a basket", 390);
  await form.getByTestId("basket-place-send").click();
  // The card follows the order as the business's workflow moves it.
  await expect(sheet.getByTestId("basket")).toHaveAttribute("data-state", "placed", { timeout: 15_000 });
  await expect(bSheet.getByTestId("basket")).toHaveAttribute("data-state", "placed", { timeout: 20_000 });
  await expect(bSheet.getByTestId("basket-order-line")).not.toHaveText("");
  await expect(bSheet.getByTestId("basket-share")).toHaveCount(2);
  await check(bola.page, "a placed basket, as a member", 360);

  // The business: one order from Ada, whose lines are whose.
  const orders = await must<{ orders: { reference: string }[] }>(biz.request.get("/v1/storefront/orders"), 200);
  const ref = orders.orders[0]?.reference ?? "";
  const one = await must<{ order: { customer: { name: string; message: string } } }>(biz.request.get(`/v1/storefront/orders/${ref}?reveal=true`), 200);
  expect(one.order.customer.name).toBe("Ada Obi");
  expect(one.order.customer.message).toContain("Ordered together in Taskiem by 2 people.");
  expect(one.order.customer.message).toContain("Bola Ade: 2 × Chapman.");

  // "Your share" cards in one step: Ada's account asked for once.
  await sheet.getByTestId("basket-share-cards").click();
  await sheet.getByTestId("basket-acct-bank").fill("GTBank");
  await sheet.getByTestId("basket-acct-number").fill("0123456789");
  await sheet.getByTestId("basket-acct-name").fill("Ada Obi");
  await check(ada.page, "share cards' account", 390);
  await sheet.getByTestId("basket-acct-send").click();
  await expect(sheet.getByTestId("basket-share").filter({ hasText: "share card sent" })).toHaveCount(1, { timeout: 15_000 });
  await bola.page.keyboard.press("Escape");
  await expect(bola.page.locator('[data-testid="people-card"][data-type="split"]').last()).toContainText("₦3,000", { timeout: 20_000 });
});
