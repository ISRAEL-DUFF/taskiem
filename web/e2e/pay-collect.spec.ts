import { expect, test, type Browser, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Collecting people to pay from several messages (docs/spec/chat-pay-people.md,
// Pay several people, From messages): "Add to the list" beside a message's
// "Pay this account" keeps the message's account on the person's own list,
// kept by the server across chats; "3 to pay · ₦95,000" stays at the foot
// of every chat, with Pay these and Clear; Pay these opens the list, each
// row saying the message it came from, its amount changed there, and Check
// makes it a batch confirmed as any list is. Here: adding from a link chat
// (real messages from a linked business) and a customer chat (stood in),
// a message already on the list, a row without an amount holding the check
// up until it has one, Pay these through to the passkey, and Clear. The
// e2e server has no payments account, so the payments, the list and the
// batch APIs are stood in for. Screenshots at 390 px when PAY_SHOTS is set.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (r: Route, status: number, body: unknown) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
const shots = process.env.PAY_SHOTS;

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@collect.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
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

/** A buyer linked to a supplier, and the supplier's words in their chat. */
async function linked(browser: Browser, width: number, said: string[]) {
  const stamp = letters();
  const supplier = await (await browser.newContext()).newPage();
  const supplierName = `Chidi Haulage ${stamp}`;
  await signUp(supplier, supplierName);
  const buyer = await (await browser.newContext({ viewport: { width, height: 844 }, reducedMotion: "reduce" })).newPage();
  await followTheme(buyer);
  await signUp(buyer, `Rivers Foods ${stamp}`);
  await supplier.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`chidi haulage ${stamp}`)}`, { headers: H })).json();
  await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } });
  const link = (await (await supplier.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  await supplier.request.post(`/v1/links/${link}/accept`, { headers: H });
  for (const text of said) expect((await supplier.request.post(`/v1/link-chats/${link}/messages`, { headers: H, data: { text } })).status()).toBe(201);
  return { page: buyer, link, supplierName };
}

async function noAxe(page: Page, what: string, shot?: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describe(`${what} (${scheme})`, vs)).toEqual([]);
    if (shots && shot) await page.screenshot({ path: `${shots}/${shot}-${scheme}.png`, fullPage: shot === "collect-list" });
  }
  await page.emulateMedia({ colorScheme: "light" });
}

const fits = async (page: Page, width: number) => expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

const NO_BANK = "Acct: 0123456789, send 45k. Chidi Okeke";
const BANKED = "Bayo 0222222222 Access Bank 50k";
const NO_AMOUNT = "Pay Tunde 0333333333 Zenith Bank";
const OTHER = "My sister: 0444444444 Wema Bank 10k";
const banks = [
  { code: "058", name: "Guaranty Trust Bank", likely: true },
  { code: "044", name: "Access Bank", likely: true },
];

/** Stands in for reading messages (POST /v1/payments/read). */
async function readApi(page: Page) {
  await page.route(
    (u) => u.pathname.startsWith("/v1/payments"),
    async (r) => {
      const url = new URL(r.request().url());
      const text = String(((r.request().postDataJSON() ?? {}) as { text?: string }).text ?? "");
      if (url.pathname === "/v1/payments/read") {
        if (text === NO_BANK) return json(r, 200, { found: true, account_number: "0123456789", amount_kobo: 4_500_000, name: "Chidi Okeke", likely: banks });
        if (text === BANKED) return json(r, 200, { found: true, account_number: "0222222222", bank_name: "Access Bank", bank_code: "044", amount_kobo: 5_000_000, name: "Bayo" });
        if (text === NO_AMOUNT) return json(r, 200, { found: true, account_number: "0333333333", bank_name: "Zenith Bank", bank_code: "057", name: "Tunde" });
        if (text === OTHER) return json(r, 200, { found: true, account_number: "0444444444", bank_name: "Wema Bank", bank_code: "035", amount_kobo: 1_000_000, name: "Ngozi" });
        return json(r, 200, { found: false });
      }
      if (url.pathname === "/v1/payments/banks") return json(r, 200, { from: "paystack", banks });
      return json(r, 404, { error: "not found" });
    },
  );
}

type Row = { id: string; saved_id: string | null; payee: string; title: string; name: string; bank_name: string; last4: string; amount_kobo: number | null; purpose: string; note: string; source: { kind: string; chat: string; message: string } | null; added_at: string };

/** Stands in for the person's list (/v1/pay-collection) and its batch. */
async function collectApi(page: Page) {
  const s = { rows: [] as Row[], inBatch: null as string | null, sent: false, calls: [] as { method: string; path: string; body: Record<string, unknown> }[] };
  let seq = 0;
  const view = () => {
    const rows = s.inBatch ? [] : s.rows;
    return {
      rows,
      count: rows.length,
      total_kobo: rows.reduce((t, x) => t + (x.amount_kobo ?? 0), 0),
      no_amount: rows.filter((x) => x.amount_kobo === null).length,
      in_batch: s.inBatch,
    };
  };
  const add = (body: Record<string, unknown>) => {
    const number = String(body.account_number ?? "");
    s.rows.push({
      id: `0192e2e0-0000-7000-8000-0000000c00${++seq}`,
      saved_id: body.payee ? "0192e2e0-0000-7000-8000-00000000b001" : null,
      payee: String(body.payee ?? ""),
      title: body.payee ? "Bayo Rice Mills" : `Account ••${number.slice(-4)}`,
      name: String(body.name ?? ""),
      bank_name: String(body.bank_name ?? ""),
      last4: number.slice(-4),
      amount_kobo: typeof body.amount_kobo === "number" ? body.amount_kobo : null,
      purpose: "",
      note: "",
      source: (body.source as Row["source"]) ?? null,
      added_at: new Date().toISOString(),
    });
  };
  const B = "0192e2e0-0000-7000-8000-00000000c0b1";
  const pay = (x: Row, i: number) => ({
    id: `0192e2e0-0000-7000-8000-00000000c0f${i}`, saved_id: x.saved_id, personal: false, payee: x.title, bank_name: x.bank_name, account: `••${x.last4}`,
    account_name: (x.name || "BAYO").toUpperCase(), amount_kobo: x.amount_kobo ?? 0, purpose: x.purpose || "other", purpose_note: "", note: "", first_payment: false, changed_account: false, needs_ok: "",
    status: s.sent ? "sending" : "prepared", reason: null, fee_kobo: null, run_id: null, started_from: "chat", prepared_by: "user:ada", confirmed_by: null,
    created_at: new Date().toISOString(), confirmed_at: s.sent ? new Date().toISOString() : null, finished_at: null, again: false,
  });
  const batch = () => {
    const payments = s.rows.map(pay);
    const total = payments.reduce((t, x) => t + x.amount_kobo, 0);
    return { id: B, title: "From messages", kind: "messages", status: s.sent ? "sent" : "prepared", created_by: "user:ada", created_at: new Date().toISOString(), confirmed_at: null, payments, total_kobo: total, count: payments.length, paid_kobo: 0, waiting: 0, failed: 0 };
  };
  await page.route(
    (u) => u.pathname.startsWith("/v1/pay-collection") || u.pathname.startsWith("/v1/payment-batches"),
    async (r) => {
      const req = r.request();
      const path = new URL(req.url()).pathname;
      const body = (req.postDataJSON() ?? {}) as Record<string, unknown>;
      s.calls.push({ method: req.method(), path, body });
      if (path === "/v1/pay-collection" && req.method() === "GET") return json(r, 200, view());
      if (path === "/v1/pay-collection" && req.method() === "POST") {
        const src = body.source as Row["source"];
        if (s.rows.some((x) => x.source?.kind === src?.kind && x.source?.chat === src?.chat && x.source?.message === src?.message))
          return json(r, 409, { error: "That message is on your list already.", code: "already" });
        add(body);
        return json(r, 201, view());
      }
      if (path === "/v1/pay-collection" && req.method() === "DELETE") {
        s.rows = [];
        return json(r, 200, view());
      }
      if (path === "/v1/pay-collection/batch") {
        if (view().no_amount > 0) return json(r, 400, { error: "Say how much for each person first.", code: "amount" });
        s.inBatch = B;
        return json(r, 201, { batch: { ...batch(), held: [] } });
      }
      const one = /^\/v1\/pay-collection\/(.+)$/.exec(path);
      if (one) {
        const x = s.rows.find((y) => y.id === one[1]);
        if (!x) return json(r, 404, { error: "not found" });
        if (req.method() === "PATCH") {
          if (typeof body.amount_kobo === "number") x.amount_kobo = body.amount_kobo;
          if (typeof body.purpose === "string") x.purpose = body.purpose;
        } else s.rows = s.rows.filter((y) => y !== x);
        return json(r, 200, view());
      }
      if (path === "/v1/payment-batches" && req.method() === "GET") return json(r, 200, { batches: [] });
      if (path === `/v1/payment-batches/${B}/confirm`) {
        if (!body.passkey) return json(r, 403, { error: "confirm with your passkey", step_up: "required", methods: ["passkey"], operation: "money.pay.batch", target: `batch:${B}` });
        s.sent = true;
        return json(r, 202, { batch: { ...batch(), results: batch().payments.map((x) => ({ payment: x.id })) } });
      }
      if (path === `/v1/payment-batches/${B}`) {
        if (s.sent) {
          // Sent: its rows leave the list.
          const b = batch();
          s.rows = [];
          s.inBatch = null;
          return json(r, 200, { batch: b });
        }
        return json(r, 200, { batch: batch() });
      }
      return json(r, 404, { error: "not found" });
    },
  );
  return { s, add, B };
}

/** The browser's passkey, stood in for: any assertion asked for is given. */
async function fakePasskey(page: Page) {
  await page.addInitScript(() => {
    const buf = (s: string) => new TextEncoder().encode(s).buffer;
    Object.defineProperty(navigator, "credentials", {
      configurable: true,
      value: {
        get: async () => ({
          id: "cred-1",
          rawId: buf("cred-1"),
          type: "public-key",
          response: { clientDataJSON: buf("{}"), authenticatorData: buf("a"), signature: buf("s"), userHandle: buf("u") },
        }),
      },
    });
  });
  await page.route("**/v1/me/step-up/options", (r) =>
    json(r, 200, { publicKey: { challenge: "Y2hhbGxlbmdl", rpId: "localhost", allowCredentials: [], userVerification: "required" } }),
  );
}

const CUSTOMER = "0192e2e0-0000-7000-8000-00000000d0c1";

/** A customer chat, stood in for, holding these messages from Tolu. */
async function customerChat(page: Page, said: string[]) {
  const now = new Date().toISOString();
  const chat = { id: CUSTOMER, name: "Tolu Bello", phone: "+234 803 ••• 0099", last: said[said.length - 1], last_mine: false, last_at: now, unread: 0, window_open: true, window_until: null, stopped: false, archived: false, line: "whatsapp" };
  const messages = said.map((body, i) => ({ id: `cm${i}`, mine: false, kind: "text", body, read_at: now, created_at: now }));
  await page.route(
    (u) => u.pathname.startsWith("/v1/customer-chats"),
    (r) => {
      const p = new URL(r.request().url()).pathname;
      if (p === "/v1/customer-chats") return json(r, 200, { chats: [chat] });
      if (p === "/v1/customer-chats/unread") return json(r, 200, { chats: 0, messages: 0 });
      if (p === `/v1/customer-chats/${CUSTOMER}`) return json(r, 200, chat);
      if (p === `/v1/customer-chats/${CUSTOMER}/messages`) return json(r, 200, { messages, more: false });
      if (p.endsWith("/orders")) return json(r, 200, { orders: [] });
      return json(r, 200, {});
    },
  );
}

for (const width of [390, 360])
  test(`messages from two chats collected, paid together with one passkey, ${width} px`, async ({ browser }) => {
    const { page, link, supplierName } = await linked(browser, width, [NO_BANK, BANKED]);
    await readApi(page);
    const api = await collectApi(page);
    await fakePasskey(page);
    await customerChat(page, [NO_AMOUNT, OTHER]);

    // A link chat: Add to the list beside Pay this account.
    await page.goto(`/chat?with=${link}`);
    const pane = page.getByTestId("chat-pane");
    const msg = (t: string) => pane.getByTestId("chat-message").filter({ hasText: t });
    await expect(msg("send 45k").getByTestId("pay-chip")).toHaveText("Pay this account");
    await expect(msg("send 45k").getByTestId("pay-chip-add")).toHaveText("Add to the list");
    await expect(pane.getByTestId("pay-list-bar")).toHaveCount(0);
    await noAxe(page, "a chat with Add to the list", width === 390 ? "collect-chip" : undefined);
    await fits(page, width);

    // No bank read: which bank, the likely ones first; then added.
    await msg("send 45k").getByTestId("pay-chip-add").click();
    const choose = page.getByRole("dialog", { name: "Which bank is it with?" });
    await expect(choose.getByTestId("collect-bank-likely")).toHaveText(["Guaranty Trust Bank", "Access Bank"]);
    await noAxe(page, "choosing the bank to add");
    await choose.getByRole("button", { name: "Guaranty Trust Bank" }).click();
    await expect(choose).toBeHidden();
    await expect(page.locator(".toast").filter({ hasText: "Added: 1 to pay" })).toBeVisible();
    expect(api.s.calls.find((c) => c.method === "POST" && c.path === "/v1/pay-collection")?.body).toEqual({
      account_number: "0123456789",
      bank_name: "Guaranty Trust Bank",
      amount_kobo: 4_500_000,
      name: "Chidi Okeke",
      source: { kind: "link", chat: link, message: expect.any(String) },
    });
    const bar = pane.getByTestId("pay-list-bar");
    await expect(bar.getByTestId("pay-list-count")).toHaveText("1 to pay · ₦45,000");
    // On the list: the chip says so.
    await expect(msg("send 45k").getByTestId("pay-chip-listed")).toHaveText("On your list");
    await expect(msg("send 45k").getByTestId("pay-chip-add")).toHaveCount(0);
    // A bank read: added at once.
    await msg("Bayo").getByTestId("pay-chip-add").click();
    await expect(page.locator(".toast").filter({ hasText: "Added: 2 to pay" })).toBeVisible();
    await expect(bar.getByTestId("pay-list-count")).toHaveText("2 to pay · ₦95,000");

    // A customer chat: the same list at its foot; a message with no amount.
    await page.goto(`/chat?with=c-${CUSTOMER}`);
    const cpane = page.getByTestId("customer-pane");
    const cmsg = (t: string) => cpane.getByTestId("customer-message").filter({ hasText: t });
    const cbar = cpane.getByTestId("pay-list-bar");
    await expect(cbar.getByTestId("pay-list-count")).toHaveText("2 to pay · ₦95,000");
    await cmsg("Pay Tunde").getByTestId("pay-chip-add").click();
    await expect(cbar.getByTestId("pay-list-count")).toHaveText("3 to pay · ₦95,000");
    await expect(cbar.getByTestId("pay-list-needs")).toHaveText("1 needs an amount");
    expect(api.s.calls.filter((c) => c.method === "POST" && c.path === "/v1/pay-collection").pop()?.body.source).toEqual({ kind: "customer", chat: CUSTOMER, message: "cm0" });
    // (The toasts dismissed, so the foot is seen.)
    for (const x of await page.locator(".toast-close").all()) await x.click();
    await expect(page.locator(".toast")).toHaveCount(0);
    await noAxe(page, "the list at the foot of a chat", width === 390 ? "collect-foot-bar" : undefined);
    await fits(page, width);

    // Added elsewhere meanwhile (another device): the server says it is on
    // the list already, and the chip says so too.
    api.add({ account_number: "0444444444", bank_name: "Wema Bank", amount_kobo: 1_000_000, name: "Ngozi", source: { kind: "customer", chat: CUSTOMER, message: "cm1" } });
    await cmsg("My sister").getByTestId("pay-chip-add").click();
    await expect(page.locator(".toast").filter({ hasText: "That message is on your list already." })).toBeVisible();
    await expect(cmsg("My sister").getByTestId("pay-chip-listed")).toBeVisible();
    await expect(cbar.getByTestId("pay-list-count")).toHaveText("4 to pay · ₦105,000");
    // Removed from the list later, on the list's page.

    // Pay several's toolbar offers the list too.
    await page.goto("/payments/several");
    await expect(page.getByTestId("add-collected")).toHaveText("From your list (4)");
    await page.getByTestId("add-collected").click();
    await expect(page).toHaveURL(/\/payments\/several\/list$/);
    const rows = page.getByTestId("collect-row");
    await expect(rows).toHaveCount(4);
    await expect(rows.nth(0).getByTestId("collect-from")).toHaveText(`from ${supplierName}'s message`);
    await expect(rows.nth(0).getByTestId("collect-from").getByRole("link")).toHaveAttribute("href", new RegExp(`/chat\\?with=${link}&msg=`));
    await expect(rows.nth(2).getByTestId("collect-from")).toHaveText("from Tolu Bello's message");
    await expect(rows.nth(0)).toContainText("Guaranty Trust Bank ••6789");
    await expect(rows.nth(0).getByTestId("collect-amount")).toHaveValue("45,000");
    await expect(page.getByTestId("collect-total")).toHaveText("₦105,000");
    // Ngozi off the list.
    await rows.nth(3).getByRole("button", { name: "Remove Ngozi" }).click();
    await expect(rows).toHaveCount(3);
    expect(api.s.calls.some((c) => c.method === "DELETE" && c.path.startsWith("/v1/pay-collection/"))).toBe(true);
    await expect(page.getByTestId("collect-sub")).toHaveText("3 people · 1 needs an amount");

    // Tunde has no amount: the check is held up, said, and nothing is made.
    await page.getByTestId("collect-check").click();
    await expect(page.getByTestId("collect-blocked")).toHaveText("Say how much for 1 person first.");
    await expect(rows.nth(2).getByTestId("collect-problem")).toHaveText("How much?");
    expect(api.s.calls.some((c) => c.path === "/v1/pay-collection/batch")).toBe(false);
    await noAxe(page, "the list from messages, a row to fix", width === 390 ? "collect-list" : undefined);
    await fits(page, width);
    // Given one, and what it is for: saved, and the list checked.
    await rows.nth(2).getByTestId("collect-amount").fill("20k");
    await rows.nth(2).getByTestId("collect-amount").press("Tab");
    await expect(page.getByTestId("collect-total")).toHaveText("₦115,000");
    expect(api.s.calls.filter((c) => c.method === "PATCH").map((c) => c.body)).toEqual([{ amount_kobo: 2_000_000 }]);
    await rows.nth(2).getByTestId("collect-for").selectOption("wages");
    await expect.poll(() => api.s.rows[2]?.purpose).toBe("wages");
    await expect(rows.nth(2).getByTestId("collect-for")).toHaveValue("wages");
    await page.getByTestId("collect-check").click();
    await expect(page).toHaveURL(new RegExp(`/payments/batches/${api.B}$`));
    expect(api.s.calls.find((c) => c.path === "/v1/pay-collection/batch")?.body).toEqual({});
    await expect(page.getByTestId("batch-status")).toHaveText("To confirm");
    await expect(page.getByTestId("batch-amount")).toHaveText("₦115,000");

    // Waiting in the batch: the chat's foot says so, opening it.
    await page.goto(`/chat?with=${link}`);
    await expect(pane.getByTestId("pay-list-waiting")).toHaveText("Waiting to be sent");
    await pane.getByTestId("pay-list-waiting").click();
    await expect(page).toHaveURL(new RegExp(`/payments/batches/${api.B}$`));

    // Confirmed with one passkey, as any list is; sent, the list is empty.
    await page.getByTestId("batch-confirm").click();
    await expect(page.getByTestId("batch-status")).not.toHaveText("To confirm");
    await page.goto(`/chat?with=${link}`);
    await expect(pane.getByTestId("chat-message").first()).toBeVisible();
    await expect(pane.getByTestId("pay-list-bar")).toHaveCount(0);
  });

test("Pay these from the foot, and Clear the list", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signUp(page, `Clear List ${letters()}`);
  await readApi(page);
  const api = await collectApi(page);
  await customerChat(page, [NO_AMOUNT]);
  api.add({ account_number: "0222222222", bank_name: "Access Bank", amount_kobo: 5_000_000, name: "Bayo", source: { kind: "link", chat: "L1", message: "m1" } });
  api.add({ account_number: "0333333333", bank_name: "Zenith Bank", name: "Tunde", source: { kind: "customer", chat: CUSTOMER, message: "cm0" } });
  await page.goto(`/chat?with=c-${CUSTOMER}`);
  const pane = page.getByTestId("customer-pane");
  const bar = pane.getByTestId("pay-list-bar");
  await expect(bar.getByTestId("pay-list-count")).toHaveText("2 to pay · ₦50,000");
  await expect(pane.getByTestId("customer-message").getByTestId("pay-chip-listed")).toBeVisible();
  // Pay these opens the list; a row from a chat this browser never opened
  // says the kind of chat.
  await bar.getByTestId("pay-list-go").click();
  await expect(page).toHaveURL(/\/payments\/several\/list$/);
  await expect(page.getByTestId("collect-row").first().getByTestId("collect-from")).toHaveText("from a message from a linked business");
  await page.goBack();
  // Clear, behind More, asked first.
  await bar.getByTestId("pay-list-more").click();
  page.once("dialog", (d) => void d.accept());
  await page.getByRole("button", { name: "Clear the list" }).click();
  await expect(page.locator(".toast").filter({ hasText: "The list is cleared" })).toBeVisible();
  await expect(bar).toHaveCount(0);
  expect(api.s.calls.some((c) => c.method === "DELETE" && c.path === "/v1/pay-collection")).toBe(true);
  await expect(pane.getByTestId("customer-message").getByTestId("pay-chip-add")).toBeVisible();
});
