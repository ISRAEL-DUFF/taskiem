import { execFileSync } from "node:child_process";
import { expect, test, type Browser, type BrowserContext, type Page, type Request, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Reading a photo with AI (I3): when this browser could not find what it
// looked for in a photo (handwriting, mostly) and an owner turned "Read
// handwriting with AI" on in Settings, "Read it with AI" is offered; only
// on that tap is the photo sent (POST /v1/photo-text, multipart), and the
// words that come back go on as the browser's would (from: "photo"). Off,
// an owner is told where to turn it on. Here: the switch (an owner turns
// it on, an admin cannot), the Pay sheet, Pay several from a photo of a
// list, a refusal, and in a chat with a linked business a photo's Pay
// this account and Save the account.
//
// The browser's reader is stood in for as photo-read.spec.ts does: the
// worker file (/ocr/v7/worker.min.js) is answered with a small worker that
// "reads" the words after WORDS: in the photo's bytes. The e2e server has
// no AI model (so `available` is false) and no payments account: the
// setting's GET is the server's own with `available` turned true, its PUT
// is the server's own, and POST /v1/photo-text and the payments API are
// stood in for.

const H = { "X-Taskiem-Request": "1" };
const PASSWORD = "correct horse battery";
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (r: Route, status: number, body: unknown) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
const shots = process.env.PHOTO_SHOTS;

async function signUp(page: Page, tenant = `Photo AI ${letters()}`) {
  const email = `${letters()}-${Math.random().toString(36).slice(2, 8)}@photo-ai.test`;
  const r = await page.request.post("/v1/signup", { headers: H, data: { tenant, email, name: `${tenant.split(" ")[0]} Owner`, password: PASSWORD } });
  expect(r.status(), await r.text()).toBe(201);
  return email;
}

const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAGAAAABACAIAAABqVuVZAAAAhklEQVR42u3XsQkAIAxFwQxmYeX+g1iKdTYQLEQIB3+CgwRe7DXtsEAACNBfoDZ67QF6DOTEKAACBAgQIECAAJnU0GJazA8CBAgQIAMECBAgLabFtJgTA0QBECBAgAABAgRIahQCcmJ+ECBAgAwQIECAAGmx2qkByB0BAgQIECBAgAAZoOslcMuY5zRJt8wAAAAASUVORK5CYII=",
  "base64",
);
const photo = (words: string) => Buffer.concat([PNG, Buffer.from(`WORDS:${words}`)]);
const file = (words: string, name = "note.png") => ({ name, mimeType: "image/png", buffer: photo(words) });

const FAKE_WORKER = `
self.onmessage = ({ data }) => {
  const { workerId, jobId, action, payload } = data;
  let out = {};
  if (action === "recognize") {
    const s = new TextDecoder().decode(payload.image);
    const at = s.indexOf("WORDS:");
    out = { text: at < 0 ? "" : s.slice(at + 6) };
  }
  self.postMessage({ workerId, jobId, action, status: "resolve", data: out });
};`;

async function standIn(context: BrowserContext) {
  await context.route("**/ocr/v7/**", (r) =>
    r.request().url().endsWith("/worker.min.js") ? r.fulfill({ status: 200, contentType: "text/javascript", body: FAKE_WORKER }) : r.fulfill({ status: 404, body: "" }),
  );
}

/** The setting as the server keeps it, with this Taskiem able to read
 * photos with AI (the e2e server has no model). */
async function available(page: Page) {
  await page.route("**/v1/settings/ai-photo-reading", async (r) => {
    const res = await r.fetch();
    const body = (await res.json()) as Record<string, unknown>;
    return json(r, res.status(), res.ok() ? { ...body, available: true } : body);
  });
}

/** Turns it on or off as the owner, on the server. */
async function turn(page: Page, on: boolean) {
  const r = await page.request.put("/v1/settings/ai-photo-reading", { headers: H, data: { ai_photo_reading: on } });
  expect(r.status(), await r.text()).toBe(200);
}

/** POST /v1/photo-text, answered here; what it was sent. */
async function photoText(page: Page, answer: (r: Route) => Promise<void>) {
  const sent: Request[] = [];
  await page.route("**/v1/photo-text", (r) => {
    sent.push(r.request());
    return answer(r);
  });
  return sent;
}

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

/** The photo went as the multipart part "photo", whole. */
function carriesPhoto(q: Request | undefined, words: string) {
  if (!q) throw new Error("the photo was not sent");
  const body = q.postDataBuffer() ?? Buffer.alloc(0);
  expect(q.method()).toBe("POST");
  expect(q.headers()["content-type"]).toMatch(/^multipart\/form-data; boundary=/);
  expect(body.includes(Buffer.from('name="photo"'))).toBe(true);
  expect(body.includes(photo(words))).toBe(true);
}

/** Follows the device's theme, so emulateMedia switches it. */
const followTheme = (page: Page) =>
  page.addInitScript(() => {
    try {
      localStorage.setItem("taskiem-theme", "system");
    } catch {
      // the default theme, then
    }
  });

async function noAxe(page: Page, what: string, shot?: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(300); // colours ease between themes
    const vs = await axe(page);
    expect(vs, describe(`${what} (${scheme})`, vs)).toEqual([]);
    if (shots && shot) await page.screenshot({ path: `${shots}/${shot}-${scheme}.png` });
  }
  await page.emulateMedia({ colorScheme: "light" });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(page.viewportSize()?.width ?? 390);
}

const payment = (over: Record<string, unknown> = {}) => ({
  id: "0192e2e0-0000-7000-8000-00000000a1e1", saved_id: null, personal: false, payee: "Account ••6789", bank_name: "Access Bank", account: "••6789",
  account_name: "CHIDI OKEKE", amount_kobo: 4_500_000, purpose: "stock", purpose_note: "", note: "", first_payment: true, changed_account: false, needs_ok: "",
  status: "prepared", reason: null, fee_kobo: null, run_id: null, started_from: "tools", prepared_by: "user:ada", confirmed_by: null,
  created_at: new Date().toISOString(), confirmed_at: null, finished_at: null, again: false, ...over,
});
const banks = [
  { code: "058", name: "Guaranty Trust Bank", likely: true },
  { code: "044", name: "Access Bank", likely: true },
];

const WORDS = "CHIDI OKAFOR\nAcct No: 0123456789\nAccess Bank\nN45,000";
// What the browser makes of handwriting: no account in it.
const SCRAWL = "Chd1 Okf\n0l2E 4S6 7B9";

async function payApi(page: Page) {
  const calls: { method: string; path: string; body: Record<string, unknown> }[] = [];
  await page.route(
    (u) => u.pathname.startsWith("/v1/payments") || u.pathname.startsWith("/v1/pay-limits"),
    async (r) => {
      const req = r.request();
      const url = new URL(req.url());
      const body = (req.postDataJSON() ?? {}) as Record<string, unknown>;
      calls.push({ method: req.method(), path: url.pathname, body });
      if (url.pathname === "/v1/pay-limits") return json(r, 200, { limits: [] });
      if (url.pathname === "/v1/payments" && req.method() === "GET") return json(r, 200, { payments: [] });
      if (url.pathname === "/v1/payments/read") {
        if (String(body.text).includes("0123456789"))
          return json(r, 200, { found: true, account_number: "0123456789", bank_name: "Access Bank", bank_code: "044", amount_kobo: 4_500_000, name: "Chidi Okafor" });
        return json(r, 200, { found: false });
      }
      if (url.pathname === "/v1/payments/banks") return json(r, 200, { from: "paystack", banks });
      if (url.pathname === "/v1/payments" && req.method() === "POST") return json(r, 201, { payment: payment({ started_from: body.from }) });
      if (url.pathname.endsWith("/cancel")) return json(r, 200, { payment: payment({ status: "cancelled" }) });
      return json(r, 404, { error: "not found" });
    },
  );
  await page.route("**/v1/chat/actions/*/choices", (r) => json(r, 200, { fields: { who: ["@bayo"] } }));
  return { calls, reads: () => calls.filter((c) => c.path === "/v1/payments/read").map((c) => c.body) };
}

async function signIn(page: Page, email: string) {
  await page.goto("/login?with=email");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

test("Settings: an owner turns reading handwriting with AI on and off; an admin sees it and cannot", async ({ page, browser }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await followTheme(page);
  const owner = await signUp(page);
  // Adding members needs a confirmed email; the e2e server sends none.
  sql(`UPDATE users SET email_verified_at = now() WHERE lower(email) = lower('${owner}')`);
  // Where this Taskiem cannot read photos with AI, there is no switch.
  await page.goto("/settings");
  await expect(page.getByTestId("settings-saved")).toBeVisible();
  await expect(page.getByTestId("settings-ai-photo-reading")).toHaveCount(0);

  await available(page);
  await page.reload();
  const card = page.getByTestId("settings-ai-photo-reading");
  const box = card.getByLabel("Read handwriting with AI");
  await expect(box).not.toBeChecked();
  await expect(box).toBeEnabled();
  await expect(card).toContainText("may be sent to Taskiem's AI to read. The photo is not kept, and its words go back only to whoever asked.");
  await expect(card).toContainText("this month's AI allowance");
  // Beside Chat tools, in the same look.
  const order = await page.locator(".card h2").allTextContents();
  expect(order.indexOf("Reading photos")).toBe(order.indexOf("Chat tools") + 1);
  await noAxe(page, "Settings with reading photos off", "settings-ai-off");
  await box.click();
  await expect(page.locator(".toast").filter({ hasText: "Saved" })).toBeVisible();
  await expect(box).toBeChecked();
  await page.reload();
  await expect(page.getByLabel("Read handwriting with AI")).toBeChecked();
  expect((await (await page.request.get("/v1/settings/ai-photo-reading", { headers: H })).json()).ai_photo_reading).toBe(true);
  await card.scrollIntoViewIfNeeded();
  await noAxe(page, "Settings with reading photos on", "settings-ai-on");

  // An admin sees it as it is, and cannot change it (nor can the API).
  const admin = `admin-${letters()}@photo-ai.test`;
  const made = await page.request.post("/v1/members", { headers: H, data: { email: admin, password: PASSWORD, roles: ["admin"] } });
  expect(made.status(), await made.text()).toBe(201);
  const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, reducedMotion: "reduce" });
  const other = await ctx.newPage();
  await followTheme(other);
  await signIn(other, admin);
  await available(other);
  await other.goto("/settings");
  const theirs = other.getByTestId("settings-ai-photo-reading");
  await expect(theirs.getByLabel("Read handwriting with AI")).toBeChecked();
  await expect(theirs.getByLabel("Read handwriting with AI")).toBeDisabled();
  await expect(theirs.getByTestId("ai-photo-reading-owner-only")).toHaveText("Only an owner can change this.");
  await theirs.scrollIntoViewIfNeeded();
  await noAxe(other, "Settings, reading photos, for an admin", "settings-ai-admin");
  const refused = await other.request.put("/v1/settings/ai-photo-reading", { headers: H, data: { ai_photo_reading: false } });
  expect(refused.status()).toBe(403);
  expect((await refused.json()).code).toBe("owner_only");
  await ctx.close();

  // Off again, by the owner.
  await page.getByLabel("Read handwriting with AI").click();
  await expect(page.getByLabel("Read handwriting with AI")).not.toBeChecked();
  await expect.poll(async () => (await (await page.request.get("/v1/settings/ai-photo-reading", { headers: H })).json()).ai_photo_reading).toBe(false);
});

test("the Pay sheet: off, an owner is told where to turn it on; on, Read it with AI fills it in from the photo", async ({ page, context }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await followTheme(page);
  await signUp(page);
  await standIn(context);
  await available(page);
  const pay = await payApi(page);
  const sent = await photoText(page, (r) => json(r, 200, { text: `  ${WORDS.replace(/\n/g, "\n\n")}  ` }));

  // Off: as before, and the owner is pointed at Settings. Nothing sent.
  await page.goto("/payments");
  await page.getByTestId("pay-account").click();
  const sheet = page.getByTestId("pay-sheet");
  await sheet.getByTestId("pay-photo-file").setInputFiles(file(SCRAWL));
  const told = page.locator(".toast").filter({ hasText: "No account number in that photo. Type it in." });
  await expect(told).toBeVisible();
  await expect(told.getByRole("link", { name: "Turn on reading with AI" })).toHaveAttribute("href", "/settings#ai-photo-reading");
  await expect(sheet.getByTestId("photo-ai")).toHaveCount(0);
  expect(sent).toHaveLength(0);

  // On (read once a page view: a new one here).
  await turn(page, true);
  await page.reload();
  await page.getByTestId("pay-account").click();
  // Found on this device: nothing offered, nothing sent.
  await sheet.getByTestId("pay-photo-file").setInputFiles(file(WORDS, "printed.png"));
  await expect(sheet.getByTestId("pay-field-number")).toHaveValue("0123456789");
  await expect(sheet.getByTestId("photo-ai")).toHaveCount(0);
  expect(sent).toHaveLength(0);
  await sheet.getByRole("button", { name: "Close" }).click();
  await expect(sheet).toBeHidden();

  // Handwriting: the browser finds no account; Read it with AI is offered.
  await page.getByTestId("pay-account").click();
  await sheet.getByTestId("pay-photo-file").setInputFiles(file(SCRAWL));
  const offer = sheet.getByTestId("photo-ai");
  await expect(offer).toBeVisible();
  await expect(offer).toContainText("No account number in that photo.");
  await expect(offer.getByRole("button", { name: "Read it with AI" })).toBeVisible();
  await expect(offer).toContainText("Sends this photo to Taskiem's AI. It is not kept.");
  expect(sent).toHaveLength(0);
  expect(pay.reads()).toEqual([
    { text: SCRAWL, from: "photo" },
    { text: WORDS, from: "photo" },
    { text: SCRAWL, from: "photo" },
  ]);
  await noAxe(page, "the Pay sheet offering Read it with AI", "pay-sheet-ai-offer");
  await offer.getByTestId("photo-ai-go").click();
  await expect(sheet.getByTestId("pay-field-number")).toHaveValue("0123456789");
  await expect(sheet.getByLabel("How much? in naira")).toHaveValue("45,000");
  await expect(offer).toHaveCount(0);
  await expect(page.getByText("Filled in from the photo. Check it before you pay.")).toBeVisible();
  expect(sent).toHaveLength(1);
  carriesPhoto(sent[0], SCRAWL);
  // The AI's words read as the browser's are: from a photo.
  expect(pay.reads().at(-1)).toEqual({ text: WORDS, from: "photo" });
  await sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "stock" }).click();
  await sheet.getByTestId("pay-prepare").click();
  await expect(sheet.getByTestId("pay-name-differs")).toHaveText("The photo says Chidi Okafor; the bank says CHIDI OKEKE.");
});

test("a refusal is said in Taskiem's words; Type it in puts the offer away", async ({ page, context }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await followTheme(page);
  await signUp(page);
  await standIn(context);
  await available(page);
  await turn(page, true);
  await payApi(page);
  const sent = await photoText(page, (r) => json(r, 409, { error: "This month's AI budget is used up: type the details in.", code: "budget" }));
  await page.goto("/payments");
  await page.getByTestId("pay-account").click();
  const sheet = page.getByTestId("pay-sheet");
  // No words at all on this device: offered too.
  await sheet.getByTestId("pay-photo-file").setInputFiles(file(" \n "));
  const offer = sheet.getByTestId("photo-ai");
  await expect(offer).toContainText("No words in that photo.");
  await offer.getByTestId("photo-ai-go").click();
  await expect(offer.getByTestId("photo-ai-error")).toHaveText("This month's AI budget is used up: type the details in.");
  expect(sent).toHaveLength(1);
  await noAxe(page, "the Pay sheet with a refusal", "pay-sheet-ai-refused");
  await offer.getByTestId("photo-ai-no").click();
  await expect(offer).toHaveCount(0);
  await expect(sheet.getByTestId("pay-field-number")).toHaveValue("");
});

test("Pay several: not one line of the photo read, Read it with AI reads the list", async ({ page, context }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await followTheme(page);
  await signUp(page);
  await standIn(context);
  await available(page);
  await turn(page, true);
  await payApi(page);
  const LIST = "Chidi 0123456789 GTBank 45k\nTunde 0222222222 Opay 20k";
  const sent = await photoText(page, (r) => json(r, 200, { text: LIST }));
  const reads: unknown[] = [];
  await page.route(
    (u) => u.pathname.startsWith("/v1/payment-batches"),
    (r) => {
      const path = new URL(r.request().url()).pathname;
      if (path !== "/v1/payment-batches/read") return json(r, 200, { batches: [] });
      const body = r.request().postDataJSON() as { text: string };
      reads.push(body);
      if (body.text === LIST)
        return json(r, 200, {
          lines: [
            { line: 1, text: "Chidi 0123456789 GTBank 45k", account_number: "0123456789", bank_name: "GTBank", amount_kobo: 4_500_000, name: "Chidi" },
            { line: 2, text: "Tunde 0222222222 Opay 20k", account_number: "0222222222", bank_name: "Opay", amount_kobo: 2_000_000, name: "Tunde" },
          ],
        });
      return json(r, 200, {
        lines: body.text.split("\n").map((text, i) => ({ line: i + 1, text, problem: "No account number of 10 digits on this line." })),
      });
    },
  );
  await page.goto("/payments/several");
  await page.getByTestId("add-photo-file").setInputFiles(file(SCRAWL, "list.jpg"));
  const offer = page.getByTestId("photo-ai");
  await expect(offer).toContainText("Not one line of that photo could be read.");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByTestId("list-row")).toHaveCount(0);
  expect(reads).toEqual([{ text: SCRAWL, from: "photo" }]);
  await noAxe(page, "Pay several offering Read it with AI", "pay-several-ai-offer");
  await offer.getByTestId("photo-ai-go").click();
  const rows = page.getByTestId("list-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(1).getByTestId("list-number")).toHaveValue("0222222222");
  expect(reads).toEqual([{ text: SCRAWL, from: "photo" }, { text: LIST, from: "photo" }]);
  carriesPhoto(sent[0], SCRAWL);
});

test("Pay several, off: the lines go in the list to fix, as before", async ({ page, context }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signUp(page);
  await standIn(context);
  await available(page);
  await payApi(page);
  const sent = await photoText(page, (r) => json(r, 500, {}));
  await page.route("**/v1/payment-batches/read", (r) => json(r, 200, { lines: [{ line: 1, text: "Chd1", problem: "No account number of 10 digits on this line." }] }));
  await page.goto("/payments/several");
  await page.getByTestId("add-photo-file").setInputFiles(file("Chd1", "list.jpg"));
  await expect(page.getByTestId("list-row")).toHaveCount(1);
  await expect(page.getByTestId("photo-ai")).toHaveCount(0);
  expect(sent).toHaveLength(0);
});

/** A buyer linked to a supplier, with the supplier's words in their chat. */
async function linked(browser: Browser) {
  const stamp = letters();
  const supplier = await (await browser.newContext()).newPage();
  await signUp(supplier, `Chidi Haulage ${stamp}`);
  const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, reducedMotion: "reduce" });
  const buyer = await ctx.newPage();
  await signUp(buyer, `Rivers Foods ${stamp}`);
  await supplier.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`chidi haulage ${stamp}`)}`, { headers: H })).json();
  await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } });
  const link = (await (await supplier.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  await supplier.request.post(`/v1/links/${link}/accept`, { headers: H });
  expect((await supplier.request.post(`/v1/link-chats/${link}/messages`, { headers: H, data: { text: "My details are in the photo" } })).status()).toBe(201);
  await supplier.context().close();
  return { buyer, ctx, link };
}

test("a chat with a linked business: a photo's Pay this account and Save the account, and Read it with AI", async ({ browser }) => {
  const { buyer: page, ctx, link } = await linked(browser);
  await followTheme(page);
  await standIn(ctx);
  await available(page);
  const pay = await payApi(page);
  // The e2e server keeps no files: their photo, and ours, are added to the
  // conversation as the API gives them, and fetched with the session.
  const now = new Date();
  const theirs = { id: "0192e2e0-0000-7000-8000-0000000f0001", link, mine: false, kind: "text", body: "", attachment: { kind: "photo", type: "image/png", size: 220, name: "details.png" }, created_at: new Date(now.getTime() + 1000).toISOString(), read_at: now.toISOString() };
  const ours = { ...theirs, id: "0192e2e0-0000-7000-8000-0000000f0002", mine: true, created_at: new Date(now.getTime() + 2000).toISOString() };
  let words = WORDS;
  const fetched: string[] = [];
  await page.route(`**/v1/link-chats/${link}/messages*`, async (r) => {
    const url = new URL(r.request().url());
    if (r.request().method() !== "GET" || url.search) return r.fallback();
    const res = await r.fetch();
    const body = (await res.json()) as { messages: unknown[] };
    return json(r, 200, { ...body, messages: [...body.messages, theirs, ours] });
  });
  await page.route(`**/v1/link-chats/${link}/files/*`, (r) => {
    fetched.push(r.request().method());
    return r.fulfill({ status: 200, contentType: "image/png", body: photo(words) });
  });
  const savedReads: unknown[] = [];
  await page.route("**/v1/me/saved/read", (r) => {
    savedReads.push(r.request().postDataJSON());
    const text = String((r.request().postDataJSON() as { text: string }).text);
    if (!text.includes("0123456789")) return json(r, 200, { kind: "text", title: "", details: { body: text } });
    return json(r, 200, { kind: "bank_account", title: "Chidi Okafor", details: { bank_name: "Access Bank", account_number: "0123456789" } });
  });
  const sent = await photoText(page, (r) => json(r, 200, { text: WORDS }));

  await page.goto(`/chat?with=${link}`);
  const pane = page.getByTestId("chat-pane");
  const photoOf = (mine: boolean) => pane.getByTestId("chat-message").filter({ has: page.locator(".chat-photo") }).nth(mine ? 1 : 0);
  await expect(photoOf(false)).toBeVisible();
  const menu = async (mine: boolean) => {
    const bubble = photoOf(mine);
    await bubble.hover();
    await bubble.locator(".bubble-chevron").click({ force: true });
    const more = page.getByTestId("chat-more");
    await expect(more.getByRole("button", { name: "Save the account", exact: true })).toBeVisible();
    return more;
  };
  // Theirs: Pay this account (as the chip under their words) and Save the account.
  let more = await menu(false);
  await expect(more.getByRole("button", { name: "Pay this account", exact: true })).toBeVisible();
  if (shots)
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      await page.waitForTimeout(300);
      await page.screenshot({ path: `${shots}/link-chat-photo-menu-${scheme}.png` });
    }
  await page.emulateMedia({ colorScheme: "light" });
  await page.keyboard.press("Escape");
  await expect(more).toBeHidden();
  // Ours: Save the account only (the chip is for the other side's).
  more = await menu(true);
  await expect(more.getByRole("button", { name: "Pay this account", exact: true })).toHaveCount(0);
  await page.keyboard.press("Escape");

  more = await menu(false);
  await more.getByRole("button", { name: "Pay this account", exact: true }).click();
  const sheet = page.getByTestId("pay-sheet");
  await expect(sheet.getByTestId("pay-field-number")).toHaveValue("0123456789");
  await expect(sheet.getByLabel("How much? in naira")).toHaveValue("45,000");
  expect(fetched.length).toBeGreaterThan(0);
  expect(fetched.every((m) => m === "GET")).toBe(true);
  expect(pay.reads()).toEqual([{ text: WORDS, from: "photo" }]);
  await sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "stock" }).click();
  await sheet.getByTestId("pay-prepare").click();
  await expect(sheet.getByTestId("pay-name-differs")).toHaveText("The photo says Chidi Okafor; the bank says CHIDI OKEKE.");
  expect(pay.calls.find((c) => c.method === "POST" && c.path === "/v1/payments")?.body).toMatchObject({ from: "chat", account_number: "0123456789" });
  await noAxe(page, "a link chat photo's payment, prepared", "link-chat-photo-pay");
  await sheet.getByRole("button", { name: "Close" }).click();
  await expect(sheet).toBeHidden();

  more = await menu(false);
  await more.getByRole("button", { name: "Save the account", exact: true }).click();
  const form = page.getByTestId("saved-form");
  await expect(form.getByTestId("saved-number-input")).toHaveValue("0123456789");
  expect(savedReads).toEqual([{ text: WORDS, from: "photo" }]);
  await noAxe(page, "a link chat photo's account to save", "link-chat-photo-save");
  await form.getByRole("button", { name: "Cancel" }).or(page.getByRole("button", { name: "Close" })).first().click();
  await expect(form).toBeHidden();
  expect(sent).toHaveLength(0);

  // Handwriting, with reading with AI on: offered in a dialog of its own.
  words = SCRAWL;
  await turn(page, true);
  await page.reload();
  await expect(photoOf(false)).toBeVisible();
  more = await menu(false);
  await more.getByRole("button", { name: "Save the account", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Read the photo with AI?" });
  await expect(dialog.getByTestId("photo-ai")).toContainText("No account number in that photo.");
  await expect(dialog).toContainText("Sends this photo to Taskiem's AI. It is not kept.");
  // "Reading the photo…" is taken away: it would cover the offer.
  await expect(page.locator(".toast").filter({ hasText: "Reading the photo…" })).toHaveCount(0);
  await noAxe(page, "a link chat photo offering Read it with AI", "link-chat-ai-offer");
  await dialog.getByTestId("photo-ai-go").click();
  await expect(dialog).toBeHidden();
  await expect(form.getByTestId("saved-number-input")).toHaveValue("0123456789");
  expect(savedReads.slice(-2)).toEqual([{ text: SCRAWL, from: "photo" }, { text: WORDS, from: "photo" }]);
  carriesPhoto(sent[0], SCRAWL);
  await ctx.close();
});
