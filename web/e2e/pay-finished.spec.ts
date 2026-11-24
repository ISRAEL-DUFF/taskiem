import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Paying one person, finished (docs/spec/build-plan-gaps.md H7): the Pay
// sheet's balance and Add money; the fee and the balance after before
// confirming; Send them a receipt; Save as a beneficiary; the receipt
// once the bank has it; the chat's diary line turning to "Received by
// the bank", or "Failed" with Try again; a batch's foot with its fees and
// by how much the balance is short; Pay it back on an order's refund. The
// e2e server has no payments account to pay from, so the payments API is
// stood in for; axe in both themes, at 390 and 360 px.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Pay Finished ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@payh7.test`,
      name: "Ada Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

const json = (r: Route, status: number, body: unknown) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

const ID = "0192e2e0-0000-7000-8000-00000000d001";
const AGAIN = "0192e2e0-0000-7000-8000-00000000d002";
const B1 = "0192e2e0-0000-7000-8000-00000000d101";
const payment = (over: Record<string, unknown> = {}) => ({
  id: ID,
  saved_id: null,
  personal: false,
  payee: "CHIDI OKEKE",
  bank_name: "Guaranty Trust Bank",
  account: "••6789",
  account_name: "CHIDI OKEKE",
  amount_kobo: 4_500_000,
  purpose: "delivery",
  purpose_note: "",
  note: "",
  first_payment: true,
  changed_account: false,
  needs_ok: "",
  status: "prepared",
  reason: null,
  fee_kobo: null,
  run_id: null,
  started_from: "tools",
  prepared_by: "user:ada",
  confirmed_by: null,
  created_at: new Date().toISOString(),
  confirmed_at: null,
  finished_at: null,
  again: false,
  fee_quote_kobo: 2500,
  route: "bank",
  tell_payee: true,
  send_receipt: false,
  may_tell: false,
  balance_kobo: 10_000_000,
  ...over,
});

async function noAxe(page: Page, what: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describe(`${what} (${scheme})`, vs)).toEqual([]);
  }
  await page.emulateMedia({ colorScheme: "light" });
}

const noSideways = async (page: Page, width: number) =>
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

for (const width of [390, 360])
  test(`the Pay sheet: balance, the fee before paying, a receipt and a beneficiary, ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await signUp(page);
    const calls: { method: string; path: string; body: Record<string, unknown> }[] = [];
    let paid = false;
    await page.route(
      (u) => u.pathname.startsWith("/v1/payments") || u.pathname === "/v1/saved" || u.pathname === "/v1/pay-limits",
      async (r) => {
        const req = r.request();
        const path = new URL(req.url()).pathname;
        const body = (req.postDataJSON() ?? {}) as Record<string, unknown>;
        calls.push({ method: req.method(), path, body });
        if (path === "/v1/pay-limits") return json(r, 200, { limits: [] });
        if (path === "/v1/payments" && req.method() === "GET") return json(r, 200, { payments: [] });
        if (path === "/v1/payments/balance")
          return json(r, 200, {
            from: "your Taskiem wallet", available_kobo: 10_000_000, wallet: true, quotes_fee: true,
            add_money: { bank_name: "Wema Bank", account_number: "1100110011", account_name: "Taskiem / Pay Finished" },
          });
        if (path === "/v1/payments/banks") return json(r, 200, { from: "iswallet/taskiem", banks: [{ code: "058", name: "Guaranty Trust Bank", likely: true }, { code: "044", name: "Access Bank" }] });
        if (path === "/v1/payments" && req.method() === "POST") return json(r, 201, { payment: payment() });
        if (path.endsWith("/confirm")) {
          if (!body.totp) return json(r, 403, { error: "confirm with your code", step_up: "required", methods: ["totp"], operation: "money.pay", target: `payment:${ID}` });
          paid = true;
          return json(r, 202, { payment: payment({ status: "sending", confirmed_at: new Date().toISOString(), send_receipt: body.send_receipt === true }) });
        }
        if (path === `/v1/payments/${ID}/receipt`)
          return json(r, 200, { url: "http://localhost/v1/pay-receipts/abc/pdf", receipt: { business: "Pay Finished", payee: "CHIDI OKEKE", bank_name: "Guaranty Trust Bank", account: "••6789", amount_kobo: 4_500_000, for: "a delivery", paid_at: new Date().toISOString(), reference: "ABC", route: "Bank transfer" } });
        if (path === `/v1/payments/${ID}/share`) return json(r, 201, { shared: "taskiem" });
        if (path === `/v1/payments/${ID}`)
          return json(r, 200, { payment: payment(paid ? { status: "paid", fee_kobo: 2500, finished_at: new Date().toISOString() } : {}) });
        if (path === "/v1/saved" && req.method() === "POST") return json(r, 201, { saved: { id: "s1", name: body.name } });
        return json(r, 404, { error: "not found" });
      },
    );
    await page.goto("/payments");
    await page.getByTestId("pay-account").click();
    const sheet = page.getByTestId("pay-sheet");
    // What people are paid from, and Add money: the wallet's own number.
    await expect(sheet.getByTestId("pay-balance")).toContainText("Balance ₦100,000");
    await expect(sheet.getByTestId("pay-balance")).toContainText("your Taskiem wallet");
    await sheet.getByTestId("pay-add-money").click();
    await expect(sheet.getByTestId("pay-add-details")).toContainText("1100110011");
    await sheet.getByTestId("pay-field-number").fill("0123456789");
    await sheet.getByRole("button", { name: "Guaranty Trust Bank" }).first().click();
    await sheet.getByLabel("How much? in naira").fill("45k");
    await sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "a delivery" }).click();
    await noAxe(page, "the pay form with the balance");
    await noSideways(page, width);
    await sheet.getByTestId("pay-prepare").click();

    // Before confirming: the fee, and the balance after with it.
    await expect(sheet.getByTestId("pay-name")).toHaveText("CHIDI OKEKE");
    await expect(sheet.getByTestId("pay-fee")).toHaveText("₦45,000 + ₦25 fee");
    await expect(sheet.getByTestId("pay-balance-after")).toHaveText("₦54,975");
    // Not someone Taskiem can tell: Send them a receipt; and a new account
    // is saved as a beneficiary by default, with a name.
    await expect(sheet.getByTestId("pay-tell")).toHaveCount(0);
    await expect(sheet.getByTestId("pay-send-receipt")).not.toBeChecked();
    await sheet.getByTestId("pay-send-receipt").check();
    await expect(sheet.getByTestId("pay-save-as")).toBeChecked();
    await expect(sheet.getByTestId("pay-save-name")).toHaveValue("chidi");
    await noAxe(page, "the payment to confirm, with its fee");
    await noSideways(page, width);

    await sheet.getByTestId("pay-confirm").click();
    await sheet.getByTestId("pay-code").fill("123456");
    await sheet.getByRole("button", { name: "Confirm with the code" }).click();
    await expect(sheet.getByTestId("pay-sent")).toBeVisible();
    const confirms = calls.filter((c) => c.path.endsWith("/confirm")).map((c) => c.body);
    expect(confirms.at(-1)).toEqual({ send_receipt: true, again: false, totp: "123456" });
    await expect.poll(() => calls.find((c) => c.method === "POST" && c.path === "/v1/saved")?.body).toEqual({
      kind: "bank_account", name: "chidi", title: "CHIDI OKEKE", bank_name: "Guaranty Trust Bank", account_number: "0123456789",
    });
    // The bank has it: received, and its receipt.
    await expect(sheet.getByTestId("pay-sent")).toContainText("Received by the bank", { timeout: 10_000 });
    await expect(sheet.getByTestId("pay-receipt-pdf")).toHaveAttribute("href", `/v1/payments/${ID}/receipt/pdf`);
    await noAxe(page, "paid, with its receipt");
    await sheet.getByTestId("pay-receipt-chat").click();
    await expect.poll(() => calls.find((c) => c.path === `/v1/payments/${ID}/share`)?.body).toEqual({ where: "taskiem" });
  });

for (const width of [390, 360])
  test(`the chat's diary line: received, or failed with Try again, ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await signUp(page);
    const FAILED = "0192e2e0-0000-7000-8000-00000000d003";
    const now = new Date().toISOString();
    await page.route("**/v1/taskiem-chat**", (r) => {
      if (r.request().method() !== "GET") return r.continue();
      return json(r, 200, {
        messages: [
          { id: "22222222-0000-4000-8000-0000000000d1", channel: "web", inbound: false, kind: "diary", created_at: now, payment: ID, text: "Paid CHIDI OKEKE ₦45,000 (delivery) · Guaranty Trust Bank ••6789" },
          { id: "22222222-0000-4000-8000-0000000000d2", channel: "web", inbound: false, kind: "diary", created_at: now, payment: FAILED, text: "Paid MUSA BELLO ₦3,000 (delivery) · Access Bank ••2108" },
          { id: "22222222-0000-4000-8000-0000000000d3", channel: "web", inbound: false, kind: "receipt", created_at: now, payment: ID, text: "Paid CHIDI OKEKE ₦45,000 for a delivery on 24 Nov 2026 · Guaranty Trust Bank ••6789." },
          { id: "22222222-0000-4000-8000-0000000000d4", channel: "web", inbound: false, kind: "diary", created_at: now, batch: B1, text: "Paid 2 people ₦30,000 (riders, week 45)" },
        ],
      });
    });
    let again = 0;
    await page.route("**/v1/payments/**", (r) => {
      const path = new URL(r.request().url()).pathname;
      if (path === `/v1/payments/${ID}`) return json(r, 200, { payment: payment({ status: "paid", fee_kobo: 2500 }) });
      if (path === `/v1/payments/${FAILED}`) return json(r, 200, { payment: payment({ id: FAILED, status: "failed", reason: "The bank refused it" }) });
      if (path === `/v1/payments/${FAILED}/again`) {
        again++;
        return json(r, 201, { payment: payment({ id: AGAIN }) });
      }
      if (path === `/v1/payments/${AGAIN}`) return json(r, 200, { payment: payment({ id: AGAIN }) });
      return json(r, 404, { error: "not found" });
    });
    await page.route(`**/v1/payment-batches/${B1}`, (r) =>
      json(r, 200, {
        batch: {
          id: B1, title: "riders, week 45", kind: "list", status: "sent", created_by: "user:ada", created_at: now, confirmed_at: now,
          payments: [payment({ status: "paid" }), payment({ id: FAILED, status: "sending" })], total_kobo: 3_000_000, count: 2, paid_kobo: 1_000_000, waiting: 0, failed: 0,
        },
      }),
    );
    await page.goto("/chat?with=taskiem");
    const lines = page.getByTestId("chat-diary");
    await expect(lines.filter({ hasText: "CHIDI OKEKE" })).toContainText("Received by the bank");
    await expect(lines.filter({ hasText: "MUSA BELLO" })).toContainText("Failed: refunded to your balance");
    await expect(lines.filter({ hasText: "Paid 2 people" }).getByTestId("pay-diary-state")).toBeVisible();
    await expect(page.getByTestId("pay-receipt-card")).toContainText("Receipt");
    await noAxe(page, "payments in the chat");
    await noSideways(page, width);
    await lines.filter({ hasText: "MUSA BELLO" }).getByTestId("pay-try-again").click();
    await expect(page).toHaveURL(new RegExp(`/payments/${AGAIN}$`));
    expect(again).toBe(1);
  });

test("a batch's foot: the fees, and by how much the balance is short", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await signUp(page);
  const now = new Date().toISOString();
  await page.route(`**/v1/payment-batches/${B1}`, (r) =>
    json(r, 200, {
      batch: {
        id: B1, title: "riders, week 45", kind: "list", status: "prepared", created_by: "user:ada", created_at: now, confirmed_at: null,
        payments: [payment({ id: "0192e2e0-0000-7000-8000-00000000d011", amount_kobo: 1_000_000, batch_id: B1 }), payment({ id: "0192e2e0-0000-7000-8000-00000000d012", amount_kobo: 2_000_000, batch_id: B1 })],
        total_kobo: 3_000_000, count: 2, paid_kobo: 0, waiting: 0, failed: 0, fees_kobo: 5000, fees_unknown: 0, balance_kobo: 2_500_000, short_kobo: 505_000,
        fit: ["0192e2e0-0000-7000-8000-00000000d011"],
      },
    }),
  );
  await page.goto(`/payments/batches/${B1}`);
  await expect(page.getByTestId("batch-fees")).toHaveText("₦50");
  await expect(page.getByTestId("batch-short")).toHaveText("Your balance is ₦25,000: ₦5,050 short for all 2 people. The first one fits: take some out, or add money first.");
  await expect(page.getByTestId("batch-balance-after")).toHaveCount(0);
  await noAxe(page, "a batch short of its balance");
  await noSideways(page, 390);
});

test("an order's refund: Pay it back opens the Pay sheet, filled in with a refund", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await signUp(page);
  const now = new Date().toISOString();
  await page.route("**/v1/storefront/orders/TK-ABC123", (r) =>
    json(r, 200, {
      revealed: false,
      order: {
        id: "0192e2e0-0000-7000-8000-00000000e001", reference: "TK-ABC123", kind: "order", status: "refund_due", customer: { name: "Bola Ade" },
        basket: [{ id: "1", name: "Jollof rice", quantity: 2, price_kobo: 250_000, line_kobo: 500_000 }], total_kobo: 500_000, payment: "before_accept",
        paid_by: "transfer", paid_at: now, created_at: now,
      },
    }),
  );
  await page.route((u) => u.pathname.startsWith("/v1/payments"), (r) => {
    const path = new URL(r.request().url()).pathname;
    if (path === "/v1/payments/banks") return json(r, 200, { from: "paystack/main", banks: [{ code: "058", name: "Guaranty Trust Bank" }] });
    return json(r, 404, { error: "not found" });
  });
  await page.goto("/storefront/orders/TK-ABC123");
  await page.getByTestId("refund-pay").click();
  const sheet = page.getByTestId("pay-sheet");
  await expect(sheet.getByLabel("How much? in naira")).toHaveValue("5000");
  await expect(sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "a refund" })).toHaveAttribute("aria-pressed", "true");
  await expect(sheet.getByTestId("refund-pay-lead")).toBeVisible();
  await noAxe(page, "paying a refund back");
  await noSideways(page, 390);
});
