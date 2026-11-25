import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// After locking (P4b-4, docs/spec/chat-pay-people.md): a locked payroll's
// Payment, waiting for money, then paid with one person not paid (who and
// why); Pay the failed ones again with Payrolla's code; Pay now before the
// date; a payslip opened from the run, saved as PDF; and an employee's
// own payslips on My pay, with their card in the Taskiem chat. Payrolla
// is stood in for: the e2e server has no Payrolla to reach.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Payroll ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@payroll.test`,
      name: "Ngozi Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

const json = (r: Route, status: number, body: unknown) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

const RUN = "0192e2e0-0000-7000-8000-00000000d401";
const SLIP_Z = "0192e2e0-0000-7000-8000-00000000f401";
const SLIP_M = "0192e2e0-0000-7000-8000-00000000f402";

const ZAINAB_ITEMS = [
  { kind: "earning", code: "BASIC", label: "Basic", amount_kobo: 270_000_00 },
  { kind: "earning", code: "HOUSING", label: "Housing", amount_kobo: 112_500_00 },
  { kind: "earning", code: "TRANSPORT", label: "Transport", amount_kobo: 67_500_00 },
  { kind: "deduction", code: "PAYE", label: "Tax (PAYE)", amount_kobo: 31_500_00 },
  { kind: "deduction", code: "PENSION_EE", label: "Pension (yours)", amount_kobo: 36_000_00 },
  { kind: "employer", code: "PENSION_ER", label: "Pension (employer's)", amount_kobo: 45_000_00 },
];

const payslip = {
  payslip_id: SLIP_Z,
  run_id: RUN,
  period: "November 2026",
  month: "November",
  period_start: "2026-11-01",
  pay_date: "2026-11-28",
  employee: { id: "e1", name: "Zainab Bello", department: "Sales" },
  employer: { name: "Amara Foods Ltd", rc_number: "RC1234567" },
  gross_kobo: 450_000_00,
  total_earnings_kobo: 450_000_00,
  total_deductions_kobo: 67_500_00,
  net_kobo: 382_500_00,
  items: ZAINAB_ITEMS,
  payment: { status: "CONFIRMED", state: "paid", words: "Paid" },
};

function standIn(page: Page) {
  const s = { stage: "short" as "scheduled" | "short" | "some" | "paid", posts: [] as { path: string; body: unknown }[] };
  const summary = () => ({
    run_id: RUN,
    period_start: "2026-11-01",
    period_end: "2026-11-30",
    pay_date: "2026-11-28",
    run_type: "REGULAR",
    status: s.stage === "some" ? "PARTIALLY_COMPLETED" : s.stage === "paid" ? "COMPLETED" : "LOCKED",
    employee_count: 2,
    total_gross_kobo: 750_000_00,
    total_net_kobo: 640_000_00,
    awaiting_approval: false,
  });
  const lines = () => [
    {
      payslip_id: SLIP_Z,
      employee_id: "e1",
      name: "Zainab Bello",
      net_kobo: 382_500_00,
      status: s.stage === "some" || s.stage === "paid" ? "CONFIRMED" : "PENDING",
      state: s.stage === "some" || s.stage === "paid" ? "paid" : "waiting",
      staff: null,
      gross_kobo: 450_000_00,
      total_earnings_kobo: 450_000_00,
      total_deductions_kobo: 67_500_00,
      items: ZAINAB_ITEMS,
    },
    {
      payslip_id: SLIP_M,
      employee_id: "e2",
      name: "Musa Ali",
      net_kobo: 257_500_00,
      status: s.stage === "some" ? "FAILED" : s.stage === "paid" ? "CONFIRMED" : "PENDING",
      state: s.stage === "some" ? "failed" : s.stage === "paid" ? "paid" : "waiting",
      ...(s.stage === "some" ? { reason: "Account closed" } : {}),
      staff: null,
      gross_kobo: 300_000_00,
      total_earnings_kobo: 300_000_00,
      total_deductions_kobo: 42_500_00,
      items: [],
    },
  ];
  const payment = () => {
    const base = { total_kobo: 640_000_00, returned: 0, short_by_kobo: 0, may_payslips: true, cannot_pay: false, scheduled_for: "2026-11-28" };
    switch (s.stage) {
      case "scheduled":
        return { ...base, status: "SCHEDULED", words: "Pays on 28 Nov", tone: "info", paid: 0, failed: 0, pending: 2, may_pay_now: true, may_retry: false };
      case "short":
        return { ...base, status: "AWAITING_FUNDS", words: "Waiting for money: short by ₦120,000", tone: "warn", paid: 0, failed: 0, pending: 2, short_by_kobo: 120_000_00, may_pay_now: false, may_retry: false };
      case "some":
        return { ...base, status: "PARTIALLY_COMPLETED", words: "Paid, 1 not paid", tone: "warn", paid: 1, failed: 1, pending: 0, may_pay_now: false, may_retry: true };
      default:
        return { ...base, status: "COMPLETED", words: "Paid", tone: "ok", paid: 2, failed: 0, pending: 0, may_pay_now: false, may_retry: false };
    }
  };
  const view = () => ({
    connection: "payrolla",
    run: { summary: summary(), created_at: "2026-11-20T09:00:00Z", updated_at: "2026-11-20T09:00:00Z", approved_at: "2026-11-21T09:00:00Z", locked_at: "2026-11-21T10:00:00Z" },
    lines: lines(),
    counts: { paid: 0, waiting: 2, failed: 0, returned: 0 },
    adjustments: [],
    exclusions: [],
    changes: { new: [], left: [], pay_changed: [] },
    may: { prepare: false, approve: true, linked: true, reconnect: false },
    ...(summary().status === "LOCKED" ? { pays_on: "Pays on 28 Nov" } : {}),
    payment: payment(),
  });
  return page
    .route(
      (u) => u.pathname.startsWith("/v1/payroll"),
      async (r) => {
        const u = new URL(r.request().url());
        const method = r.request().method();
        const p = u.pathname;
        if (method !== "GET") s.posts.push({ path: p, body: r.request().postDataJSON() });
        if (p === `/v1/payroll/runs/${RUN}`) return json(r, 200, view());
        if (p === `/v1/payroll/runs/${RUN}/confirmations`)
          return json(r, 202, {
            challenge: { challenge_id: "ch1", channel: "TOTP", expires_at: "2026-11-24T10:05:00Z" },
            sent_to: "Type the code from your authenticator app.",
            safer: false,
          });
        if (p === `/v1/payroll/runs/${RUN}/retry-failed` || p === `/v1/payroll/runs/${RUN}/pay-now`) {
          const body = r.request().postDataJSON() as { code: string };
          if (body.code !== "482910") return json(r, 422, { error: "That code isn't right. Check it and type it again.", code: "code_invalid" });
          const retry = p.endsWith("/retry-failed");
          s.stage = "paid";
          return json(r, 200, {
            run: summary(),
            payment: { status: "COMPLETED", total_kobo: 640_000_00, paid: 2, failed: 0, returned: 0, pending: 0 },
            words: retry ? "Sent again. Payrolla pays each of them into their current account." : "Paying now. Payrolla pays 2 people ₦640,000.",
          });
        }
        if (p === `/v1/payroll/payslips/${SLIP_Z}` || p === `/v1/payroll/me/payslips/${SLIP_Z}`) return json(r, 200, { payslip });
        if (p === `/v1/payroll/payslips/${SLIP_Z}/pdf` || p === `/v1/payroll/me/payslips/${SLIP_Z}/pdf`)
          return r.fulfill({ status: 200, contentType: "application/pdf", body: "%PDF-1.4\n%stand-in\n" });
        if (p === `/v1/payroll/payslips/${SLIP_Z}/link`) return json(r, 201, { url: "https://payroller.test/p/abc", expires_at: "2026-12-01T10:00:00Z" });
        if (p === "/v1/payroll/me")
          return json(r, 200, {
            linked: true,
            me: { name: "Zainab Bello", phone: "+234 803 ••• 4567", status: "active", bank: { bank_code: "058", bank: "GTBank", account_name: "ZAINAB A. BELLO", last4: "6789" }, waiting_change: null },
          });
        if (p === "/v1/payroll/me/payslips")
          return json(r, 200, {
            linked: true,
            payslips: [
              { payslip_id: SLIP_Z, run_id: RUN, period: "November 2026", month: "November", pay_date: "2026-11-28", gross_kobo: 450_000_00, net_kobo: 382_500_00, state: "paid", words: "Paid" },
              { payslip_id: SLIP_M, run_id: "r-oct", period: "October 2026", month: "October", pay_date: "2026-10-31", gross_kobo: 450_000_00, net_kobo: 382_500_00, state: "paid", words: "Paid" },
            ],
          });
        return json(r, 404, { error: "not found", code: "not_found" });
      },
    )
    .then(() => s);
}

async function noAxe(page: Page, what: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describe(`${what} (${scheme})`, vs)).toEqual([]);
  }
  await page.emulateMedia({ colorScheme: "light" });
}

const fits = async (page: Page, width: number) => expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

for (const width of [390, 360]) {
  test(`a locked payroll waits for money, pays, and the failed one is paid again with Payrolla's code; a payslip opened and saved, ${width} px`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 844 });
    await signUp(page);
    const s = await standIn(page);

    // Waiting for money.
    await page.goto(`/payroll/runs/${RUN}`);
    const pay = page.getByTestId("payroll-payment");
    await expect(pay.getByTestId("payroll-payment-words")).toHaveText("Waiting for money: short by ₦120,000");
    await expect(pay.getByTestId("payroll-add-money")).toContainText("Add money to your Payrolla wallet");
    await expect(pay.getByTestId("payroll-pay-now")).toHaveCount(0);
    await fits(page, width);
    await noAxe(page, "a payroll waiting for money");

    // Paid, one not: who and why; paid again with the code.
    s.stage = "some";
    await page.reload();
    await expect(pay.getByTestId("payroll-payment-words")).toHaveText("Paid, 1 not paid");
    await expect(pay).toContainText("Paid 1 of 2 people, 1 not paid");
    await expect(page.getByTestId("payroll-line").filter({ hasText: "Musa Ali" }).getByTestId("payroll-line-reason")).toHaveText("Account closed");
    await fits(page, width);
    await noAxe(page, "a payroll paid but one");
    await pay.getByTestId("payroll-retry").click();
    const sheet = page.getByRole("dialog", { name: "Pay the failed ones again: November 2026 payroll" });
    await expect(sheet.getByTestId("payroll-retry-note")).toContainText("Payments the bank returned are not sent again.");
    await sheet.getByTestId("payroll-code-send").click();
    await sheet.getByTestId("payroll-code").fill("000000");
    await sheet.getByTestId("payroll-code-confirm").click();
    await expect(sheet.getByRole("alert")).toHaveText("That code isn't right. Check it and type it again.");
    await sheet.getByTestId("payroll-code").fill("482 910");
    await noAxe(page, "the code sheet, Pay the failed ones again");
    await sheet.getByTestId("payroll-code-confirm").click();
    await expect(page.getByTestId("payroll-said")).toHaveText("Sent again. Payrolla pays each of them into their current account.");
    await expect(pay.getByTestId("payroll-payment-words")).toHaveText("Paid");
    expect(s.posts.find((p) => p.path.endsWith("/confirmations"))?.body).toMatchObject({ action: "retry_failed" });
    const keys = s.posts.filter((p) => p.path.endsWith("/retry-failed")).map((p) => (p.body as { key: string }).key);
    expect(new Set(keys).size).toBe(keys.length);

    // Zainab's payslip, from the run: the breakdown, saved as PDF, a link.
    await page.getByTestId("payroll-line").filter({ hasText: "Zainab Bello" }).getByTestId("payroll-line-payslip").click();
    await expect(page).toHaveURL(new RegExp(`/payroll/payslips/${SLIP_Z}$`));
    await expect(page.getByRole("heading", { level: 1, name: "Payslip for November 2026" })).toBeVisible();
    await expect(page.getByTestId("payslip-employer")).toHaveText("Amara Foods Ltd · RC1234567");
    await expect(page.getByTestId("payslip-breakdown")).toContainText("Tax (PAYE)");
    await expect(page.getByTestId("payslip-breakdown")).toContainText("₦382,500");
    const download = page.waitForEvent("download");
    await page.getByTestId("payslip-pdf").click();
    expect((await download).suggestedFilename()).toBe("payslip-november-2026.pdf");
    await page.getByTestId("payslip-send-link").click();
    await expect(page.getByTestId("payslip-link")).toContainText("It works for 7 days");
    await fits(page, width);
    await noAxe(page, "a payslip");
  });
}

test("Pay now before the pay day, with Payrolla's code", async ({ page }) => {
  test.setTimeout(90_000);
  await page.setViewportSize({ width: 390, height: 844 });
  await signUp(page);
  const s = await standIn(page);
  s.stage = "scheduled";
  await page.goto(`/payroll/runs/${RUN}`);
  await expect(page.getByTestId("payroll-payment-words")).toHaveText("Pays on 28 Nov");
  await page.getByTestId("payroll-pay-now").click();
  const sheet = page.getByRole("dialog", { name: "Pay now: November 2026 payroll" });
  await expect(sheet.getByTestId("payroll-pay-now-warning")).toHaveText("Payrolla pays everyone now instead of on 28 Nov 2026.");
  await sheet.getByTestId("payroll-code-send").click();
  await sheet.getByTestId("payroll-code").fill("482910");
  await noAxe(page, "the code sheet, Pay now");
  await sheet.getByTestId("payroll-code-confirm").click();
  await expect(page.getByTestId("payroll-said")).toHaveText("Paying now. Payrolla pays 2 people ₦640,000.");
  expect(s.posts.find((p) => p.path.endsWith("/confirmations"))?.body).toMatchObject({ action: "pay_now" });
});

for (const width of [390, 360]) {
  test(`an employee's own payslips on My pay, ${width} px`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width, height: 844 });
    await signUp(page);
    await standIn(page);
    await page.goto("/payroll/me");
    const list = page.getByTestId("mypay-payslips");
    await expect(list.getByTestId("mypay-payslip")).toHaveCount(2);
    await expect(list.getByTestId("mypay-payslip").first()).toContainText("November 2026");
    await fits(page, width);
    await noAxe(page, "My pay with payslips");
    await list.getByTestId("mypay-payslip-open").first().click();
    await expect(page).toHaveURL(new RegExp(`/payroll/me/payslips/${SLIP_Z}$`));
    await expect(page.getByTestId("payslip-hero")).toContainText("₦382,500");
    await expect(page.getByTestId("payslip-send-link")).toHaveCount(0);
    await fits(page, width);
    await noAxe(page, "my payslip");
  });
}
