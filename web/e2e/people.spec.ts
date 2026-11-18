import { execFileSync } from "node:child_process";
import { createServer as createHttp, type Server as HttpServer } from "node:http";
import { expect, test, type APIRequestContext, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// People chatting with people, stage 5a, the web (docs/spec/people-chatting.md
// §5, §7, §8, §10.1, §11), on the real server: two people find each other by
// number and by a person's link, a request is accepted, they chat with
// ticks (and without read ticks once one turns them off), reply and delete
// for everyone; a stranger's scam is held and a warned message is marked;
// block and report; "Nobody" refuses a new chat; a product shared live and
// a "Your share" card marked paid. Phones at 390 and 360 px, both themes,
// axe. The people's codes go out on Taskiem's WhatsApp number to a fake
// Graph API on the port you.spec.ts and customer-chat.spec.ts use, so this
// file runs one test at a time and apart from them.
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

const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

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

/** Opens a bubble's menu and picks an entry. */
async function bubbleMenu(page: Page, text: string, entry: string) {
  const bubble = page.getByTestId("people-message").filter({ hasText: text }).last();
  await bubble.hover();
  await bubble.locator(".bubble-chevron").click({ force: true });
  await page.getByTestId("chat-more").getByRole("button", { name: entry, exact: true }).click();
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
const st: { ada?: Awaited<ReturnType<typeof person>>; bola?: Awaited<ReturnType<typeof person>>; chat?: string; adaLink?: string; business?: string } = {};

test("two people find each other by number, a request is accepted, and they chat with ticks", async ({ browser }) => {
  test.setTimeout(240_000);
  const ada = await person(browser, "Ada Obi", 390);
  const bola = await person(browser, "Bola Ade", 360);
  st.ada = ada;
  st.bola = bola;

  // Bola lets everyone find them by number, in You's settings.
  await bola.page.goto("/you/settings");
  await bola.page.getByTestId("you-people-privacy").click();
  await expect(bola.page.getByRole("heading", { name: "Privacy with people" })).toBeVisible();
  await expect(bola.page.getByTestId("privacy-find_by_number").getByLabel("My contacts")).toBeChecked();
  // The honest words, the app's: kept by Taskiem, its servers can open
  // them, not end-to-end encrypted, checks on strangers' messages, never ads
  // or AI, read only on a report or by law.
  const kept = bola.page.getByTestId("people-privacy-kept");
  await expect(kept).toContainText("kept by Taskiem");
  await expect(kept).toContainText("Taskiem’s servers can open them: they are not end-to-end encrypted.");
  await expect(kept).toContainText("Automated checks look for scams in messages from people you don’t know");
  await expect(kept).toContainText("never used for ads or to train AI");
  await expect(kept).toContainText("except a moderator when you report a chat, or when the law requires it");
  expect(((await bola.page.locator("body").textContent()) ?? "").replace(/they are not end-to-end encrypted/g, "")).not.toMatch(/end-to-end|encrypt/i);
  await check(bola.page, "/you/settings/people", 360);
  const saved = bola.page.waitForResponse((r) => r.url().endsWith("/v1/me/privacy/people") && r.request().method() === "PUT");
  await bola.page.getByTestId("privacy-find_by_number").getByLabel("Everyone").check();
  expect((await saved).status()).toBe(200);

  // Ada finds Bola by number in New chat, adds them, and writes.
  await ada.page.goto("/you");
  await ada.page.getByTestId("people-new-chat-open").click();
  const sheet = ada.page.getByTestId("people-new-chat");
  await expect(sheet.getByTestId("people-contacts-phone")).toHaveText(
    'Finding friends from your phone\'s contacts works in the Taskiem app on your phone, for people whose "Who can find me by my number" lets you find them.',
  );
  // A number nobody has: Not on Taskiem yet, with Invite.
  await sheet.getByTestId("people-phone").fill("0809 999 0001");
  await sheet.getByTestId("people-find").click();
  await expect(sheet.getByTestId("people-not-found")).toContainText("Not on Taskiem yet");
  await expect(sheet.getByTestId("people-invite-whatsapp")).toHaveAttribute("href", /wa\.me/);
  await sheet.getByTestId("people-phone").fill(bola.typed);
  await sheet.getByTestId("people-find").click();
  await expect(sheet.getByTestId("people-found-name")).toHaveText("Bola Ade");
  await check(ada.page, "New chat with someone found", 390);
  await sheet.getByTestId("people-found-add").click();
  await sheet.getByTestId("people-found-name-input").fill("Bola (school)");
  await sheet.getByTestId("people-found-save").click();
  await expect(sheet.getByText("In your contacts")).toBeVisible();
  await sheet.getByTestId("people-found-message").click();
  await expect(ada.page).toHaveURL(/\/you\/people\/[0-9a-f-]{36}$/);
  st.chat = /\/you\/people\/([0-9a-f-]{36})$/.exec(ada.page.url())?.[1];
  await expect(ada.page.getByRole("heading", { name: "Bola (school)", level: 2 })).toBeVisible();
  await ada.page.getByLabel("Message Bola (school)").fill("Hello Bola, it's Ada");
  await ada.page.getByLabel("Message Bola (school)").press("Enter");
  const first = ada.page.getByTestId("people-message").filter({ hasText: "Hello Bola, it's Ada" });
  await expect(first.getByTestId("people-ticks")).toHaveAttribute("data-ticks", /^(sent|delivered)$/);
  // The chat is read again after the first send: the waiting line at once
  // (well before the next look), and the head says nothing it shouldn't.
  await expect(ada.page.getByTestId("people-waiting")).toBeVisible({ timeout: 2500 });
  await check(ada.page, "/you/people/{id}, waiting", 390);

  // Bola: one request waiting, at the top of the Chats.
  await bola.page.goto("/you");
  await expect(bola.page.getByTestId("people-requests-entry")).toContainText("1");
  await check(bola.page, "/you with a request", 360);
  await bola.page.getByTestId("people-requests-entry").click();
  await expect(bola.page.getByTestId("people-request")).toHaveCount(1);
  await check(bola.page, "/you/people/requests", 360);
  await bola.page.getByTestId("people-request").click();
  await expect(bola.page).toHaveURL(/\/you\/people\/requests\/[0-9a-f-]{36}$/);
  await expect(bola.page.getByTestId("people-message").filter({ hasText: "Hello Bola" })).toBeVisible();
  await expect(bola.page.getByTestId("request-bar")).toBeVisible();
  await expect(bola.page.getByTestId("people-head-line")).toHaveText("Message request");
  await check(bola.page, "a request opened", 360);
  // Read while a request: Ada is not told.
  await ada.page.waitForTimeout(5000);
  await expect(first.getByTestId("people-ticks")).not.toHaveAttribute("data-ticks", "read");
  await bola.page.getByTestId("request-accept").click();
  await expect(bola.page).toHaveURL(/\/you\/people\/[0-9a-f-]{36}$/);
  await expect(bola.page.getByTestId("request-bar")).toHaveCount(0);
  // Accepted: Ada's message shows read (both have read ticks on).
  await expect(first.getByTestId("people-ticks")).toHaveAttribute("data-ticks", "read", { timeout: 20_000 });
  await bola.page.getByLabel("Message Ada Obi").fill("Hi Ada! Good to see you here");
  await bola.page.getByLabel("Message Ada Obi").press("Enter");
  await expect(ada.page.getByTestId("people-message").filter({ hasText: "Good to see you here" })).toBeVisible({ timeout: 20_000 });

  // A reply, quoted on the other side.
  await bubbleMenu(ada.page, "Good to see you here", "Reply");
  await expect(ada.page.getByTestId("people-replying")).toBeVisible();
  await ada.page.getByLabel("Message Bola (school)").fill("Same here");
  await ada.page.getByLabel("Message Bola (school)").press("Enter");
  const reply = bola.page.getByTestId("people-message").filter({ hasText: "Same here" });
  await expect(reply.getByTestId("people-quote")).toContainText("Good to see you here", { timeout: 20_000 });
  await check(bola.page, "a chat with a reply", 360);
  await check(ada.page, "a chat with ticks", 390);

  // Delete for everyone: gone from Bola's side too.
  await ada.page.getByLabel("Message Bola (school)").fill("Oops, wrong chat");
  await ada.page.getByLabel("Message Bola (school)").press("Enter");
  await expect(bola.page.getByTestId("people-message").filter({ hasText: "Oops, wrong chat" })).toBeVisible({ timeout: 20_000 });
  await bubbleMenu(ada.page, "Oops, wrong chat", "Delete");
  await ada.page.getByTestId("delete-for-everyone").click();
  await expect(ada.page.getByTestId("people-deleted")).toBeVisible();
  await expect(bola.page.getByTestId("people-deleted")).toHaveText(/This message was deleted/, { timeout: 20_000 });
  await expect(bola.page.getByTestId("people-message").filter({ hasText: "Oops, wrong chat" })).toHaveCount(0);

  // Read ticks off on Bola's side: Ada sees none, even for what was read.
  await bola.page.goto("/you/settings/people");
  const off = bola.page.waitForResponse((r) => r.url().endsWith("/v1/me/privacy/people") && r.request().method() === "PUT");
  await bola.page.getByTestId("privacy-read_ticks").uncheck();
  expect((await off).status()).toBe(200);
  await ada.page.getByLabel("Message Bola (school)").fill("Are you coming on Saturday?");
  await ada.page.getByLabel("Message Bola (school)").press("Enter");
  await bola.page.goto(`/you`);
  const row = bola.page.getByTestId("people-chat").filter({ hasText: "Ada Obi" });
  await expect(row).toContainText("Are you coming on Saturday?");
  await row.click();
  await expect(bola.page.getByTestId("people-message").filter({ hasText: "Are you coming" })).toBeVisible();
  await ada.page.waitForTimeout(5000);
  for (const t of await ada.page.getByTestId("people-message").filter({ has: ada.page.getByTestId("people-ticks") }).getByTestId("people-ticks").all())
    await expect(t).not.toHaveAttribute("data-ticks", "read");

  // Ada's one list: Bola's row, marked as a person, with no business mark.
  await ada.page.goto("/you");
  await expect(ada.page.getByTestId("people-chat").filter({ hasText: "Bola (school)" })).toBeVisible();
  await check(ada.page, "/you with a chat with a person", 390);
});

test("a person's link and QR code: opened signed out, a chat from it, and replaced", async ({ browser }) => {
  test.setTimeout(180_000);
  const ada = st.ada as NonNullable<typeof st.ada>;
  await ada.page.goto("/you");
  await ada.page.getByTestId("people-new-chat-open").click();
  await ada.page.getByTestId("people-show-link").click();
  await expect(ada.page.getByTestId("people-qr")).toBeVisible();
  const url = (await ada.page.getByTestId("people-link-url").textContent()) ?? "";
  expect(url).toMatch(/\/p\/[a-z0-9]{10}$/);
  const code = url.split("/p/")[1] ?? "";
  st.adaLink = code;
  // The QR code is an image the server draws.
  const qr = await ada.page.request.get("/v1/me/link/qr");
  expect(qr.headers()["content-type"]).toContain("image/svg+xml");
  await check(ada.page, "your link and QR code", 390);

  // Signed out: the name and Chat on Taskiem, which signs in first.
  const out = await (await browser.newContext({ viewport: { width: 360, height: 800 } })).newPage();
  await out.goto(`/p/${code}`);
  await expect(out.getByRole("heading", { name: "Ada Obi" })).toBeVisible();
  await check(out, "/p/{code} signed out", 360);
  await out.getByTestId("person-link-chat").click();
  await expect(out).toHaveURL(new RegExp(`/login\\?next=${encodeURIComponent(`/p/${code}`)}`));

  // Signed in, Chidi opens the link: Message opens a chat with Ada.
  const chidi = await person(browser, "Chidi Eze", 390);
  await chidi.page.goto(`/p/${code}`);
  await expect(chidi.page.getByTestId("people-found-name")).toHaveText("Ada Obi");
  await check(chidi.page, "/p/{code} signed in", 390);
  await chidi.page.getByTestId("people-found-message").click();
  await expect(chidi.page).toHaveURL(/\/you\/people\/[0-9a-f-]{36}$/);
  await chidi.page.getByLabel("Message Ada Obi").fill("Hi Ada, Chidi from the market");
  await chidi.page.getByLabel("Message Ada Obi").press("Enter");
  await expect(chidi.page.getByTestId("people-message").filter({ hasText: "Chidi from the market" })).toBeVisible();

  // Replaced: the old link no longer works.
  await ada.page.getByTestId("people-link-replace").click();
  await ada.page.getByTestId("confirm-ok").click();
  await expect(ada.page.getByTestId("people-link-url")).not.toHaveText(url);
  st.adaLink = ((await ada.page.getByTestId("people-link-url").textContent()) ?? "").split("/p/")[1];
  await out.goto(`/p/${code}`);
  await expect(out.getByTestId("person-link-gone")).toContainText("This link no longer works");
});

test("a scam is held, a warned message is marked, block and report, and Nobody refuses", async ({ browser }) => {
  test.setTimeout(180_000);
  const ada = st.ada as NonNullable<typeof st.ada>;
  const bola = st.bola as NonNullable<typeof st.bola>;
  const dayo = await person(browser, "Dayo Ola", 360);
  await dayo.page.goto(`/p/${st.adaLink}`);
  await dayo.page.getByTestId("people-found-message").click();
  await expect(dayo.page).toHaveURL(/\/you\/people\/[0-9a-f-]{36}$/);
  // A stranger asking for a code: held, said plainly to the sender.
  await dayo.page.getByLabel("Message Ada Obi").fill("You have won a prize! Send me the OTP to claim it");
  await dayo.page.getByLabel("Message Ada Obi").press("Enter");
  await expect(dayo.page.getByTestId("people-problem")).toContainText("This message was not sent: it looks like a scam");
  await check(dayo.page, "a held message", 360);
  // Asking for a transfer to an account: delivered, with the warning on Ada's side.
  await dayo.page.getByLabel("Message Ada Obi").fill("Please pay into my account 0123456789 at GTBank for the bag");
  await dayo.page.getByLabel("Message Ada Obi").press("Enter");
  await expect(dayo.page.getByTestId("people-message").filter({ hasText: "0123456789" })).toBeVisible();

  await ada.page.goto("/you/people/requests");
  await expect(ada.page.getByTestId("people-request")).toHaveCount(2);
  await ada.page.getByTestId("people-request").filter({ hasText: "Dayo Ola" }).click();
  const warned = ada.page.getByTestId("people-message").filter({ hasText: "0123456789" });
  await expect(warned.getByTestId("people-transfer-warning")).toBeVisible();
  await expect(ada.page.getByTestId("people-message").filter({ hasText: "prize" })).toHaveCount(0);
  await check(ada.page, "a request with a warning", 390);

  // Block and report: the consent sheet says the last 20 messages go.
  await ada.page.getByTestId("request-block").click();
  const rep = ada.page.getByTestId("people-report-sheet");
  await expect(rep).toContainText("Send the last 20 messages of this chat to Taskiem's moderators");
  await rep.getByLabel("They are trying to cheat me").check();
  await check(ada.page, "block and report", 390);
  await expect(rep.getByTestId("people-report-send")).toBeDisabled();
  await rep.getByTestId("people-report-consent").check();
  await rep.getByTestId("people-report-send").click();
  await expect(ada.page).toHaveURL(/\/you\/people\/requests$/);
  await expect(ada.page.getByTestId("people-request")).toHaveCount(1);
  expect(Number(sql(`SELECT count(*) FROM people_reports`))).toBeGreaterThan(0);
  // In the blocked list, and unblocked from it.
  await ada.page.goto("/you/settings/blocked");
  const blocked = ada.page.getByTestId("people-blocked").filter({ hasText: "Dayo Ola" });
  await expect(blocked).toBeVisible();
  await check(ada.page, "/you/settings/blocked", 390);
  await blocked.getByRole("button", { name: /Unblock/ }).click();
  await expect(blocked).toHaveCount(0);

  // Bola: "Who can message me: Nobody" refuses Dayo's new chat.
  await bola.page.goto("/you/settings/people");
  const nobody = bola.page.waitForResponse((r) => r.url().endsWith("/v1/me/privacy/people") && r.request().method() === "PUT");
  await bola.page.getByTestId("privacy-message").getByLabel("Nobody").check();
  expect((await nobody).status()).toBe(200);
  await dayo.page.goto("/you");
  await dayo.page.getByTestId("people-new-chat-open").click();
  await dayo.page.getByTestId("people-phone").fill(bola.typed);
  await dayo.page.getByTestId("people-find").click();
  await dayo.page.getByTestId("people-found-message").click();
  await expect(dayo.page.getByTestId("people-start-problem")).toContainText("not taking messages");
  // Back to everyone, for the next test.
  await bola.page.getByTestId("privacy-message").getByLabel("Everyone, as a request").check();
});

test("a product shared live, a business from Discover, and a 'Your share' card marked paid", async ({ browser }) => {
  test.setTimeout(240_000);
  const ada = st.ada as NonNullable<typeof st.ada>;
  const bola = st.bola as NonNullable<typeof st.bola>;
  const biz = await (await browser.newContext()).newPage();
  const name = `Iya Pp Kitchen ${letters()}`;
  const tenant = await shop(biz.request, name);
  st.business = name;

  // Ada chats with the business (from Discover), so its shop is hers to share.
  // (Discover's index takes the business in a moment after its profile.)
  await expect.poll(async () => (await ada.page.request.get(`/v1/discover/${tenant}`)).status(), { timeout: 60_000 }).toBe(200);
  await ada.page.goto(`/you/discover/${tenant}`);
  await ada.page.getByTestId("discover-chat").click({ timeout: 15_000 });
  await expect(ada.page).toHaveURL(/\/you\/chats\/[0-9a-f-]{36}$/);

  // In the chat with Bola: Share, a product, from that business's shop.
  await ada.page.goto(`/you/people/${st.chat}`);
  await ada.page.getByTestId("people-share-button").click();
  const share = ada.page.getByTestId("people-share-sheet");
  await check(ada.page, "the share sheet", 390);
  await share.getByTestId("share-product").click();
  await share.getByTestId("share-business-row").filter({ hasText: name }).getByRole("button").click();
  await share.getByTestId("share-product-row").filter({ hasText: "Jollof rice" }).getByRole("button", { name: /Share/ }).click();
  const card = bola.page.getByTestId("people-card").filter({ hasText: "Jollof rice" });
  await bola.page.goto(`/you`);
  await bola.page.getByTestId("people-chat").filter({ hasText: "Ada Obi" }).click();
  await expect(card).toBeVisible({ timeout: 20_000 });
  // Opened live: the price as it is now, and Chat with the business.
  sql(`UPDATE storefront_catalogue SET item = jsonb_set(item, '{price_kobo}', '500000') WHERE tenant_id = '${tenant}' AND row_id = '1'`);
  await card.getByTestId("people-card-open").click();
  const live = bola.page.getByTestId("people-card-sheet");
  await expect(live.getByTestId("people-card-price")).toHaveText("₦5,000 now (it was ₦4,500 when shared)");
  await expect(live.getByTestId("people-card-stock")).toHaveText("Available");
  await expect(live.getByTestId("people-card-order")).toHaveText("Order from their shop");
  await expect(live.getByTestId("people-card-order")).toHaveAttribute("href", new RegExp(`business=${tenant}&from=shared&tool=shop`));
  await check(bola.page, "a shared product opened", 360);
  // Order from their shop: Bola's own chat with the business, started from
  // a shared card (from=shared), its shop open.
  const started = bola.page.waitForRequest((r) => r.url().endsWith("/v1/me/chats") && r.method() === "POST");
  const withAda = bola.page.url();
  await live.getByTestId("people-card-order").click();
  expect((await started).postDataJSON()).toEqual({ business_id: tenant, from: "shared" });
  await expect(bola.page).toHaveURL(/\/you\/chats\/[0-9a-f-]{36}$/);
  await expect(bola.page.getByTestId("shop-sheet")).toBeVisible();
  await bola.page.goto(withAda);

  // A business shared from Discover: pick the chat, and it is sent.
  await ada.page.goto(`/you/discover/${tenant}`);
  await ada.page.getByTestId("discover-share").click();
  await ada.page.getByTestId("share-to-person").getByTestId("share-to-row").filter({ hasText: "Bola" }).getByRole("button").click();
  await expect(ada.page).toHaveURL(new RegExp(`/you/people/${st.chat}$`));
  await expect(ada.page.getByTestId("people-card").filter({ hasText: name }).first()).toBeVisible();
  // Opened, a business's card offers Chat with them, from a shared card.
  await ada.page.locator("[data-testid=people-card][data-type=business]").filter({ hasText: name }).first().getByTestId("people-card-open").click();
  const bizCard = ada.page.getByTestId("people-card-sheet");
  await expect(bizCard.getByTestId("people-card-chat")).toHaveText("Chat with them");
  await expect(bizCard.getByTestId("people-card-chat")).toHaveAttribute("href", new RegExp(`business=${tenant}&from=shared$`));
  await bizCard.getByRole("button", { name: /Close/ }).first().click();

  // Ada's bank account for share cards, saved in Settings.
  await ada.page.goto("/you/settings/payout");
  await ada.page.getByTestId("account-bank").fill("GTBank");
  await ada.page.getByTestId("account-number").fill("0123 456 789");
  await ada.page.getByTestId("account-name").fill("Ada Obi");
  await check(ada.page, "/you/settings/payout", 390);
  await ada.page.getByTestId("payout-save").click();
  await expect(ada.page.getByText("Saved").first()).toBeVisible();

  // Your share: ₦3,000 for the cake, to the saved account.
  await ada.page.goto(`/you/people/${st.chat}`);
  await ada.page.getByTestId("people-share-button").click();
  await ada.page.getByTestId("share-split").click();
  const split = ada.page.getByTestId("people-split-sheet");
  await split.getByTestId("split-amount").fill("3,000");
  await split.getByTestId("split-what").fill("the cake");
  await expect(split.getByTestId("split-saved-account")).toContainText("Ada Obi, GTBank 0123456789");
  await check(ada.page, "Your share", 390);
  await split.getByTestId("split-send").click();
  const owed = bola.page.getByTestId("people-card").filter({ hasText: "Your share: ₦3,000 for the cake" });
  await expect(owed).toBeVisible({ timeout: 20_000 });
  await expect(owed).toContainText("Pay to Ada Obi, GTBank 0123456789");
  await check(bola.page, "a share card", 360);
  await owed.getByTestId("split-paid").click();
  await expect(owed.getByText("Paid", { exact: true })).toBeVisible();
  await expect(ada.page.getByTestId("people-card").filter({ hasText: "Your share" }).getByText("Paid", { exact: true })).toBeVisible({ timeout: 20_000 });
});

test("a share asks what for, a refused card is not called sent, a stranger's share is warned, and a forward keeps what failed", async ({ browser }) => {
  test.setTimeout(240_000);
  const ada = st.ada as NonNullable<typeof st.ada>;
  const eko = await person(browser, "Eko Bassey", 360);
  await eko.page.goto(`/p/${st.adaLink}`);
  await eko.page.getByTestId("people-found-message").click();
  await expect(eko.page).toHaveURL(/\/you\/people\/[0-9a-f-]{36}$/);

  // Your share: at least ₦1, and what it is for, before Send.
  await eko.page.getByTestId("people-share-button").click();
  await eko.page.getByTestId("share-split").click();
  const split = eko.page.getByTestId("people-split-sheet");
  const send = split.getByTestId("split-send");
  await split.getByTestId("split-amount").fill("0.50");
  await expect(split).toContainText("At least ₦1");
  await expect(send).toBeDisabled();
  await split.getByTestId("split-amount").fill("2,500");
  await split.getByTestId("account-bank").fill("Opay");
  await split.getByTestId("account-number").fill("8012345678");
  await split.getByTestId("account-name").fill("Eko Bassey");
  await expect(send).toBeDisabled();
  await expect(split.getByLabel("What for")).toHaveAttribute("required", "");
  await check(eko.page, "Your share, what for required", 360);
  // A fee for a prize, to a stranger: held. Said in the chat, never "Sent".
  await split.getByTestId("split-what").fill("Processing fee to claim your prize");
  await expect(send).toBeEnabled();
  await send.click();
  await expect(eko.page.getByTestId("people-problem")).toContainText("not sent");
  await expect(eko.page.getByTestId("people-split-sheet")).toHaveCount(0);
  await expect(eko.page.locator(".toasts .toast-text").filter({ hasText: /^Sent$/ })).toHaveCount(0);
  await expect(eko.page.getByTestId("people-card")).toHaveCount(0);
  // A share for the cake goes, as a request, and the waiting line says so.
  await eko.page.getByTestId("people-share-button").click();
  await eko.page.getByTestId("share-split").click();
  await split.getByTestId("split-amount").fill("2,500");
  await split.getByTestId("account-bank").fill("Opay");
  await split.getByTestId("account-number").fill("8012345678");
  await split.getByTestId("account-name").fill("Eko Bassey");
  await split.getByTestId("split-what").fill("the jollof");
  await split.getByTestId("split-send").click();
  await expect(eko.page.getByTestId("people-card").filter({ hasText: "Your share: ₦2,500 for the jollof" })).toBeVisible();
  await expect(eko.page.locator(".toasts .toast-text").filter({ hasText: /^Sent$/ })).toHaveCount(1);
  await expect(eko.page.getByTestId("people-waiting")).toBeVisible({ timeout: 2500 });

  // Ada: the request's share card carries the people's transfer warning,
  // Copy number, and no Mark as paid while it is a request.
  await ada.page.goto("/you/people/requests");
  await ada.page.getByTestId("people-request").filter({ hasText: "Eko Bassey" }).click();
  const owed = ada.page.getByTestId("people-message").filter({ hasText: "the jollof" });
  await expect(owed.getByTestId("people-transfer-warning")).toHaveText("Be careful with requests to send money to an account. Taskiem never asks you to. Only pay people you know.");
  await expect(owed.getByTestId("split-copy")).toHaveText("Copy number");
  await expect(owed.getByTestId("split-paid")).toHaveCount(0);
  await check(ada.page, "a stranger's share card, warned", 390);
  await ada.page.getByTestId("request-accept").click();
  await expect(ada.page).toHaveURL(/\/you\/people\/[0-9a-f-]{36}$/);
  const ekoChat = /\/you\/people\/([0-9a-f-]{36})$/.exec(ada.page.url())?.[1] ?? "";
  // The info sheet: the honest words, with no lock.
  await ada.page.getByRole("button", { name: "More about this chat" }).click();
  await ada.page.getByTestId("people-info").click();
  await expect(ada.page.getByTestId("people-kept")).toContainText("they are not end-to-end encrypted");
  await ada.page.getByTestId("people-info-sheet").getByRole("button", { name: /Close/ }).first().click();

  // Chidi's request accepted too, so Ada has two chats to forward to.
  await ada.page.goto("/you/people/requests");
  await ada.page.getByTestId("people-request").filter({ hasText: "Chidi Eze" }).click();
  await ada.page.getByTestId("request-accept").click();
  await expect(ada.page).toHaveURL(/\/you\/people\/[0-9a-f-]{36}$/);

  // A forward to Chidi and Eko, Eko's failing: one forwarded, Eko alone
  // still picked to try again.
  await ada.page.goto(`/you/people/${st.chat}`);
  await ada.page.getByLabel("Message Bola (school)").fill("Forward me along");
  await ada.page.getByLabel("Message Bola (school)").press("Enter");
  await expect(ada.page.getByTestId("people-message").filter({ hasText: "Forward me along" }).getByTestId("people-ticks")).toBeVisible();
  await ada.page.route("**/v1/me/people/forward", async (route) => {
    const res = await route.fetch();
    const out = (await res.json()) as { sent: { chat_id?: string }[]; failed: { chat_id: string; error: string }[] };
    out.sent = out.sent.slice(0, 1);
    out.failed = [{ chat_id: ekoChat, error: "This account can't get messages right now." }];
    await route.fulfill({ response: res, json: out });
  });
  await bubbleMenu(ada.page, "Forward me along", "Forward");
  const fw = ada.page.getByTestId("people-forward-sheet");
  const picks = fw.getByTestId("people-forward-pick");
  await expect(picks.first()).toBeVisible();
  await expect(picks).toHaveCount(2);
  for (let i = 0; i < 2; i++) await picks.nth(i).check();
  await fw.getByTestId("people-forward-send").click();
  await expect(fw.getByRole("alert")).toContainText("Eko Bassey: This account can't get messages right now.");
  await expect(ada.page.locator(".toasts .toast-text").filter({ hasText: /^Forwarded$/ })).toHaveCount(1);
  await expect(fw.locator("label").filter({ hasText: "Eko Bassey" }).getByTestId("people-forward-pick")).toBeChecked();
  await expect(fw.locator("input[data-testid=people-forward-pick]:checked")).toHaveCount(1);
  await ada.page.unroute("**/v1/me/people/forward");
});

/** The browser's push, standing in (as chat-push.spec.ts): one subscription kept in this browser's storage. */
const PUSH_STAND_IN = () => {
  const KEY = "e2e-push-sub";
  const b64 = (bytes: number[]) => btoa(String.fromCharCode(...bytes)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const read = () => {
    try {
      return JSON.parse(localStorage.getItem(KEY) ?? "null") as { endpoint: string; keys: { p256dh: string; auth: string } } | null;
    } catch {
      return null;
    }
  };
  const wrap = (j: { endpoint: string; keys: { p256dh: string; auth: string } }) => ({
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
        keys: { p256dh: b64([4, ...Array.from({ length: 64 }, (_, i) => (i * 7 + 3) % 256)]), auth: b64(Array.from({ length: 16 }, (_, i) => i + 1)) },
      };
      localStorage.setItem(KEY, JSON.stringify(j));
    }
    return wrap(j) as unknown as PushSubscription;
  };
};
const NOTIFY_GRANTED = () => {
  Object.defineProperty(Notification, "permission", { get: () => "granted", configurable: true });
  Notification.requestPermission = async () => "granted";
};

test("live: a message, typing and presence without a reload; search, starred, a place, the trust line and the push preview", async ({ browser }) => {
  test.setTimeout(300_000);
  const ada = st.ada as NonNullable<typeof st.ada>;
  const bola = st.bola as NonNullable<typeof st.bola>;

  // Bola's Chats list, then nothing read again but by the live connection.
  await bola.page.goto("/you");
  const adaRow = bola.page.getByTestId("people-chat").filter({ hasText: "Ada Obi" });
  await expect(adaRow).toBeVisible();
  await bola.page.route("**/v1/me/conversations*", (r) => r.abort());
  await ada.page.goto(`/you/people/${st.chat}`);
  const box = ada.page.getByLabel("Message Bola (school)");
  // Typing: shown on Bola's row while Ada types.
  // The server passes typing on at most every 3 seconds per chat: the
  // last test's words are well behind.
  await ada.page.waitForTimeout(3500);
  await box.fill("Bring the zobo");
  await expect(adaRow.getByTestId("people-row-typing")).toHaveText("typing…", { timeout: 10_000 });
  await check(bola.page, "/you with someone typing", 360);
  await box.fill("");
  await expect(adaRow.getByTestId("people-row-typing")).toHaveCount(0, { timeout: 10_000 });
  // A message: the row's last line changes without a reload.
  await box.fill("Bring the zobo drink to the party tomorrow");
  await box.press("Enter");
  await expect(adaRow).toContainText("Bring the zobo drink", { timeout: 10_000 });
  await bola.page.unroute("**/v1/me/conversations*");

  // In the chat: the head says typing…, and the message comes live (the
  // chat's own reading is stopped once it is open).
  await adaRow.click();
  await expect(bola.page.getByTestId("people-message").filter({ hasText: "zobo drink" })).toBeVisible();
  await bola.page.route("**/v1/me/people/*/messages", (r) => (r.request().method() === "GET" ? r.abort() : r.continue()));
  await ada.page.waitForTimeout(3500);
  await box.fill("And the puff");
  await expect(bola.page.getByTestId("people-typing")).toHaveText("typing…", { timeout: 10_000 });
  await check(bola.page, "a chat with someone typing", 360);
  await box.press("Enter");
  await expect(bola.page.getByTestId("people-message").filter({ hasText: "And the puff" })).toBeVisible({ timeout: 10_000 });
  await expect(bola.page.getByTestId("people-typing")).toHaveCount(0, { timeout: 10_000 });
  await bola.page.unroute("**/v1/me/people/*/messages");

  // Presence: the tab going to the back says this connection is away, and
  // coming back that it is in front.
  const away = bola.page.waitForRequest((r) => r.url().endsWith("/v1/me/presence") && r.method() === "POST" && r.postDataJSON()?.online === false);
  await bola.page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect((await away).postDataJSON()).toMatchObject({ online: false, connection: expect.stringMatching(/^[0-9a-f-]{36}$/) });
  const back = bola.page.waitForRequest((r) => r.url().endsWith("/v1/me/presence") && r.method() === "POST" && r.postDataJSON()?.online === true);
  await bola.page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect((await back).postDataJSON()).toMatchObject({ online: true });

  // Search: a word's start finds Ada's message; it opens the chat at it.
  await bola.page.goto("/you");
  await bola.page.getByTestId("people-search-open").click();
  await expect(bola.page).toHaveURL(/\/you\/people\/search/);
  await bola.page.getByTestId("people-search-q").fill("zob");
  const hit = bola.page.getByTestId("people-found").filter({ hasText: "Ada Obi" });
  await expect(hit).toHaveCount(1);
  await expect(hit.locator("strong")).toHaveText("zobo");
  await expect(bola.page.getByTestId("people-search-count")).toHaveText("1 message found");
  await check(bola.page, "/you/people/search", 360);
  await bola.page.getByTestId("people-search-q").fill("nothingsaidlikethis");
  await expect(bola.page.getByTestId("people-search-count")).toHaveText("No messages found.");
  await bola.page.getByTestId("people-search-q").fill("zobo drink");
  await hit.click();
  await expect(bola.page).toHaveURL(/\/you\/people\/[0-9a-f-]{36}\?at=/);
  const atZobo = bola.page.getByTestId("people-message").filter({ hasText: "zobo drink" });
  await expect(atZobo).toHaveClass(/flash/);
  await expect(atZobo).toBeInViewport();

  // Starred: Bola stars it; the starred list opens the chat at it.
  await bubbleMenu(bola.page, "zobo drink", "Star");
  await expect(atZobo.getByRole("img", { name: "Starred" })).toBeVisible();
  await bola.page.goto("/you/settings");
  await bola.page.getByTestId("you-people-starred").click();
  await expect(bola.page).toHaveURL(/\/you\/people\/starred$/);
  const starred = bola.page.getByTestId("people-starred-row");
  await expect(starred).toHaveCount(1);
  await expect(starred).toContainText("Bring the zobo drink");
  await check(bola.page, "/you/people/starred", 360);
  await starred.click();
  await expect(bola.page).toHaveURL(/\?at=/);
  await expect(bola.page.getByTestId("people-message").filter({ hasText: "zobo drink" })).toHaveClass(/flash/);

  // A place: Bola's browser lets Taskiem see where they are (at a fixed
  // place); asked for only on Bola's tap, seen before it is sent, with a name.
  // (Ada has sent all a new account may in a minute: Bola sends it.)
  await bola.page.context().grantPermissions(["geolocation"]);
  await bola.page.context().setGeolocation({ latitude: 6.5095, longitude: 3.3711 });
  await bola.page.getByTestId("people-share-button").click();
  await bola.page.getByTestId("share-place").click();
  const place = bola.page.getByTestId("people-place-sheet");
  await expect(place.getByTestId("place-preview")).toHaveCount(0);
  await place.getByTestId("place-find").click();
  await expect(place.getByTestId("place-preview")).toContainText("6.50950, 3.37110");
  await expect(place.getByTestId("place-map")).toHaveAttribute("href", /mlat=6\.5095&mlon=3\.3711/);
  await place.getByTestId("place-label").fill("The gate on Herbert Macaulay");
  await check(bola.page, "send a place", 360);
  const placed = bola.page.waitForResponse((r) => /\/v1\/me\/people\/[^/]+\/messages$/.test(r.url()) && r.request().method() === "POST");
  await place.getByTestId("place-send").click();
  const placedRes = await placed;
  expect(placedRes.status(), await placedRes.text()).toBe(201);
  expect(placedRes.request().postDataJSON()).toMatchObject({ location: { lat: 6.5095, lng: 3.3711, label: "The gate on Herbert Macaulay" } });
  await expect(bola.page.getByTestId("people-place").filter({ hasText: "The gate on Herbert Macaulay" })).toBeVisible();
  const sentPlace = ada.page.getByTestId("people-place").filter({ hasText: "The gate on Herbert Macaulay" });
  await expect(sentPlace).toBeVisible({ timeout: 10_000 });
  await expect(sentPlace).toHaveAttribute("href", /openstreetmap\.org\/\?mlat=6\.5095&mlon=3\.3711/);
  await check(ada.page, "a place in a chat", 390);
  // Ada's browser does not let Taskiem see where she is: said plainly.
  await ada.page.context().clearPermissions();
  await ada.page.getByTestId("people-share-button").click();
  await ada.page.getByTestId("share-place").click();
  await ada.page.getByTestId("people-place-sheet").getByTestId("place-find").click();
  await expect(ada.page.getByTestId("place-problem")).toContainText("this browser blocked it");
  await check(ada.page, "a place refused", 390);
  await ada.page.getByTestId("people-place-sheet").getByRole("button", { name: /Close/ }).first().click();

  // A suspended sender's refusal points to Your standing (the server's words, stood in).
  await bola.page.route("**/v1/me/people/*/messages", (r) =>
    r.request().method() === "POST"
      ? r.fulfill({
          status: 403,
          json: { error: "Taskiem's moderators have stopped your account from messaging people for now. See Your standing in Settings to find out why and to appeal." },
        })
      : r.continue(),
  );
  await bola.page.getByLabel("Message Ada Obi").fill("Are you there?");
  await bola.page.getByLabel("Message Ada Obi").press("Enter");
  await expect(bola.page.getByTestId("people-problem")).toContainText("stopped your account");
  await expect(bola.page.getByTestId("people-problem-standing")).toHaveAttribute("href", "/you/settings/standing");
  await bola.page.unroute("**/v1/me/people/*/messages");

  // The one Chats list: a business row carries its trust line, as its chat's head.
  await ada.page.goto("/you");
  const bizRow = ada.page.getByTestId("person-chat").filter({ hasText: st.business ?? "" });
  await expect(bizRow.getByTestId("chat-row-trust")).toHaveText("Phone checked · Business checked · New on Taskiem");
  await check(ada.page, "/you with a business's trust line", 390);

  // The privacy page links Your standing and the published counts.
  await ada.page.goto("/you/settings/privacy");
  await expect(ada.page.getByTestId("privacy-standing")).toHaveAttribute("href", "/you/settings/standing");
  await expect(ada.page.getByTestId("privacy-transparency")).toHaveAttribute("href", "/transparency");

  // This browser's notifications: on, then the words hidden in them.
  const efe = await person(browser, "Efe Ojo", 390, { permissions: ["notifications"], serviceWorkers: "allow" }, [PUSH_STAND_IN, NOTIFY_GRANTED]);
  await efe.page.goto("/you/settings");
  const mine = efe.page.getByTestId("you-browser-push");
  await mine.getByTestId("you-push-on").click();
  const preview = mine.getByTestId("you-push-preview");
  await expect(preview).toBeChecked();
  await expect(mine.getByTestId("you-push-preview-words")).toContainText("who wrote and the start of the message");
  await check(efe.page, "/you/settings with this browser's notifications", 390);
  const patched = efe.page.waitForResponse((r) => /\/v1\/me\/push\/[0-9a-f-]{36}\?side=you$/.test(r.url()) && r.request().method() === "PATCH");
  await preview.click();
  expect((await patched).status()).toBe(200);
  await expect(preview).not.toBeChecked();
  await expect(mine.getByTestId("you-push-preview-words")).toContainText("Someone wrote to you");
  const devices = (await (await efe.page.request.get("/v1/me/push?side=you", { headers: H })).json()) as { devices: { preview: boolean }[] };
  expect(devices.devices.map((d) => d.preview)).toEqual([false]);
  // Read again: the switch stays off.
  await efe.page.reload();
  await expect(efe.page.getByTestId("you-push-preview")).not.toBeChecked();
});
