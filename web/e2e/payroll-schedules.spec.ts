import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Payroll settings and the tax and pension schedules (P4b-5,
// docs/spec/chat-pay-people.md): the setup on Payroll settings, read-only
// with Change in Payrolla; Tax and pension with November's PAYE, pension
// and NHF; the pension schedule with each person's PIN masked, shown with
// a passkey; Download as a spreadsheet; and an owner whose Payrolla
// doesn't allow reports asked to reconnect. Payrolla is stood in for, and
// navigator.credentials answers as a passkey would.

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

const RUN = "0192e2e0-0000-7000-8000-00000000d501";
const PAYE = "0192e2e0-0000-7000-8000-00000000f501";
const PEN = "0192e2e0-0000-7000-8000-00000000f502";
const NHF = "0192e2e0-0000-7000-8000-00000000f503";

const sched = (id: string, kind: string, title: string, to: string, total: number, people: number, when: string) => ({
  id,
  run_id: RUN,
  kind,
  title,
  to,
  period: "2026-11",
  period_words: "November 2026",
  total_kobo: total,
  people,
  state: "to_pay",
  state_words: "To pay",
  when,
});

const SCHEDULES = [
  sched(PAYE, "paye", "Tax (PAYE)", "Lagos State Internal Revenue Service", 70_000_00, 3, "Due 10 Dec"),
  sched(PEN, "pension", "Pension", "Pension fund administrator •••• IBTC", 180_000_00, 3, "Due 7 Dec"),
  sched(NHF, "nhf", "Housing fund (NHF)", "Federal Mortgage Bank of Nigeria", 18_000_00, 3, "Due 10 Dec"),
];

const PEOPLE = [
  { employee_id: "e1", name: "Zainab Bello", amount_kobo: 81_000_00, pfa: "STANBIC_IBTC", pin: "PEN100012345678" },
  { employee_id: "e2", name: "Musa Ali", amount_kobo: 54_000_00, pfa: "STANBIC_IBTC", pin: "PEN100087654321" },
  { employee_id: "e3", name: "Ada Obi", amount_kobo: 45_000_00, pfa: "STANBIC_IBTC", pin: "PEN200011112222" },
];
const mask = (v: string) => "•••• " + v.slice(-4);

function standIn(page: Page) {
  const s = { scope: true, reveals: 0, downloads: [] as unknown[] };
  return page
    .route(
      (u) => u.pathname.startsWith("/v1/payroll") || u.pathname === "/v1/me/step-up/options",
      (r) => {
        const p = new URL(r.request().url()).pathname;
        if (p === "/v1/me/step-up/options") return json(r, 200, { publicKey: { challenge: "AAAA", allowCredentials: [] } });
        if (p === "/v1/payroll/connection")
          return json(r, 200, {
            connected: true,
            is_owner: true,
            may_link: false,
            modes: ["test"],
            connection: { id: "c1", name: "payrolla", status: "active", auth: "oauth", test: true, company: { name: "Amara Foods Ltd" }, scopes: ["payroll:read"] },
          });
        if (p === "/v1/payroll/settings")
          return json(r, 200, {
            is_owner: true,
            settings: {
              company: { name: "Amara Foods Ltd", rc_number: "RC1234567" },
              rows: [
                { key: "pay_frequency", label: "How often people are paid", value: "Every month" },
                { key: "tax_state", label: "State you pay tax to", value: "Lagos State" },
                { key: "separation_of_duties", label: "Someone else approves", value: "On: whoever prepares a payroll can't approve it", on: true },
                { key: "funding", label: "How payroll is paid for", value: "You add money to your Payrolla wallet before pay day" },
                { key: "paye_tin", label: "PAYE tax number", value: "Not on file yet. Payrolla needs it to file your tax: add it in Payrolla", warn: true, on: false },
              ],
              deductions: [
                { key: "paye", label: "Tax (PAYE)", value: "In force, by the tax tables", on: true },
                { key: "pension", label: "Pension", value: "8% from each person, 10% from you", on: true },
                { key: "nhf", label: "Housing fund (NHF)", value: "2.5% of basic pay", on: true },
                { key: "nhis", label: "Health insurance (NHIS)", value: "Not in force", on: false },
              ],
              since: "1 Jan 2026",
              change_url: "https://payroller.example/settings",
              separation_of_duties: true,
            },
          });
        if (!s.scope && p.startsWith("/v1/payroll/schedules"))
          return json(r, 409, {
            code: "scope_required",
            error: "Payrolla hasn't allowed Taskiem to read your tax and pension schedules. Reconnect Payrolla and allow Read statutory reports.",
          });
        if (p === "/v1/payroll/schedules") return json(r, 200, { schedules: SCHEDULES });
        if (p === `/v1/payroll/schedules/${PAYE}`)
          return json(r, 200, { schedule: SCHEDULES[0], lines: PEOPLE.map((l) => ({ employee_id: l.employee_id, name: l.name, amount_kobo: l.amount_kobo / 3 })), masked: false });
        if (p === `/v1/payroll/schedules/${PEN}`)
          return json(r, 200, { schedule: SCHEDULES[1], lines: PEOPLE.map((l) => ({ ...l, pfa: mask(l.pfa), pin: mask(l.pin) })), masked: true });
        if (p === `/v1/payroll/schedules/${PEN}/reveal`) {
          const body = r.request().postDataJSON() as { passkey?: unknown };
          if (!body?.passkey) return json(r, 403, { error: "Confirm with your passkey.", step_up: "passkey" });
          s.reveals++;
          return json(r, 200, { schedule: SCHEDULES[1], lines: PEOPLE, masked: false });
        }
        if (p.endsWith("/download")) {
          s.downloads.push(r.request().postDataJSON());
          return json(r, 200, { filename: "pension-november-2026.csv", csv: "Name,Amount (NGN),PFA,Pension PIN\nZainab Bello,81000.00,STANBIC_IBTC,PEN100012345678\n" });
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
  test(`An owner sees the payroll's setup and November's tax and pension schedules, PINs shown with a passkey, ${width} px`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 844 });
    await page.addInitScript(() => {
      const buf = () => new Uint8Array([1, 2, 3]).buffer;
      Object.defineProperty(navigator, "credentials", {
        configurable: true,
        value: {
          get: async () => ({ id: "AQID", rawId: buf(), type: "public-key", response: { clientDataJSON: buf(), authenticatorData: buf(), signature: buf(), userHandle: null } }),
        },
      });
    });
    await signUp(page);
    const s = await standIn(page);

    // The setup, read-only, with Change in Payrolla.
    await page.goto("/payroll/settings");
    const setup = page.getByTestId("payroll-setup");
    await expect(setup.getByTestId("payroll-setup-pay_frequency")).toContainText("Every month");
    await expect(setup.getByTestId("payroll-setup-separation_of_duties")).toContainText("whoever prepares a payroll can't approve it");
    await expect(setup.getByTestId("payroll-setup-paye_tin")).toContainText("Not on file yet");
    await expect(setup.getByTestId("payroll-setup-pension")).toContainText("8% from each person, 10% from you");
    await expect(setup.getByTestId("payroll-setup-change")).toHaveAttribute("href", "https://payroller.example/settings");
    await expect(setup.locator("input, select, textarea")).toHaveCount(0);
    await fits(page, width);
    await noAxe(page, "Payroll settings with the setup");

    // Tax and pension: November's schedules.
    await setup.getByTestId("payroll-setup-schedules").click();
    await expect(page).toHaveURL(/\/payroll\/schedules$/);
    await expect(page.getByTestId("payroll-schedule-month")).toContainText("November 2026");
    await expect(page.getByTestId("payroll-schedule-paye")).toContainText("₦70,000");
    await expect(page.getByTestId("payroll-schedule-paye")).toContainText("Due 10 Dec · 3 people");
    await expect(page.getByTestId("payroll-schedule-pension")).toContainText("₦180,000");
    await fits(page, width);
    await noAxe(page, "Tax and pension");

    // The pension schedule: PINs masked, then shown with a passkey.
    await page.getByTestId("payroll-schedule-pension").click();
    await expect(page.getByRole("heading", { level: 1, name: "Pension, November 2026" })).toBeVisible();
    await expect(page.getByTestId("payroll-schedule-hero")).toContainText("₦180,000");
    await expect(page.getByTestId("payroll-schedule-line")).toHaveCount(3);
    await expect(page.getByTestId("payroll-schedule-line").first()).toContainText("PIN •••• 5678");
    await expect(page.locator("body")).not.toContainText("PEN100012345678");
    await fits(page, width);
    await noAxe(page, "a pension schedule, masked");
    await page.getByTestId("payroll-schedule-reveal").click();
    await expect(page.getByTestId("payroll-schedule-line").first()).toContainText("PIN PEN100012345678");
    await expect(page.getByTestId("payroll-schedule-reveal")).toHaveCount(0);
    expect(s.reveals).toBe(1);
    await fits(page, width);

    // Download: a spreadsheet, the passkey asked for again.
    const download = page.waitForEvent("download");
    await page.getByTestId("payroll-schedule-download").click();
    expect((await download).suggestedFilename()).toBe("pension-november-2026.csv");
    expect(s.downloads[0]).toMatchObject({ passkey: { id: "AQID" } });

    // Payrolla doesn't allow reports: the owner is asked to reconnect.
    s.scope = false;
    await page.goto("/payroll/schedules");
    await expect(page.getByTestId("payroll-reports-scope")).toContainText("Reconnect Payrolla and allow Read statutory reports.");
    await expect(page.getByTestId("payroll-reports-reconnect")).toHaveAttribute("href", "/payroll/settings");
    await noAxe(page, "Tax and pension, not allowed");
  });
}
