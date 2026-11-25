import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// This month's payroll in Taskiem (P4b-3, docs/spec/chat-pay-people.md):
// the Payroll page's Prepare November payroll; the payroll's page with each
// person's breakdown, a bonus added (before tax), someone left out and put
// back, the wallet's shortfall, Payrolla's refusal in words (the preparer
// can't approve it), Approve with the code Payrolla sends (a wrong one,
// then the right one) and Lock and pay with its warning; Reject with a
// note; and the payroll's card in the Taskiem chat. Payrolla is stood in
// for: the e2e server has no Payrolla to reach.

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

const RUN = "0192e2e0-0000-7000-8000-00000000d301";
const E_Z = "0192e2e0-0000-7000-8000-00000000e301";
const E_M = "0192e2e0-0000-7000-8000-00000000e302";
const E_A = "0192e2e0-0000-7000-8000-00000000e303";

const items = (gross: number, bonus = 0) => [
  { kind: "earning", code: "BASIC", label: "Basic", amount_kobo: gross * 0.6 },
  { kind: "earning", code: "HOUSING", label: "Housing", amount_kobo: gross * 0.25 },
  { kind: "earning", code: "TRANSPORT", label: "Transport", amount_kobo: gross * 0.15 },
  ...(bonus ? [{ kind: "earning", code: "BONUS", label: "Bonus: Q4 target", amount_kobo: bonus }] : []),
  { kind: "deduction", code: "PAYE", label: "Tax (PAYE)", amount_kobo: (gross + bonus) * 0.07 },
  { kind: "deduction", code: "PENSION_EE", label: "Pension (yours)", amount_kobo: gross * 0.08 },
  { kind: "employer", code: "PENSION_ER", label: "Pension (employer's)", amount_kobo: gross * 0.1 },
];

function standIn(page: Page) {
  const s = {
    status: "COMPUTED",
    prepared: false,
    bonus: 0,
    leftOut: false,
    posts: [] as { path: string; body: unknown }[],
    sod: false,
  };
  const people = () =>
    [
      { id: E_Z, name: "Zainab Bello", gross: 450_000_00, bonus: s.bonus },
      { id: E_M, name: "Musa Ali", gross: 300_000_00, bonus: 0 },
      ...(s.leftOut ? [] : [{ id: E_A, name: "Ada Obi", gross: 250_000_00, bonus: 0 }]),
    ].map((p) => {
      const it = items(p.gross, p.bonus);
      const earn = p.gross + p.bonus;
      const ded = it.filter((i) => i.kind === "deduction").reduce((a, i) => a + i.amount_kobo, 0);
      return {
        payslip_id: `p-${p.id}`,
        employee_id: p.id,
        name: p.name,
        net_kobo: earn - ded,
        status: "PENDING",
        state: "waiting",
        staff: null,
        gross_kobo: p.gross,
        total_earnings_kobo: earn,
        total_deductions_kobo: ded,
        items: it,
      };
    });
  const summary = () => {
    const lines = people();
    return {
      run_id: RUN,
      period_start: "2026-11-01",
      period_end: "2026-11-30",
      pay_date: "2026-11-28",
      run_type: "REGULAR",
      status: s.status,
      employee_count: lines.length,
      total_gross_kobo: lines.reduce((a, l) => a + l.total_earnings_kobo, 0),
      total_net_kobo: lines.reduce((a, l) => a + l.net_kobo, 0),
      awaiting_approval: s.status === "COMPUTED",
    };
  };
  const view = () => {
    const sum = summary();
    const short = sum.total_net_kobo - 800_000_00;
    return {
      connection: "payrolla",
      run: { summary: sum, created_at: "2026-11-20T09:00:00Z", updated_at: "2026-11-20T09:00:00Z", approved_at: null, locked_at: null },
      lines: people(),
      counts: { paid: 0, waiting: people().length, failed: 0, returned: 0 },
      adjustments: s.bonus ? [{ id: "adj1", employee_id: E_Z, name: "Zainab Bello", type: "bonus", amount_kobo: s.bonus, reason: "Q4 target" }] : [],
      exclusions: s.leftOut ? [{ id: "ex1", employee_id: E_A, name: "Ada Obi", reason: "unpaid leave" }] : [],
      changes: { since: "October 2026", new: ["Ada Obi"], left: [], pay_changed: [{ name: "Zainab Bello", from_kobo: 400_000_00, to_kobo: 450_000_00 }] },
      funding: {
        has_wallet: true,
        available_kobo: 800_000_00,
        needed_kobo: sum.total_net_kobo,
        short_by_kobo: short,
        words: `Your Payrolla wallet has ₦800,000. This payroll needs ₦${(sum.total_net_kobo / 100).toLocaleString("en-NG")}: short by ₦${(short / 100).toLocaleString("en-NG")}. Add money before 28 Nov.`,
      },
      may: { prepare: s.status === "COMPUTED", approve: true, linked: true, reconnect: false },
      ...(s.status === "LOCKED" ? { pays_on: "Pays on 28 Nov" } : {}),
    };
  };
  return page
    .route(
      (u) => u.pathname.startsWith("/v1/payroll"),
      async (r) => {
        const u = new URL(r.request().url());
        const method = r.request().method();
        const p = u.pathname;
        if (method !== "GET") s.posts.push({ path: p, body: r.request().postDataJSON() });
        if (p === "/v1/payroll")
          return json(r, 200, {
            connected: true,
            connection: "payrolla",
            test: true,
            auth: "oauth",
            is_owner: true,
            company: { name: "Amara Foods Ltd", pay_frequency: "MONTHLY" },
            runs: s.prepared ? [summary()] : [],
            may_prepare: true,
            this_month: { period: "2026-11", label: "November 2026", month: "November", ...(s.prepared ? { run: summary() } : {}) },
          });
        if (p === "/v1/payroll/runs" && method === "POST") {
          s.prepared = true;
          return json(r, 201, { run: summary() });
        }
        if (p === `/v1/payroll/runs/${RUN}`) return json(r, 200, view());
        if (p === `/v1/payroll/runs/${RUN}/adjustments`) {
          s.bonus = (r.request().postDataJSON() as { amount_kobo: number }).amount_kobo;
          return json(r, 201, { run: summary() });
        }
        if (p === `/v1/payroll/runs/${RUN}/exclusions`) {
          s.leftOut = true;
          return json(r, 201, { run: summary() });
        }
        if (p === `/v1/payroll/runs/${RUN}/exclusions/ex1`) {
          s.leftOut = false;
          return json(r, 200, { run: summary() });
        }
        if (p === `/v1/payroll/runs/${RUN}/confirmations`) {
          if (s.sod) return json(r, 403, { error: "You prepared this payroll, so someone else must approve it. Ask Musa Bello.", code: "sod" });
          return json(r, 202, {
            challenge: { challenge_id: "ch1", channel: "SMS", destination_hint: "•••• 4567", expires_at: "2026-11-24T10:05:00Z" },
            sent_to: "Payrolla sent a code to your phone ending 4567.",
            safer: true,
          });
        }
        if (p === `/v1/payroll/runs/${RUN}/approve` || p === `/v1/payroll/runs/${RUN}/lock`) {
          const body = r.request().postDataJSON() as { code: string };
          if (body.code !== "482910") return json(r, 422, { error: "That code isn't right. Check it and type it again.", code: "code_invalid" });
          const lock = p.endsWith("/lock");
          s.status = lock ? "LOCKED" : "APPROVED";
          return json(r, 200, {
            run: summary(),
            payment: { status: lock ? "SCHEDULED" : "NOT_SCHEDULED", total_kobo: summary().total_net_kobo, paid: 0, failed: 0, returned: 0, pending: 3 },
            words: lock ? "Locked. Payrolla pays 3 people ₦870,250 on 28 Nov." : "Approved. Lock and pay?",
          });
        }
        if (p === `/v1/payroll/runs/${RUN}/reject`) {
          s.status = "DRAFT";
          return json(r, 200, { run: summary(), words: "Sent back. Whoever prepared it is told, with your note." });
        }
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
  test(`November's payroll is prepared, a bonus added, someone left out, approved and locked with Payrolla's code, ${width} px`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 844 });
    await signUp(page);
    const s = await standIn(page);
    await page.goto("/payroll");
    const panel = page.getByTestId("payroll-this-month");
    await expect(panel).toContainText("This month");
    await fits(page, width);
    await noAxe(page, "Payroll, This month");
    await panel.getByTestId("payroll-prepare").click();
    await expect(page).toHaveURL(new RegExp(`/payroll/runs/${RUN}$`));
    expect(s.posts.find((p) => p.path === "/v1/payroll/runs")?.body).toMatchObject({ period: "2026-11" });

    // Each person, with the breakdown; the wallet is short; what changed.
    await expect(page.getByRole("heading", { level: 1, name: "Payroll for November 2026" })).toBeVisible();
    await expect(page.getByTestId("payroll-line")).toHaveCount(3);
    await expect(page.getByTestId("payroll-short")).toContainText("short by");
    await expect(page.getByTestId("payroll-changes")).toContainText("New this month: Ada Obi");
    const zainab = page.getByTestId("payroll-line").filter({ hasText: "Zainab Bello" });
    await zainab.getByText("See the breakdown").click();
    await expect(zainab.getByTestId("payroll-breakdown")).toContainText("Tax (PAYE)");
    await expect(zainab.getByTestId("payroll-breakdown")).toContainText("Pension (employer's)");
    await fits(page, width);
    await noAxe(page, "a payroll waiting for approval");

    // A bonus, before tax.
    await page.getByTestId("payroll-adjust").click();
    const adj = page.getByRole("dialog", { name: "Add a bonus or deduction" });
    await expect(adj).toContainText("Before tax: tax and pension apply as usual.");
    await adj.getByTestId("payroll-adjust-amount").fill("100k");
    await adj.getByTestId("payroll-adjust-reason").fill("Q4 target");
    await noAxe(page, "the bonus sheet");
    await adj.getByTestId("payroll-adjust-save").click();
    await expect(page.getByTestId("payroll-adjustment")).toContainText("₦100,000 bonus, Q4 target");
    expect(s.posts.find((p) => p.path.endsWith("/adjustments"))?.body).toMatchObject({ employee_id: E_Z, type: "bonus", amount_kobo: 100_000_00, reason: "Q4 target" });

    // Ada left out, then put back.
    await page.getByTestId("payroll-line").filter({ hasText: "Ada Obi" }).getByTestId("payroll-leave-out").click();
    const leave = page.getByRole("dialog", { name: "Leave Ada Obi out this month" });
    await leave.getByTestId("payroll-leave-reason").fill("unpaid leave");
    await leave.getByTestId("payroll-leave-save").click();
    await expect(page.getByTestId("payroll-excluded")).toContainText("unpaid leave");
    await expect(page.getByTestId("payroll-line")).toHaveCount(2);
    await page.getByTestId("payroll-put-back").click();
    await expect(page.getByTestId("payroll-line")).toHaveCount(3);

    // The preparer can't approve it: said before any code.
    s.sod = true;
    await page.getByTestId("payroll-approve").click();
    let sheet = page.getByRole("dialog", { name: "Approve: November 2026 payroll" });
    await sheet.getByTestId("payroll-code-send").click();
    await expect(sheet.getByRole("alert")).toContainText("You prepared this payroll, so someone else must approve it.");
    await sheet.getByRole("button", { name: "Cancel" }).click();
    s.sod = false;

    // Approve with the code: a wrong one, then the right one.
    await page.getByTestId("payroll-approve").click();
    sheet = page.getByRole("dialog", { name: "Approve: November 2026 payroll" });
    await sheet.getByTestId("payroll-code-send").click();
    await expect(sheet.getByTestId("payroll-code-sent")).toHaveText("Payrolla sent a code to your phone ending 4567. Type it here.");
    await sheet.getByTestId("payroll-code").fill("000 000");
    await sheet.getByTestId("payroll-code-confirm").click();
    await expect(sheet.getByRole("alert")).toHaveText("That code isn't right. Check it and type it again.");
    await fits(page, width);
    await noAxe(page, "the code sheet, a wrong code");
    await sheet.getByTestId("payroll-code").fill("482 910");
    await sheet.getByTestId("payroll-code-confirm").click();
    await expect(page.getByTestId("payroll-said")).toHaveText("Approved. Lock and pay?");

    // Lock and pay, with its warning.
    await page.getByTestId("payroll-lock").click();
    sheet = page.getByRole("dialog", { name: "Lock and pay: November 2026 payroll" });
    await expect(sheet.getByTestId("payroll-lock-warning")).toHaveText("Locking pays everyone on 28 Nov 2026. A locked payroll can't be unlocked.");
    await sheet.getByTestId("payroll-code-send").click();
    await sheet.getByTestId("payroll-code").fill("482910");
    await noAxe(page, "the code sheet, Lock and pay");
    await sheet.getByTestId("payroll-code-confirm").click();
    await expect(page.getByTestId("payroll-said")).toHaveText("Locked. Payrolla pays 3 people ₦870,250 on 28 Nov.");
    const codes = s.posts.filter((p) => p.path.endsWith("/approve") || p.path.endsWith("/lock")).map((p) => (p.body as { key: string }).key);
    expect(new Set(codes).size).toBe(codes.length);
    await fits(page, width);
    await noAxe(page, "a locked payroll");
  });
}

test("Reject sends the payroll back with a note", async ({ page }) => {
  test.setTimeout(90_000);
  await page.setViewportSize({ width: 390, height: 844 });
  await signUp(page);
  const s = await standIn(page);
  s.prepared = true;
  await page.goto(`/payroll/runs/${RUN}`);
  await page.getByTestId("payroll-reject").click();
  const sheet = page.getByRole("dialog", { name: "Send November 2026 payroll back" });
  await sheet.getByTestId("payroll-reject-note").fill("Musa's bonus is wrong");
  await noAxe(page, "the reject sheet");
  await sheet.getByTestId("payroll-reject-send").click();
  await expect(page.getByTestId("payroll-said")).toHaveText("Sent back. Whoever prepared it is told, with your note.");
  expect(s.posts.find((p) => p.path.endsWith("/reject"))?.body).toMatchObject({ note: "Musa's bonus is wrong" });
});
