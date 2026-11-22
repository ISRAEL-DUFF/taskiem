import { expect, test, type BrowserContext, type Page, type Request, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Reading account details from photos (I2; the app does the same, I1): a
// photo's words are read in the browser, on the person's own device, by
// Tesseract.js in a worker (src/lib/photoText.ts), and only the words go
// to Taskiem's readers with from: "photo". Here: the Pay sheet's "From a
// photo", a photo that holds no account, Pay this account and Save the
// account on a photo in a customer chat, and Pay several from a photo of
// a list. The e2e server has no payments account, so the payments API is
// stood in for, as pay-from-message.spec.ts does.
//
// The reader is stood in for without any hook in the app: the worker file
// the app loads (/ocr/v7/worker.min.js) is answered with a small worker
// that speaks Tesseract.js's messages and "reads" the words written after
// WORDS: in the photo's bytes (a real PNG, so it shows). The page's code,
// Tesseract.js's loader and the worker all run as they do for a person.
// One test below runs the real reader on a photo drawn in the page, under
// the app's own policy (no bypassCSP).

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (r: Route, status: number, body: unknown) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
const shots = process.env.PHOTO_SHOTS;

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: `Photo Pay ${letters()}`, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@photo.test`, name: "Ada Owner", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

// A small picture of a slip of paper with lines on it, then the words the
// stand-in reader finds in it (after the picture's end, so it still shows).
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAGAAAABACAIAAABqVuVZAAAAhklEQVR42u3XsQkAIAxFwQxmYeX+g1iKdTYQLEQIB3+CgwRe7DXtsEAACNBfoDZ67QF6DOTEKAACBAgQIECAAJnU0GJazA8CBAgQIAMECBAgLabFtJgTA0QBECBAgAABAgRIahQCcmJ+ECBAgAwQIECAAGmx2qkByB0BAgQIECBAgAAZoOslcMuY5zRJt8wAAAAASUVORK5CYII=",
  "base64",
);
const photo = (words: string) => Buffer.concat([PNG, Buffer.from(`WORDS:${words}`)]);
const file = (words: string, name = "account.png") => ({ name, mimeType: "image/png", buffer: photo(words) });

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

/** The stand-in reader, and every request the page makes, to show the
 * photo is never sent: only GETs fetch a photo, nothing carries one. */
async function standIn(context: BrowserContext, page: Page) {
  const asked: string[] = [];
  await context.route("**/ocr/v7/**", (r) => {
    asked.push(new URL(r.request().url()).pathname);
    if (r.request().url().endsWith("/worker.min.js")) return r.fulfill({ status: 200, contentType: "text/javascript", body: FAKE_WORKER });
    return r.fulfill({ status: 404, body: "" });
  });
  const sent: Request[] = [];
  page.on("request", (q) => {
    if (q.method() !== "GET") sent.push(q);
  });
  const neverSent = () => {
    for (const q of sent) {
      const body = q.postDataBuffer() ?? Buffer.alloc(0);
      expect(body.includes(Buffer.from("WORDS:")), `${q.method()} ${q.url()} carries the photo`).toBe(false);
      expect(body.includes(PNG.subarray(0, 8)), `${q.method()} ${q.url()} carries the photo`).toBe(false);
      expect(q.url(), "nothing is uploaded").not.toMatch(/upload/);
    }
  };
  return { asked, neverSent };
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
    const vs = await axe(page);
    expect(vs, describe(`${what} (${scheme})`, vs)).toEqual([]);
    if (shots && shot) await page.screenshot({ path: `${shots}/${shot}-${scheme}.png` });
  }
  await page.emulateMedia({ colorScheme: "light" });
}

const fits = async (page: Page, width: number) => expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

const banks = [
  { code: "058", name: "Guaranty Trust Bank", likely: true },
  { code: "044", name: "Access Bank", likely: true },
  { code: "50211", name: "Kuda Microfinance Bank", likely: true },
  ...["Zenith Bank", "First Bank of Nigeria", "Wema Bank"].map((name, i) => ({ code: `9${i}`, name, likely: false })),
];

const payment = (over: Record<string, unknown> = {}) => ({
  id: "0192e2e0-0000-7000-8000-00000000e001", saved_id: null, personal: false, payee: "Account ••6789", bank_name: "Access Bank", account: "••6789",
  account_name: "CHIDI OKEKE", amount_kobo: 4_500_000, purpose: "stock", purpose_note: "", note: "", first_payment: true, changed_account: false, needs_ok: "",
  status: "prepared", reason: null, fee_kobo: null, run_id: null, started_from: "tools", prepared_by: "user:ada", confirmed_by: null,
  created_at: new Date().toISOString(), confirmed_at: null, finished_at: null, again: false, ...over,
});

const WORDS = "CHIDI OKAFOR\nAcct No: 0123456789\nAccess Bank\nN45,000";

/** Stands in for the payments API; returns what was asked of it. */
async function payApi(page: Page, readDelay = 0) {
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
        if (readDelay) await new Promise((x) => setTimeout(x, readDelay));
        if (String(body.text).includes("0123456789"))
          return json(r, 200, { found: true, account_number: "0123456789", bank_name: "Access Bank", bank_code: "044", amount_kobo: 4_500_000, name: "Chidi Okafor" });
        return json(r, 200, { found: false });
      }
      if (url.pathname === "/v1/payments/banks") return json(r, 200, { from: "paystack", banks: url.searchParams.get("account") ? banks : banks.map((b) => ({ ...b, likely: false })) });
      if (url.pathname === "/v1/payments" && req.method() === "POST") return json(r, 201, { payment: payment({ started_from: body.from }) });
      if (url.pathname.endsWith("/cancel")) return json(r, 200, { payment: payment({ status: "cancelled" }) });
      return json(r, 404, { error: "not found" });
    },
  );
  await page.route("**/v1/chat/actions/*/choices", (r) => json(r, 200, { fields: { who: ["@bayo"] } }));
  return calls;
}

for (const width of [390, 360])
  test(`the Pay sheet filled from a photo, read on this device, ${width} px`, async ({ page, context }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await followTheme(page);
    await signUp(page);
    const reader = await standIn(context, page);
    // The words take a moment to read on the server: "Reading the photo…".
    const calls = await payApi(page, 600);
    await page.goto("/payments");
    await page.getByTestId("pay-account").click();
    const sheet = page.getByTestId("pay-sheet");
    const button = sheet.getByTestId("pay-photo");
    await expect(button).toContainText("From a photo");
    await expect(sheet.getByTestId("pay-photo-note")).toHaveText("Read on this device, not sent.");
    // Phones offer the camera, computers the files: nothing is forced.
    const input = sheet.getByTestId("pay-photo-file");
    await expect(input).toHaveAttribute("accept", "image/*");
    expect(await input.getAttribute("capture")).toBeNull();
    await noAxe(page, "the Pay sheet with From a photo", width === 390 ? "pay-sheet-empty" : undefined);
    await fits(page, width);

    await input.setInputFiles(file(WORDS));
    await expect(button).toContainText("Reading the photo…");
    await expect(sheet.getByTestId("pay-field-number")).toHaveValue("0123456789");
    await expect(sheet.getByLabel("How much? in naira")).toHaveValue("45,000");
    await expect(sheet.getByRole("group", { name: "Banks this number could be with" }).getByRole("button", { name: "Access Bank" })).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByText("Filled in from the photo. Check it before you pay.")).toBeVisible();
    // Only the words went, said to be from a photo; the reader was this
    // browser's, and the photo was not sent anywhere.
    const read = calls.filter((c) => c.path === "/v1/payments/read");
    expect(read.map((c) => c.body)).toEqual([{ text: WORDS, from: "photo" }]);
    expect(reader.asked).toContain("/ocr/v7/worker.min.js");
    reader.neverSent();
    await sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "stock" }).click();
    await noAxe(page, "the Pay sheet filled from a photo", width === 390 ? "pay-sheet-from-photo" : undefined);
    await fits(page, width);

    await sheet.getByTestId("pay-prepare").click();
    await expect(sheet.getByTestId("pay-name")).toHaveText("CHIDI OKEKE");
    // The photo's name is not the bank's: said in amber, as a message's is.
    await expect(sheet.getByTestId("pay-name-differs")).toHaveText("The photo says Chidi Okafor; the bank says CHIDI OKEKE.");
    expect(calls.find((c) => c.method === "POST" && c.path === "/v1/payments")?.body).toEqual({
      from: "tools",
      account_number: "0123456789",
      bank_name: "Access Bank",
      amount_kobo: 4_500_000,
      purpose: "stock",
    });
    await noAxe(page, "a payment from a photo, prepared", width === 390 ? "pay-sheet-photo-prepared" : undefined);
    reader.neverSent();
  });

test("a photo with no account number, or no words, is said; the form stays to type in", async ({ page, context }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signUp(page);
  const reader = await standIn(context, page);
  const calls = await payApi(page);
  await page.goto("/payments");
  await page.getByTestId("pay-someone").click();
  const sheet = page.getByTestId("pay-sheet");
  // Paying a saved account offers it too.
  await sheet.getByTestId("pay-photo-file").setInputFiles(file("Rice 50kg\nBeans 20kg", "receipt.png"));
  await expect(page.getByText("No account number in that photo. Type it in.")).toBeVisible();
  expect(calls.filter((c) => c.path === "/v1/payments/read").map((c) => c.body)).toEqual([{ text: "Rice 50kg\nBeans 20kg", from: "photo" }]);
  await expect(sheet.getByTestId("pay-field-who")).toHaveValue("");
  await expect(sheet.getByTestId("pay-photo")).toContainText("From a photo");
  // No words at all: not even asked.
  await sheet.getByTestId("pay-photo-file").setInputFiles(file("  \n ", "blank.png"));
  await expect(page.getByText("No words in that photo. Type it in.")).toBeVisible();
  expect(calls.filter((c) => c.path === "/v1/payments/read")).toHaveLength(1);
  reader.neverSent();
});

test("a customer chat's photo: Pay this account and Save the account", async ({ page, context }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await followTheme(page);
  await signUp(page);
  const reader = await standIn(context, page);
  const calls = await payApi(page);
  const now = new Date().toISOString();
  const chat = { id: "0192e2e0-0000-7000-8000-00000000d0e1", name: "Tolu Bello", phone: "+234 803 ••• 0099", last: "Photo", last_mine: false, last_at: now, unread: 0, window_open: true, window_until: null, stopped: false, archived: false, line: "whatsapp" };
  const messages = [
    { id: "m1", mine: false, kind: "text", body: "Here are my details", read_at: now, created_at: now },
    { id: "m2", mine: false, kind: "media", body: "Photo", attachment: { kind: "photo", type: "image/png", size: 120, name: "details.png" }, read_at: now, created_at: now },
  ];
  const fetched: string[] = [];
  await page.route(
    (u) => u.pathname.startsWith("/v1/customer-chats"),
    (r) => {
      const p = new URL(r.request().url()).pathname;
      if (p === "/v1/customer-chats") return json(r, 200, { chats: [chat] });
      if (p === "/v1/customer-chats/unread") return json(r, 200, { chats: 0, messages: 0 });
      if (p === `/v1/customer-chats/${chat.id}`) return json(r, 200, chat);
      if (p === `/v1/customer-chats/${chat.id}/messages`) return json(r, 200, { messages, more: false });
      if (p === `/v1/customer-chats/${chat.id}/files/m2`) {
        fetched.push(r.request().method());
        return r.fulfill({ status: 200, contentType: "image/png", body: photo(WORDS) });
      }
      if (p.endsWith("/orders")) return json(r, 200, { orders: [] });
      return json(r, 200, {});
    },
  );
  const savedReads: unknown[] = [];
  await page.route("**/v1/me/saved/read", (r) => {
    savedReads.push(r.request().postDataJSON());
    return json(r, 200, { kind: "bank_account", title: "Chidi Okafor", details: { bank_name: "Access Bank", account_number: "0123456789" } });
  });
  await page.goto(`/chat?with=c-${chat.id}`);
  const pane = page.getByTestId("customer-pane");
  const bubble = pane.getByTestId("customer-message").filter({ has: page.getByRole("img", { name: "Photo from Tolu Bello" }) });
  const menu = async (entry: string) => {
    await bubble.hover();
    await bubble.locator(".bubble-chevron").click({ force: true });
    const more = page.getByTestId("chat-more");
    await expect(more.getByRole("button", { name: "Save the account", exact: true })).toBeVisible();
    if (entry === "look") return more;
    await more.getByRole("button", { name: entry, exact: true }).click();
    return more;
  };
  // The photo's menu: Pay this account (an owner may pay) and Save the account.
  const more = await menu("look");
  await expect(more.getByRole("button", { name: "Pay this account", exact: true })).toBeVisible();
  // (Not axe'd open: the open menu's 22 px chevron is the chat's own, as
  // in every chat; the sheets it opens are, below.)
  if (shots)
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      await page.screenshot({ path: `${shots}/chat-photo-menu-${scheme}.png` });
    }
  await page.emulateMedia({ colorScheme: "light" });
  await page.keyboard.press("Escape");
  await expect(more).toBeHidden();

  await menu("Pay this account");
  const sheet = page.getByTestId("pay-sheet");
  await expect(sheet.getByTestId("pay-field-number")).toHaveValue("0123456789");
  await expect(sheet.getByLabel("How much? in naira")).toHaveValue("45,000");
  // Fetched the way the chat shows it, read here; only the words sent.
  expect(fetched.length).toBeGreaterThan(0);
  expect(fetched.every((m) => m === "GET")).toBe(true);
  expect(calls.filter((c) => c.path === "/v1/payments/read").map((c) => c.body)).toEqual([{ text: WORDS, from: "photo" }]);
  // Filled from the photo, it does not offer another.
  await expect(sheet.getByTestId("pay-photo")).toHaveCount(0);
  await sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "stock" }).click();
  await sheet.getByTestId("pay-prepare").click();
  await expect(sheet.getByTestId("pay-name-differs")).toHaveText("The photo says Chidi Okafor; the bank says CHIDI OKEKE.");
  expect(calls.find((c) => c.method === "POST" && c.path === "/v1/payments")?.body).toMatchObject({ from: "chat", account_number: "0123456789", amount_kobo: 4_500_000 });
  await noAxe(page, "a chat photo's payment, prepared", "chat-photo-pay");
  await sheet.getByRole("button", { name: "Close" }).click();
  await expect(sheet).toBeHidden();

  // Save the account: Save as…'s sheet for an account, filled in.
  await menu("Save the account");
  const form = page.getByTestId("saved-form");
  await expect(form.getByTestId("saved-number-input")).toHaveValue("0123456789");
  await expect(form.getByLabel("Title")).toHaveValue("Chidi Okafor");
  expect(savedReads).toEqual([{ text: WORDS, from: "photo" }]);
  await noAxe(page, "a chat photo's account to save", "chat-photo-save");
  reader.neverSent();
});

test("Pay several from a photo of a list: the words read as a pasted list, what cannot be read shown to fix", async ({ page, context }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await followTheme(page);
  await signUp(page);
  const reader = await standIn(context, page);
  await payApi(page);
  const LIST = "Chidi 0123456789 GTBank 45k\nTunde 20k";
  const reads: unknown[] = [];
  await page.route(
    (u) => u.pathname.startsWith("/v1/payment-batches"),
    (r) => {
      const path = new URL(r.request().url()).pathname;
      if (path === "/v1/payment-batches/read") {
        reads.push(r.request().postDataJSON());
        return json(r, 200, {
          lines: [
            { line: 1, text: "Chidi 0123456789 GTBank 45k", account_number: "0123456789", bank_name: "GTBank", amount_kobo: 4_500_000, name: "Chidi" },
            { line: 2, text: "Tunde 20k", amount_kobo: 2_000_000, name: "Tunde", problem: "No account number of 10 digits on this line." },
          ],
        });
      }
      return json(r, 200, { batches: [] });
    },
  );
  await page.goto("/payments/several");
  await expect(page.getByRole("heading", { name: "Pay several people", level: 1 })).toBeVisible();
  await expect(page.getByTestId("add-photo")).toContainText("From a photo of a list");
  await expect(page.getByTestId("add-photo-note")).toHaveText("Read on this device, not sent.");
  await noAxe(page, "Pay several with From a photo of a list", "pay-several-empty");
  await fits(page, 390);

  await page.getByTestId("add-photo-file").setInputFiles(file(LIST, "list.jpg"));
  const rows = page.getByTestId("list-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0).getByTestId("list-number")).toHaveValue("0123456789");
  await expect(rows.nth(0).getByTestId("list-amount")).toHaveValue("45,000");
  await expect(rows.nth(1).getByTestId("list-row-problem")).toHaveText("No account number of 10 digits on this line.");
  expect(reads).toEqual([{ text: LIST, from: "photo" }]);
  await noAxe(page, "a list read from a photo", "pay-several-from-photo");
  await fits(page, 390);
  // A list typed in is not from a photo.
  await page.getByTestId("add-paste").click();
  const paste = page.getByRole("dialog", { name: "Paste a list" });
  await paste.getByTestId("paste-text").fill("Bayo 0222222222 Opay 5k");
  await paste.getByTestId("paste-read").click();
  await expect.poll(() => reads.length).toBe(2);
  expect(reads[1]).toEqual({ text: "Bayo 0222222222 Opay 5k" });
  reader.neverSent();
});

// The app's own policy here (axe is not run, so it needs no bypass).
test.describe("under the app's policy", () => {
test.use({ bypassCSP: false });
test("the real reader reads a photo in this browser, under the app's own policy", async ({ page }) => {
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 390, height: 844 });
  await signUp(page);
  const calls = await payApi(page);
  const ocr: string[] = [];
  page.on("requestfinished", (q) => {
    if (q.url().includes("/ocr/v7/")) ocr.push(new URL(q.url()).pathname);
  });
  const blocked: string[] = [];
  page.on("console", (m) => {
    if (/Content Security Policy|Refused to/i.test(m.text())) blocked.push(m.text());
  });
  await page.goto("/payments");
  // A photo of an account's details, drawn here.
  const png = await page.evaluate(async () => {
    const c = document.createElement("canvas");
    c.width = 900;
    c.height = 260;
    const g = c.getContext("2d");
    if (!g) throw new Error("no canvas here");
    g.fillStyle = "#fff";
    g.fillRect(0, 0, c.width, c.height);
    g.fillStyle = "#000";
    g.font = "bold 64px Arial, sans-serif";
    g.fillText("Account 0123456789", 40, 110);
    g.fillText("Access Bank", 40, 200);
    const blob = await new Promise<Blob>((ok, no) => c.toBlob((b) => (b ? ok(b) : no(new Error("no picture"))), "image/png"));
    return Array.from(new Uint8Array(await blob.arrayBuffer()));
  });
  await page.getByTestId("pay-account").click();
  const sheet = page.getByTestId("pay-sheet");
  await sheet.getByTestId("pay-photo-file").setInputFiles({ name: "details.png", mimeType: "image/png", buffer: Buffer.from(png) });
  await expect(sheet.getByTestId("pay-field-number")).toHaveValue("0123456789", { timeout: 90_000 });
  const read = calls.find((c) => c.path === "/v1/payments/read");
  expect(read?.body.from).toBe("photo");
  expect(String(read?.body.text)).toContain("0123456789");
  // Taskiem's own copy of the reader, and nothing refused by the policy.
  expect(ocr).toEqual(expect.arrayContaining(["/ocr/v7/worker.min.js", "/ocr/v7/eng.traineddata.gz"]));
  expect(blocked).toEqual([]);
});
});
