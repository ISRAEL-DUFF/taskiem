import { expect, test, type Browser, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Paying from a message (docs/spec/chat-pay-people.md, phase 2): a chat
// message holding an account number gets "Pay this account"; tapped, the
// Pay sheet opens filled in, with the likely banks first, the full list a
// tap away, "Check the likely banks for me" and the name the message gave
// said in amber when the bank's differs; a chip already paid says so and
// pays again; staff pay saved accounts only; and Pay on a rider. The
// messages are real (two linked businesses); the e2e server has no
// payments account, so the payments API is stood in for here. Axe in both
// themes, at 390 and 360 px.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (r: Route, status: number, body: unknown) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@paymsg.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

/** A buyer linked to a supplier, and the supplier's words in their chat. */
async function linked(browser: Browser, viewport: { width: number; height: number }, said: string[]) {
  const stamp = letters();
  const supplier = await (await browser.newContext()).newPage();
  await signUp(supplier, `Chidi Haulage ${stamp}`);
  const buyer = await (await browser.newContext({ viewport, reducedMotion: "reduce" })).newPage();
  await signUp(buyer, `Rivers Foods ${stamp}`);
  await supplier.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`chidi haulage ${stamp}`)}`, { headers: H })).json();
  await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } });
  const link = (await (await supplier.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  await supplier.request.post(`/v1/links/${link}/accept`, { headers: H });
  for (const text of said) expect((await supplier.request.post(`/v1/link-chats/${link}/messages`, { headers: H, data: { text } })).status()).toBe(201);
  return { buyer, link };
}

async function noAxe(page: Page, what: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describe(`${what} (${scheme})`, vs)).toEqual([]);
  }
  await page.emulateMedia({ colorScheme: "light" });
}

const NEW = "Acct: 0123456789, send 45k. Chidi Okafor";
const PAID = "Pay my rider 0987654321 GTBank";
const SAVED = "Same as before: 0555555555 Opay";
const banks = [
  { code: "058", name: "Guaranty Trust Bank", likely: true },
  { code: "044", name: "Access Bank", likely: true },
  { code: "50211", name: "Kuda Microfinance Bank", likely: true },
  ...["Zenith Bank", "First Bank of Nigeria", "United Bank for Africa", "Wema Bank", "Sterling Bank", "Fidelity Bank", "OPay Digital Services"].map((name, i) => ({ code: `9${i}`, name, likely: false })),
];

const payment = (over: Record<string, unknown> = {}) => ({
  id: "0192e2e0-0000-7000-8000-00000000c001", saved_id: null, personal: false, payee: "Account ••6789", bank_name: "Guaranty Trust Bank", account: "••6789",
  account_name: "CHIDI OKEKE", amount_kobo: 4_500_000, purpose: "delivery", purpose_note: "", note: "", first_payment: true, changed_account: false, needs_ok: "",
  status: "prepared", reason: null, fee_kobo: null, run_id: null, started_from: "chat", prepared_by: "user:ada", confirmed_by: null,
  created_at: new Date().toISOString(), confirmed_at: null, finished_at: null, again: false, ...over,
});

/** Stands in for the payments API; returns what was asked of it. */
async function payApi(page: Page) {
  const calls: { method: string; path: string; query: string; body: Record<string, unknown> }[] = [];
  await page.route(
    (u) => u.pathname.startsWith("/v1/payments"),
    async (r) => {
      const req = r.request();
      const url = new URL(req.url());
      const body = (req.postDataJSON() ?? {}) as Record<string, unknown>;
      calls.push({ method: req.method(), path: url.pathname, query: url.search, body });
      if (url.pathname === "/v1/payments/read") {
        const text = String(body.text ?? "");
        if (text === NEW) return json(r, 200, { found: true, account_number: "0123456789", amount_kobo: 4_500_000, name: "Chidi Okafor", likely: banks.slice(0, 3) });
        if (text === PAID)
          return json(r, 200, {
            found: true, account_number: "0987654321", bank_name: "Guaranty Trust Bank", bank_code: "058",
            paid: { id: "p0", amount_kobo: 4_500_000, status: "paid", at: `${new Date().getFullYear()}-11-03T10:00:00Z` },
          });
        if (text === SAVED) return json(r, 200, { found: true, account_number: "0555555555", bank_name: "OPay Digital Services", bank_code: "96", payee: "chidi" });
        return json(r, 200, { found: false });
      }
      if (url.pathname === "/v1/payments/banks") return json(r, 200, { from: "paystack", banks: url.searchParams.get("account") ? banks : banks.map((b) => ({ ...b, likely: false })) });
      if (url.pathname === "/v1/payments/name-check")
        return json(r, 200, {
          account: "••6789",
          banks: [
            { bank_name: "Guaranty Trust Bank", bank_code: "058", account_name: "CHIDI OKEKE" },
            { bank_name: "Access Bank", bank_code: "044", problem: "No account ••6789 at Access Bank." },
            { bank_name: "Kuda Microfinance Bank", bank_code: "50211", problem: "No account ••6789 at Kuda Microfinance Bank." },
          ],
        });
      if (url.pathname === "/v1/payments" && req.method() === "POST") return json(r, 201, { payment: payment() });
      if (url.pathname.endsWith("/cancel")) return json(r, 200, { payment: payment({ status: "cancelled" }) });
      return json(r, 404, { error: "not found" });
    },
  );
  await page.route("**/v1/chat/actions/*/choices", (r) => json(r, 200, { fields: { who: ["@chidi", "@bayo"] } }));
  return calls;
}

for (const width of [390, 360])
  test(`a message's account number is paid from its chip, ${width} px`, async ({ browser }) => {
    const { buyer: page, link } = await linked(browser, { width, height: 844 }, [NEW, PAID, "Rice is back in stock, call 08031234567"]);
    const calls = await payApi(page);
    await page.goto(`/chat?with=${link}`);
    const pane = page.getByTestId("chat-pane");
    const msg = (t: string) => pane.getByTestId("chat-message").filter({ hasText: t });
    // A new account: Pay this account. One already paid says when.
    await expect(msg("send 45k").getByTestId("pay-chip")).toHaveText("Pay this account");
    await expect(msg("Pay my rider").getByTestId("pay-chip")).toHaveText(/^Paid ₦45,000 on 3 Nov · Pay again$/);
    // A phone number is not an account: no chip, and it is not even read.
    await expect(msg("Rice is back").getByTestId("pay-chip-line")).toHaveCount(0);
    expect(calls.filter((c) => c.path === "/v1/payments/read").map((c) => c.body.text)).not.toContain("Rice is back in stock, call 08031234567");
    await noAxe(page, "the chat with pay chips");
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

    // Tapped: the sheet, filled in; no bank, so the likely banks first.
    await msg("send 45k").getByTestId("pay-chip").click();
    const sheet = page.getByTestId("pay-sheet");
    await expect(sheet.getByTestId("pay-field-number")).toHaveValue("0123456789");
    await expect(sheet.getByLabel("How much? in naira")).toHaveValue("45,000");
    const likely = sheet.getByRole("group", { name: "Banks this number could be with" });
    await expect(likely.getByRole("button")).toHaveText(["Guaranty Trust Bank", "Access Bank", "Kuda Microfinance Bank"]);
    expect(calls.find((c) => c.path === "/v1/payments/banks")?.query).toBe("?account=0123456789");
    // The full list is one tap away, and searchable.
    await expect(sheet.getByTestId("pay-bank")).toHaveCount(0);
    await sheet.getByTestId("pay-all-banks").click();
    await sheet.getByTestId("pay-bank-search").fill("wema");
    await expect(sheet.getByTestId("pay-bank").locator("option")).toHaveText(["Choose the bank", "Wema Bank"]);
    await sheet.getByTestId("pay-all-banks").click();
    await expect(sheet.getByTestId("pay-bank")).toHaveCount(0);
    // Check the likely banks: each answers; the person picks one.
    await sheet.getByTestId("pay-check-banks").click();
    await expect(sheet.getByTestId("pay-answers").locator("li")).toHaveText(["Guaranty Trust Bank: CHIDI OKEKE", "Access Bank: no account", "Kuda Microfinance Bank: no account"]);
    expect(calls.find((c) => c.path === "/v1/payments/name-check")?.body).toEqual({ account_number: "0123456789" });
    await sheet.getByTestId("pay-answer").click();
    await expect(sheet.getByTestId("pay-answer")).toHaveAttribute("aria-pressed", "true");
    await expect(likely.getByRole("button", { name: "Guaranty Trust Bank" })).toHaveAttribute("aria-pressed", "true");
    // The message's name is not the bank's: said in amber.
    await expect(sheet.getByTestId("pay-name-differs")).toHaveText("The message says Chidi Okafor; the bank says CHIDI OKEKE.");
    await sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "a delivery" }).click();
    await noAxe(page, "the Pay sheet with the likely banks");
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await sheet.getByTestId("pay-prepare").click();
    await expect(sheet.getByTestId("pay-name")).toHaveText("CHIDI OKEKE");
    await expect(sheet.getByTestId("pay-name-differs")).toHaveText("The message says Chidi Okafor; the bank says CHIDI OKEKE.");
    expect(calls.find((c) => c.method === "POST" && c.path === "/v1/payments")?.body).toEqual({
      from: "chat",
      account_number: "0123456789",
      bank_name: "Guaranty Trust Bank",
      amount_kobo: 4_500_000,
      purpose: "delivery",
    });
    await noAxe(page, "the payment from a message, prepared");
    await sheet.getByRole("button", { name: "Close" }).click();
    await expect(sheet).toBeHidden();

    // Paid before: the chip still pays again, its bank filled in.
    await msg("Pay my rider").getByTestId("pay-chip").click();
    await expect(sheet.getByTestId("pay-field-number")).toHaveValue("0987654321");
    await expect(sheet.getByRole("group", { name: "Banks this number could be with" }).getByRole("button", { name: "Guaranty Trust Bank" })).toHaveAttribute("aria-pressed", "true");
  });

test("staff pay a message's account only when it is saved; the customer chats too", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await signUp(page, `Mama Kitchen ${letters()}`);
  const calls = await payApi(page);
  // A cashier who may pay, not an owner.
  await page.route("**/v1/me", async (r) => {
    const res = await r.fetch();
    const me = (await res.json()) as { roles: string[]; permissions: string[] };
    return json(r, 200, { ...me, roles: ["cashier"], permissions: [...new Set([...me.permissions, "money.pay", "order.handle"])] });
  });
  const now = new Date().toISOString();
  const chat = { id: "0192e2e0-0000-7000-8000-00000000d001", name: "Tolu Bello", phone: "+234 803 ••• 0099", last: SAVED, last_mine: false, last_at: now, unread: 0, window_open: true, window_until: null, stopped: false, archived: false, line: "whatsapp" };
  const messages = [
    { id: "m1", mine: false, kind: "text", body: NEW, read_at: now, created_at: now },
    { id: "m2", mine: false, kind: "text", body: SAVED, read_at: now, created_at: now },
    { id: "m3", mine: true, kind: "text", body: "Pay us at 0111111111 Zenith", read_at: null, created_at: now },
  ];
  await page.route(
    (u) => u.pathname.startsWith("/v1/customer-chats"),
    (r) => {
      const p = new URL(r.request().url()).pathname;
      if (p === "/v1/customer-chats") return json(r, 200, { chats: [chat] });
      if (p === "/v1/customer-chats/unread") return json(r, 200, { chats: 0, messages: 0 });
      if (p === `/v1/customer-chats/${chat.id}`) return json(r, 200, chat);
      if (p === `/v1/customer-chats/${chat.id}/messages`) return json(r, 200, { messages, more: false });
      if (p.endsWith("/orders")) return json(r, 200, { orders: [] });
      return json(r, 200, {});
    },
  );
  await page.goto(`/chat?with=c-${chat.id}`);
  const pane = page.getByTestId("customer-pane");
  const msg = (t: string) => pane.getByTestId("customer-message").filter({ hasText: t });
  // Not saved: staff are told to ask an owner, and cannot pay it.
  await expect(msg("send 45k").getByTestId("pay-chip-ask")).toHaveText("Ask an owner to save this account");
  await expect(msg("send 45k").getByTestId("pay-chip")).toHaveCount(0);
  // The business's own words get no chip.
  await expect(msg("Pay us at").getByTestId("pay-chip-line")).toHaveCount(0);
  // Saved: paid by its @name.
  await msg("Same as before").getByTestId("pay-chip").click();
  const sheet = page.getByTestId("pay-sheet");
  await expect(sheet.getByTestId("pay-field-who")).toHaveValue("@chidi");
  await expect(sheet.getByTestId("pay-field-number")).toHaveCount(0);
  await noAxe(page, "a customer chat's chip, for staff");
  await sheet.getByLabel("How much? in naira").fill("5k");
  await sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "a refund" }).click();
  await sheet.getByTestId("pay-prepare").click();
  await expect(sheet.getByTestId("pay-name")).toBeVisible();
  expect(calls.find((c) => c.method === "POST" && c.path === "/v1/payments")?.body).toEqual({ from: "chat", payee: "@chidi", amount_kobo: 500_000, purpose: "refund" });
});

test("staff see no Check the likely banks: an owner's", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signUp(page, `Bola Stores ${letters()}`);
  await payApi(page);
  await page.route("**/v1/me", async (r) => {
    const me = (await (await r.fetch()).json()) as { roles: string[] };
    return json(r, 200, { ...me, roles: ["manager"] });
  });
  await page.goto("/payments");
  await page.getByTestId("pay-account").click();
  const sheet = page.getByTestId("pay-sheet");
  await sheet.getByTestId("pay-field-number").fill("0123456789");
  await expect(sheet.getByRole("group", { name: "Banks this number could be with" })).toBeVisible();
  await expect(sheet.getByTestId("pay-check-banks")).toHaveCount(0);
});
