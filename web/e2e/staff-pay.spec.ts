import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Where we pay a staff member (outside payroll; salaries are Payrolla's):
// an owner links one of the business's saved bank accounts to them with a
// passkey for that person (operation staff.account, target their id), Pay
// on their page goes there for wages, they are unlinked again; and in Pay
// several people, Add staff adds those with an account linked and shows
// those without as not payable, with why. The staff, saved, step-up and
// payment APIs are stood in for: the e2e server has no payments account
// to pay from, and the browser's passkey is a stand-in too.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Staff Pay ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@staffpay.test`,
      name: "Ada Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

const json = (r: Route, status: number, body: unknown) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

const ADA = "0192e2e0-0000-7000-8000-00000000a100";
const MUSA = "0192e2e0-0000-7000-8000-00000000a101";
const S1 = "0192e2e0-0000-7000-8000-00000000b101";
const B1 = "0192e2e0-0000-7000-8000-00000000c101";

const ACCOUNT = {
  saved_id: S1,
  name: "musa",
  title: "Musa Bello",
  bank_name: "Access Bank",
  last4: "6789",
  account_name: "MUSA BELLO",
  linked_by: "user:ada",
  linked_at: new Date().toISOString(),
};

const person = (id: string, name: string, roles: string[], acct: typeof ACCOUNT | null, you = false) => ({
  user_id: id,
  name,
  phone: "•••4567",
  roles,
  chat: true,
  you,
  profile: { title: "", note: "", rota: [] },
  pay_account: acct,
});

const FIGURES = {
  period: "week",
  from: "2026-11-16",
  to: "2026-11-22",
  minutes: 0,
  shifts: 0,
  late_days: [],
  missed_days: [],
  sales: { count: 0, total_kobo: 0, by_how: {} },
  handovers: [],
  orders: 0,
  tasks: { done: 0, done_late: 0, cant: 0, open: 0, late: 0 },
};

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
  const asked: unknown[] = [];
  await page.route("**/v1/me/step-up/options", (r) => {
    asked.push(r.request().postDataJSON());
    return json(r, 200, { publicKey: { challenge: "Y2hhbGxlbmdl", rpId: "localhost", allowCredentials: [], userVerification: "required" } });
  });
  return asked;
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

/** The staff APIs stood in for, Musa linked to `state.linked` (or not). */
async function standIn(page: Page, state: { linked: typeof ACCOUNT | null }) {
  const puts: Record<string, unknown>[] = [];
  await page.route(
    (u) => u.pathname.startsWith("/v1/staff"),
    async (r) => {
      const req = r.request();
      const path = new URL(req.url()).pathname;
      if (path === "/v1/staff") return json(r, 200, { people: [person(ADA, "Ada Owner", ["owner"], null, true), person(MUSA, "Musa Bello", ["cashier"], state.linked)], invites: [] });
      if (path.endsWith("/figures")) return json(r, 200, FIGURES);
      if (path === `/v1/staff/${ADA}`) return json(r, 200, person(ADA, "Ada Owner", ["owner"], null, true));
      if (path === `/v1/staff/${MUSA}`) return json(r, 200, person(MUSA, "Musa Bello", ["cashier"], state.linked));
      if (path === `/v1/staff/${MUSA}/pay-account` && req.method() === "PUT") {
        const body = req.postDataJSON() as Record<string, unknown>;
        puts.push(body);
        if (!body.passkey) return json(r, 403, { error: "confirm with your passkey or authenticator code", step_up: "required", methods: ["passkey"], operation: "staff.account", target: MUSA });
        state.linked = body.saved_id === S1 ? { ...ACCOUNT, linked_at: new Date().toISOString() } : null;
        return json(r, 200, { pay_account: state.linked });
      }
      return json(r, 404, { error: "not found" });
    },
  );
  await page.route(
    (u) => u.pathname === "/v1/saved" && u.searchParams.get("kind") === "bank_account",
    (r) =>
      json(r, 200, {
        limit: 100,
        saved: [
          { id: S1, owner: "business", name: "musa", title: "Musa Bello", kind: "bank_account", hint: "Access Bank ••6789", details: {}, check: { state: "checked", account_name: "MUSA BELLO", via: null, at: null, problem: null }, to_change: false, created_at: "", updated_at: "" },
          { id: "0192e2e0-0000-7000-8000-00000000b102", owner: "business", name: "bayo", title: "Bayo Rice Mills", kind: "bank_account", hint: "GTBank ••1111", details: {}, check: { state: "unchecked", account_name: null, via: null, at: null, problem: null }, to_change: false, created_at: "", updated_at: "" },
        ],
      }),
  );
  return puts;
}

for (const width of [390, 360])
  test(`an owner links where a staff member is paid, pays them, and pays staff in a list, ${width} px`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width, height: 844 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await signUp(page);
    const asked = await fakePasskey(page);
    const state: { linked: typeof ACCOUNT | null } = { linked: null };
    const puts = await standIn(page, state);
    let paid: unknown;
    await page.route(
      (u) => u.pathname.startsWith("/v1/payments"),
      (r) => {
        const path = new URL(r.request().url()).pathname;
        if (path === "/v1/payments" && r.request().method() === "POST") {
          paid = r.request().postDataJSON();
          return json(r, 201, {
            payment: {
              id: "0192e2e0-0000-7000-8000-00000000d101", saved_id: S1, personal: false, payee: "Musa Bello", bank_name: "Access Bank", account: "••6789",
              account_name: "MUSA BELLO", amount_kobo: 2_000_000, purpose: "wages", purpose_note: "", note: "", first_payment: false, changed_account: false, needs_ok: "",
              status: "prepared", reason: null, fee_kobo: null, started_from: "tools", prepared_by: "user:ada", confirmed_by: null, staff_id: MUSA,
              created_at: new Date().toISOString(), confirmed_at: null, finished_at: null, again: false,
            },
          });
        }
        if (path.endsWith("/cancel")) return json(r, 200, {});
        return json(r, 200, { payments: [] });
      },
    );

    // Nothing linked yet: no Pay, and the owner links one.
    await page.goto(`/staff/${MUSA}`);
    await expect(page.getByRole("heading", { name: "Musa Bello", level: 1 })).toBeVisible();
    const card = page.getByTestId("person-pay");
    await expect(card.getByRole("heading", { name: "Where we pay them" })).toBeVisible();
    await expect(card.getByTestId("person-pay-none")).toHaveText("No account linked");
    await expect(card).toContainText("Salaries are paid through Payrolla.");
    await expect(page.getByTestId("staff-pay")).toHaveCount(0);
    await noAxe(page, "a staff member with no account linked");
    await fits(page, width);

    await card.getByTestId("person-pay-link").click();
    const choose = page.getByRole("dialog", { name: "Where we pay Musa Bello" });
    await choose.getByRole("radio", { name: /MUSA BELLO/ }).check();
    await noAxe(page, "choosing the account");
    await fits(page, width);
    await choose.getByTestId("person-pay-save").click();
    const confirm = page.getByRole("dialog", { name: "Confirm: where Musa Bello is paid" });
    await confirm.getByRole("button", { name: "Confirm with your passkey" }).click();
    await expect(choose).toHaveCount(0);
    await expect(card.getByTestId("person-pay-account")).toHaveText("@musa · Access Bank ••6789 · MUSA BELLO");
    expect(puts.map((b) => b.saved_id)).toEqual([S1, S1]);
    expect(asked).toEqual([{ operation: "staff.account", target: MUSA }]);
    await noAxe(page, "a staff member with an account linked");
    await fits(page, width);

    // Pay: the Pay sheet, to the linked account, for wages.
    await page.getByTestId("staff-pay").click();
    const sheet = page.getByTestId("pay-sheet");
    await expect(sheet.getByTestId("pay-staff-lead")).toContainText("Access Bank ••6789 · MUSA BELLO");
    await expect(sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "wages" })).toHaveAttribute("aria-pressed", "true");
    await sheet.getByLabel("How much? in naira").fill("20k");
    await noAxe(page, "the Pay sheet for a staff member");
    await fits(page, width);
    await sheet.getByTestId("pay-prepare").click();
    await expect(sheet.getByTestId("pay-name")).toHaveText("MUSA BELLO");
    expect(paid).toEqual({ from: "tools", staff_id: MUSA, amount_kobo: 2_000_000, purpose: "wages" });

    // Unlinked: Pay goes away.
    await page.reload();
    page.once("dialog", (d) => void d.accept());
    await card.getByRole("button", { name: "Unlink the account from Musa Bello" }).click();
    await page.getByRole("dialog", { name: "Confirm: where Musa Bello is paid" }).getByRole("button", { name: "Confirm with your passkey" }).click();
    await expect(card.getByTestId("person-pay-none")).toHaveText("No account linked");
    await expect(page.getByTestId("staff-pay")).toHaveCount(0);
    expect(puts.at(-1)).toMatchObject({ saved_id: null });

    // Pay several people: Add staff, Musa linked again; Ada has no account.
    state.linked = ACCOUNT;
    let made: Record<string, unknown> | undefined;
    await page.route(
      (u) => u.pathname.startsWith("/v1/payment-batches"),
      (r) => {
        const req = r.request();
        if (req.method() === "POST") {
          made = req.postDataJSON() as Record<string, unknown>;
          return json(r, 201, {
            batch: { id: B1, title: "", kind: "list", status: "preparing", created_by: "user:ada", created_at: new Date().toISOString(), confirmed_at: null, payments: [], total_kobo: 0, count: 0, paid_kobo: 0, waiting: 0, failed: 0 },
          });
        }
        return json(r, 200, { batches: [] });
      },
    );
    await page.goto("/payments/several");
    await page.getByTestId("add-staff").click();
    const picker = page.getByRole("dialog", { name: "Add staff" });
    const ada = picker.getByRole("checkbox", { name: /Ada Owner/ });
    await expect(ada).toBeDisabled();
    await expect(picker).toContainText("No account linked: link one on their page");
    await noAxe(page, "the staff to pick");
    await fits(page, width);
    await picker.getByRole("checkbox", { name: /Musa Bello/ }).check();
    await picker.getByTestId("pick-add").click();
    const row = page.getByTestId("list-row");
    await expect(row).toHaveCount(1);
    await expect(row).toContainText("@musa · Access Bank ••6789 · MUSA BELLO");
    await expect(row.getByTestId("list-for")).toHaveValue("wages");
    await row.getByTestId("list-amount").fill("20k");
    await noAxe(page, "a list with a staff member");
    await fits(page, width);
    await page.getByTestId("list-check").click();
    await expect.poll(() => made).toEqual({ from: "tools", title: "", kind: "list", rows: [{ staff_id: MUSA, amount_kobo: 2_000_000, purpose: "wages" }] });
  });
