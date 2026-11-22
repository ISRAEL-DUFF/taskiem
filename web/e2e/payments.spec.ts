import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Paying one person (docs/spec/chat-pay-people.md, P1): the Pay people
// page, the Pay sheet (prepared, the bank's name large, warnings, "pay
// again?", a code for the step-up), one payment's page (the chat's link)
// and the chat's Pay topic opening the sheet instead of sending words.
// The e2e server has no payments account to pay from, so the payments API
// is stood in for here; the pages, the sheet and the order of the calls
// are what is tested. Axe in both themes, at 390 and 360 px.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Pay Kitchen ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@pay.test`,
      name: "Ada Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

const ID = "0192e2e0-0000-7000-8000-00000000a001";
const payment = (over: Record<string, unknown> = {}) => ({
  id: ID,
  saved_id: "0192e2e0-0000-7000-8000-00000000b001",
  personal: false,
  payee: "Bayo Rice Mills",
  bank_name: "GTBank",
  account: "••6789",
  account_name: "BAYO ADEWALE",
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
  ...over,
});

const json = (r: Route, status: number, body: unknown) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

/** Stands in for the payments API; returns what was asked of it. */
async function payApi(page: Page, list: unknown[] = []) {
  const calls: { method: string; path: string; body: Record<string, unknown> }[] = [];
  let confirms = 0;
  await page.route(
    (u) => u.pathname.startsWith("/v1/payments") || u.pathname === "/v1/pay-limits" || u.pathname.startsWith("/v1/pay-limits/"),
    async (r) => {
      const req = r.request();
      const path = new URL(req.url()).pathname;
      const body = (req.postDataJSON() ?? {}) as Record<string, unknown>;
      calls.push({ method: req.method(), path, body });
      if (path === "/v1/pay-limits") return json(r, 200, { limits: [{ role: "cashier", one_kobo: 5_000_000, day_kobo: 20_000_000 }] });
      if (path.startsWith("/v1/pay-limits/")) return json(r, 200, { role: path.split("/").pop(), ...body });
      if (path === "/v1/payments" && req.method() === "GET") return json(r, 200, { payments: list });
      if (path === "/v1/payments" && req.method() === "POST") return json(r, 201, { payment: payment({ started_from: body.from }) });
      if (path.endsWith("/cancel")) return json(r, 200, { payment: payment({ status: "cancelled" }) });
      if (path.endsWith("/confirm")) {
        confirms++;
        if (!body.again) return json(r, 409, { error: "You paid Bayo Rice Mills ₦45,000 in the last day. Pay again?", code: "pay_again" });
        if (!body.totp && !body.passkey)
          return json(r, 403, { error: "confirm with your passkey or authenticator code", step_up: "required", methods: ["totp"], operation: "money.pay", target: `payment:${ID}` });
        return json(r, 202, { payment: payment({ status: "waiting_ok", needs_ok: "limit", confirmed_at: new Date().toISOString() }) });
      }
      if (path === `/v1/payments/${ID}`) return json(r, 200, { payment: payment() });
      return json(r, 404, { error: "not found" });
    },
  );
  await page.route("**/v1/chat/actions/*/choices", (r) => json(r, 200, { fields: { who: ["@bayo", "@chidi"] } }));
  return { calls, confirms: () => confirms };
}

async function noAxe(page: Page, what: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describe(`${what} (${scheme})`, vs)).toEqual([]);
  }
  await page.emulateMedia({ colorScheme: "light" });
}

for (const width of [390, 360])
  test(`the Pay people page and the Pay sheet, ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await signUp(page);
    const api = await payApi(page, [
      payment({ status: "paid", fee_kobo: 2500 }),
      payment({ id: "0192e2e0-0000-7000-8000-00000000a002", payee: "Account ••1234", account_name: "CHIDI OKEKE", account: "••1234", bank_name: "Opay", status: "failed", reason: "account closed", amount_kobo: 1_000_000, purpose: "other", purpose_note: "fuel" }),
    ]);
    await page.goto("/payments");
    await expect(page.getByRole("heading", { name: "Pay people", level: 1 })).toBeVisible();
    const rows = page.getByTestId("payment");
    await expect(rows).toHaveCount(2);
    await expect(rows.first()).toContainText("BAYO ADEWALE");
    await expect(rows.first()).toContainText("₦45,000");
    await expect(rows.first()).toContainText("GTBank ••6789 · A delivery");
    await expect(rows.first()).toContainText("Paid");
    await expect(rows.nth(1)).toContainText("Not paid");
    await expect(rows.nth(1)).toContainText("Other: fuel");
    // The owner sees what each role may pay.
    await expect(page.getByTestId("pay-limit").filter({ hasText: "Cashier" })).toContainText("Up to ₦50,000 a payment, ₦200,000 a day");
    await noAxe(page, "payments");
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

    // A role's limit changed.
    await page.getByRole("button", { name: "Change the limit for Cashier" }).click();
    const limit = page.getByTestId("pay-limit-form");
    await limit.getByLabel("One payment, at most").fill("30k");
    await limit.getByLabel("A day's total, at most").fill("100,000");
    await limit.getByRole("button", { name: "Save the limit" }).click();
    await expect(limit).toBeHidden();
    expect(api.calls.find((c) => c.method === "PUT")).toMatchObject({ path: "/v1/pay-limits/cashier", body: { one_kobo: 3_000_000, day_kobo: 10_000_000 } });

    // Pay someone: a saved account from the chips, 45k, for a delivery.
    await page.getByTestId("pay-someone").click();
    const sheet = page.getByTestId("pay-sheet");
    await sheet.getByRole("group", { name: "Who are you paying?" }).getByRole("button", { name: "@bayo" }).click();
    await sheet.getByLabel("How much? in naira").fill("45k");
    await sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "a delivery" }).click();
    await expect(sheet.getByTestId("pay-prepare")).toHaveText("Check and pay ₦45,000");
    await noAxe(page, "the pay form");
    await sheet.getByTestId("pay-prepare").click();
    expect(api.calls.find((c) => c.method === "POST" && c.path === "/v1/payments")?.body).toEqual({
      from: "tools",
      payee: "@bayo",
      amount_kobo: 4_500_000,
      purpose: "delivery",
    });

    // Prepared: the bank's name large, the facts, the first payment said.
    await expect(sheet.getByTestId("pay-name")).toHaveText("BAYO ADEWALE");
    await expect(sheet.getByTestId("pay-amount")).toHaveText("₦45,000");
    await expect(sheet.getByTestId("pay-warn-first")).toHaveText("First payment to this account.");
    await noAxe(page, "the prepared payment");
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

    // Confirm: "pay again?" first, then the code, then it waits for the OK.
    await sheet.getByTestId("pay-confirm").click();
    await expect(sheet.getByTestId("pay-again")).toContainText("Pay again?");
    await expect(sheet.getByTestId("pay-confirm")).toHaveText("Pay ₦45,000 again");
    await sheet.getByTestId("pay-confirm").click();
    await sheet.getByTestId("pay-code").fill("123456");
    await sheet.getByRole("button", { name: "Confirm with the code" }).click();
    await expect(sheet.getByTestId("pay-sent")).toContainText("Sent for the owner's OK");
    await expect(sheet.getByTestId("pay-sent")).toContainText("over the payer's limit");
    const confirms = api.calls.filter((c) => c.path.endsWith("/confirm")).map((c) => c.body);
    expect(confirms).toEqual([{ again: false }, { again: true }, { again: true, totp: "123456" }]);
    await sheet.getByRole("button", { name: "Done" }).click();
    await expect(sheet).toBeHidden();
  });

test("one payment's page: checked, then confirmed or cancelled", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await signUp(page);
  await payApi(page);
  // A changed account: said in red before anything else.
  let cancelled = false;
  await page.route(`**/v1/payments/${ID}**`, (r) => {
    if (r.request().url().endsWith("/cancel")) cancelled = true;
    return json(r, 200, {
      payment: payment({ changed_account: true, first_payment: false, account_name: null, purpose: "stock", note: "rice", status: cancelled ? "cancelled" : "prepared" }),
    });
  });
  await page.goto(`/payments/${ID}`);
  await expect(page.getByTestId("payment-hero")).toContainText("₦45,000");
  await expect(page.getByTestId("payment-status")).toHaveText("To confirm");
  await expect(page.getByTestId("pay-name")).toHaveText("Bayo Rice Mills");
  await expect(page.getByTestId("pay-warn-changed")).toContainText("Not the account you paid Bayo Rice Mills before (GTBank ••6789)");
  await expect(page.getByTestId("pay-warn-unchecked")).toBeVisible();
  await expect(page.getByText("Stock bought").first()).toBeVisible();
  await noAxe(page, "a prepared payment");
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);

  await page.getByTestId("payment-cancel").click();
  await expect(page.getByTestId("payment-status")).toHaveText("Cancelled");
  expect(cancelled).toBe(true);
  await expect(page.getByTestId("payment-confirm")).toBeHidden();
});

test("the chat's Pay topic opens the Pay sheet, filled, and sends no words", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await signUp(page);
  const api = await payApi(page);
  await page.route("**/v1/chat/actions", (r) =>
    json(r, 200, {
      topics: [
        {
          id: "pay", name: "Pay", icon: "money", color: "teal",
          actions: [
            {
              id: "pay.person", title: "Pay someone", line: "From your balance, checked with the bank", icon: "money", does: "asks", opens: "form", tool: "pay", confirm: "passkey",
              fields: [
                { key: "who", label: "Who are you paying?", hint: "One of your saved accounts, like @chidi.", kind: "payee" },
                { key: "amount", label: "How much?", kind: "money" },
                { key: "for", label: "What is it for?", kind: "choice", choices: ["a bill", "wages", "a delivery", "a refund", "stock bought", "other"] },
                { key: "note", label: "A few words for their bank statement?", kind: "text", optional: true },
              ],
            },
            {
              id: "pay.account", title: "Pay an account number", line: "Any bank: its name is checked first", icon: "money", does: "asks", opens: "form", tool: "pay", add: true, confirm: "passkey",
              fields: [
                { key: "number", label: "What is the account number?", hint: "10 digits.", kind: "account" },
                { key: "bank", label: "Which bank is it with?", kind: "bank" },
                { key: "amount", label: "How much?", kind: "money" },
                { key: "for", label: "What is it for?", kind: "choice", choices: ["a bill", "wages", "a delivery", "a refund", "stock bought", "other"] },
              ],
            },
          ],
        },
      ],
    }),
  );
  // Taskiem picked Pay an account number with what it read in the message.
  let said = 0;
  await page.route("**/v1/taskiem-chat", (r) => {
    if (r.request().method() !== "POST") return r.continue();
    said++;
    return json(r, 200, {
      messages: [{
        id: "22222222-0000-4000-8000-0000000000a1", channel: "web", inbound: false, kind: "menu", created_at: new Date().toISOString(),
        text: JSON.stringify({ topic: "pay", pick: "pay.account", values: { number: "0123456789", bank: "Opay", amount: "45k" }, line: "About Pay." }),
      }],
    });
  });
  await page.route("**/v1/payments/banks*", (r) => json(r, 200, { from: "paystack", banks: [{ code: "058", name: "Guaranty Trust Bank" }, { code: "999992", name: "OPay Digital Services" }] }));
  await page.goto("/chat?with=taskiem");
  await page.getByTestId("taskiem-pane").locator("#taskiem-text").fill("send 45k to 0123456789 opay");
  await page.getByTestId("taskiem-pane").locator("#taskiem-text").press("Enter");
  const card = page.getByTestId("taskiem-menu").last();
  await card.getByRole("button", { name: /Pay an account number/ }).click();
  const sheet = page.getByTestId("pay-sheet");
  await expect(sheet.getByTestId("pay-field-number")).toHaveValue("0123456789");
  // "Opay" from the message: the bank in the list it matches.
  await expect(sheet.getByTestId("pay-bank")).toHaveValue("OPay Digital Services");
  await expect(sheet.getByLabel("How much? in naira")).toHaveValue("45k");
  await sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "stock bought" }).click();
  await noAxe(page, "the chat's pay sheet");
  await sheet.getByTestId("pay-prepare").click();
  await expect(sheet.getByTestId("pay-name")).toHaveText("BAYO ADEWALE");
  expect(api.calls.find((c) => c.method === "POST" && c.path === "/v1/payments")?.body).toEqual({
    from: "chat",
    account_number: "0123456789",
    bank_name: "OPay Digital Services",
    amount_kobo: 4_500_000,
    purpose: "stock",
  });
  // Change: the prepared payment is cancelled and the form is back.
  await sheet.getByRole("button", { name: "Change" }).click();
  await expect(sheet.getByTestId("pay-prepare")).toBeVisible();
  await expect.poll(() => api.calls.some((c) => c.path.endsWith("/cancel"))).toBe(true);
  // Only the one message was sent: the form's words never were.
  expect(said).toBe(1);
});

test("a batch row whose OK did not reach it: confirmed again from its page", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await signUp(page);
  await payApi(page);
  let confirmed = 0;
  await page.route(`**/v1/payments/${ID}**`, (r) => {
    if (r.request().method() === "POST" && r.request().url().endsWith("/confirm")) {
      confirmed++;
      return json(r, 202, { payment: payment({ status: "sending", awaits_your_ok: false, run_id: "0192e2e0-0000-7000-8000-00000000f001" }) });
    }
    return json(r, 200, { payment: payment({ status: "sending", awaits_your_ok: confirmed === 0, run_id: "0192e2e0-0000-7000-8000-00000000f001" }) });
  });
  await page.goto(`/payments/${ID}`);
  await expect(page.getByTestId("payment-awaits")).toContainText("your OK did not reach this payment");
  await noAxe(page, "a payment waiting for your OK");
  await page.getByTestId("payment-confirm-again").click();
  await expect(page.getByTestId("payment-confirm-again")).toBeHidden();
  expect(confirmed).toBe(1);
});
