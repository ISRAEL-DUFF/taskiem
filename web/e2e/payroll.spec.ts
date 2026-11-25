import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Payroll in Taskiem, on Payrolla (P4a): payroll is Payrolla's, and
// Taskiem shows it in its own screens. Without a Payrolla connection the
// page says where payroll is and how to connect (the real server); with
// one, the runs, the latest on pine, Show more, one run with Payrolla's
// approval button and a person not paid with why, and an owner confirming
// who a staff member is in Payrolla. Payrolla's answers are stood in for:
// the e2e server has no Payrolla to read.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Payroll ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@payroll.test`,
      name: "Ada Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

const json = (r: Route, status: number, body: unknown) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

const RUN1 = "0192e2e0-0000-7000-8000-00000000d101";
const RUN2 = "0192e2e0-0000-7000-8000-00000000d102";
const RUN3 = "0192e2e0-0000-7000-8000-00000000d103";
const MUSA = "0192e2e0-0000-7000-8000-00000000a101";
const KEMI = "0192e2e0-0000-7000-8000-00000000a102";
const E_MUSA = "0192e2e0-0000-7000-8000-00000000e101";
const E_KEMI = "0192e2e0-0000-7000-8000-00000000e102";
const E_TUNDE = "0192e2e0-0000-7000-8000-00000000e103";

const run = (id: string, start: string, end: string, status: string, net: number) => ({
  run_id: id,
  period_start: start,
  period_end: end,
  pay_date: end,
  run_type: "REGULAR",
  status,
  employee_count: 3,
  total_gross_kobo: net + 30_000_00,
  total_net_kobo: net,
  awaiting_approval: status === "COMPUTED",
});

const RUNS = {
  connected: true,
  connection: "payrolla",
  test: true,
  company: { name: "Mama's Kitchen Ltd", pay_frequency: "MONTHLY" },
  runs: [run(RUN1, "2026-11-01", "2026-11-30", "COMPUTED", 450_000_00), run(RUN2, "2026-10-01", "2026-10-31", "PARTIALLY_COMPLETED", 440_000_00)],
  next_cursor: "c2",
};

const RUN_VIEW = {
  connection: "payrolla",
  run: { summary: run(RUN1, "2026-11-01", "2026-11-30", "COMPUTED", 450_000_00), created_at: "2026-11-20T09:00:00Z", updated_at: "2026-11-20T09:00:00Z", approved_at: null, locked_at: null },
  lines: [
    { payslip_id: "p1", employee_id: E_MUSA, name: "Musa Bello", net_kobo: 150_000_00, status: "CONFIRMED", state: "paid", reference: "r1", staff: { user_id: MUSA, name: "Musa Bello" } },
    { payslip_id: "p2", employee_id: E_KEMI, name: "Kemi Ade", net_kobo: 150_000_00, status: "PENDING", state: "waiting", staff: null },
    { payslip_id: "p3", employee_id: E_TUNDE, name: "Tunde Bello", net_kobo: 150_000_00, status: "FAILED", state: "failed", reason: "The account number is closed", staff: null },
  ],
  counts: { paid: 1, waiting: 1, failed: 1, returned: 0 },
  approval_url: "https://app.payrolla.example/runs/approve",
};

/** Payrolla, stood in for. */
async function standIn(page: Page) {
  const puts: { path: string; body: unknown }[] = [];
  const state = { musa: null as null | { employee_id: string; name: string; linked_at: string } };
  await page.route(
    (u) => u.pathname.startsWith("/v1/payroll"),
    (r) => {
      const u = new URL(r.request().url());
      if (u.pathname === "/v1/payroll")
        return u.searchParams.get("cursor") === "c2"
          ? json(r, 200, { ...RUNS, runs: [run(RUN3, "2026-09-01", "2026-09-30", "COMPLETED", 430_000_00)], next_cursor: undefined })
          : json(r, 200, RUNS);
      if (u.pathname === `/v1/payroll/runs/${RUN1}`) return json(r, 200, RUN_VIEW);
      if (u.pathname === "/v1/payroll/staff")
        return json(r, 200, {
          connection: "payrolla",
          staff: [
            { user_id: MUSA, name: "Musa Bello", payrolla: state.musa, suggested: state.musa ? null : { employee_id: E_MUSA, name: "Musa Bello", by: "email" } },
            { user_id: KEMI, name: "Kemi Ade", payrolla: null, suggested: null },
          ],
          employees: [
            { id: E_MUSA, name: "Musa Bello", department: "Kitchen", status: "ACTIVE", linked_to: state.musa ? MUSA : null },
            { id: E_KEMI, name: "Kemi Ade", status: "ACTIVE", linked_to: null },
            { id: E_TUNDE, name: "Tunde Bello", status: "TERMINATED", linked_to: null },
          ],
        });
      return json(r, 404, { error: "not found", code: "not_found" });
    },
  );
  await page.route(
    (u) => /^\/v1\/staff\/[^/]+\/payrolla$/.test(u.pathname),
    (r) => {
      const body = r.request().postDataJSON() as { employee_id?: string };
      puts.push({ path: new URL(r.request().url()).pathname, body });
      state.musa = body.employee_id ? { employee_id: body.employee_id, name: "Musa Bello", linked_at: new Date().toISOString() } : null;
      return json(r, 200, { payrolla: state.musa });
    },
  );
  return { puts, state };
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

test("without Payrolla, the Payroll page says payroll is in Payrolla and how to connect it", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signUp(page);
  await page.goto("/payroll");
  await expect(page.getByRole("heading", { level: 1, name: "Payroll" })).toBeVisible();
  const note = page.getByTestId("payroll-not-connected");
  await expect(note).toContainText("worked out, approved and paid in Payrolla");
  await expect(page.getByTestId("payroll-connect")).toHaveAttribute("href", "/payroll/settings");
  await expect(page.getByTestId("payroll-run")).toHaveCount(0);
  await fits(page, 390);
  await noAxe(page, "Payroll, not connected");
});

for (const width of [390, 360])
  test(`the runs, a run waiting for approval with someone not paid, and confirming who a staff member is, ${width} px`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width, height: 844 });
    await signUp(page);
    const { puts } = await standIn(page);

    // The runs: the company, Test, the latest on pine, each in plain words.
    await page.goto("/payroll");
    await expect(page.getByTestId("payroll-company")).toContainText("Mama's Kitchen Ltd");
    await expect(page.getByTestId("payroll-company")).toContainText("Paid monthly");
    await expect(page.getByTestId("payroll-test")).toHaveText("Test");
    const hero = page.getByTestId("payroll-hero");
    await expect(hero).toContainText("₦450,000");
    await expect(page.getByTestId("payroll-hero-status")).toHaveText("Waiting for approval");
    await expect(hero).toContainText("November 2026 · 3 people");
    const rows = page.getByTestId("payroll-run");
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toContainText("November 2026");
    await expect(rows.nth(0)).toContainText("Waiting for approval");
    await expect(rows.nth(0)).toContainText("pay day 30 Nov 2026");
    await expect(rows.nth(1)).toContainText("Partly paid");
    await fits(page, width);
    await noAxe(page, "Payroll, the runs");
    await page.getByTestId("payroll-more").click();
    await expect(rows).toHaveCount(3);
    await expect(rows.nth(2)).toContainText("September 2026");
    await expect(rows.nth(2)).toContainText("Paid");
    await expect(page.getByTestId("payroll-more")).toHaveCount(0);

    // One run: the approval button opens Payrolla; those not paid first, with why.
    await rows.nth(0).getByRole("link").click();
    await expect(page).toHaveURL(new RegExp(`/payroll/runs/${RUN1}$`));
    await expect(page.getByRole("heading", { level: 1, name: "Payroll for November 2026" })).toBeVisible();
    await expect(page.getByTestId("payroll-run-hero")).toContainText("₦450,000");
    const approve = page.getByTestId("payroll-approve-open");
    await expect(approve).toHaveAttribute("href", RUN_VIEW.approval_url);
    await expect(approve).toHaveAttribute("target", "_blank");
    await expect(page.getByTestId("payroll-approve")).toContainText("Approving happens in Payrolla for now");
    const lines = page.getByTestId("payroll-line");
    await expect(lines).toHaveCount(3);
    await expect(lines.nth(0)).toHaveAttribute("data-state", "failed");
    await expect(lines.nth(0)).toContainText("Tunde Bello");
    await expect(lines.nth(0)).toContainText("Not paid");
    await expect(lines.nth(0).getByTestId("payroll-line-reason")).toHaveText("The account number is closed");
    await expect(lines.nth(2)).toContainText("Paid");
    await expect(lines.nth(2).getByRole("link", { name: "Staff: Musa Bello" })).toHaveAttribute("href", `/staff/${MUSA}`);
    await fits(page, width);
    await noAxe(page, "a payroll run");

    // Staff in Payrolla: confirm the suggestion.
    await page.goto("/payroll/staff");
    const musa = page.getByTestId("payroll-staff").filter({ hasText: "Musa Bello" });
    await expect(musa.getByTestId("payroll-staff-suggested")).toHaveText("Looks like Musa Bello in Payrolla (same email)");
    const kemi = page.getByTestId("payroll-staff").filter({ hasText: "Kemi Ade" });
    await expect(kemi).toContainText("Not linked to anyone in Payrolla");
    await fits(page, width);
    await noAxe(page, "Staff in Payrolla");
    await kemi.getByTestId("payroll-staff-choose").click();
    const sheet = page.getByRole("dialog", { name: "Who Kemi Ade is in Payrolla" });
    await expect(sheet.getByTestId("pick-employee")).toHaveCount(3);
    await expect(sheet).toContainText("Has left");
    await noAxe(page, "choosing who someone is in Payrolla");
    await sheet.getByRole("button", { name: "Cancel" }).click();
    await musa.getByRole("button", { name: "Confirm Musa Bello is Musa Bello" }).click();
    await expect(musa.getByTestId("payroll-staff-linked")).toHaveText("In Payrolla: Musa Bello");
    expect(puts).toEqual([{ path: `/v1/staff/${MUSA}/payrolla`, body: { employee_id: E_MUSA } }]);
    await expect(musa.getByTestId("payroll-staff-unlink")).toBeVisible();
  });
