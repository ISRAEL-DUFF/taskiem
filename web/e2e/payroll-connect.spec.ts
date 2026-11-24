import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Connecting Payrolla in one step and linking approvers (P4b-1,
// docs/spec/chat-pay-people.md). An owner new to Payrolla signs up and
// connects in one step, a key connection's owner sees Reconnect Payrolla,
// an ended connection says so, and an approver links their own Payrolla
// account with one code (a wrong code, then the right one; an expired code
// offers Send a new code). Payrolla is stood in for: the e2e server has no
// Payrolla to reach.

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

const NGOZI = "a0000000-0000-4000-8000-000000000001";
const MUSA = "a0000000-0000-4000-8000-000000000002";

type State = "none" | "one_step" | "key" | "ended";

/** Payrolla, stood in for, through Taskiem's own routes. */
async function standIn(page: Page, start: State) {
  const s = { state: start, linked: false, posts: [] as { path: string; body: unknown }[], expireNext: false };
  const connection = () => {
    const base = { is_owner: true, may_link: true, modes: ["test"] };
    if (s.state === "none") return { ...base, connected: false };
    const c = { id: "0192e2e0-0000-7000-8000-0000000c0001", name: "payrolla", test: true, company: { id: "c1", name: "Amara Foods Ltd" } };
    if (s.state === "ended") return { ...base, connected: false, ended: true, connection: { ...c, status: "expired", auth: "oauth" } };
    if (s.state === "key") return { ...base, connected: true, connection: { ...c, status: "active", auth: "key", scopes: ["payroll:read", "employees:read"] } };
    return { ...base, connected: true, connection: { ...c, status: "active", auth: "oauth", scopes: ["payroll:read", "payroll:approve", "webhooks:manage"] } };
  };
  const approvers = () => {
    const ngozi = {
      user_id: NGOZI,
      name: "Ngozi Eze",
      email: "ngozi@amara.test",
      role: "OWNER",
      can_approve: true,
      can_pay: true,
      can_confirm: true,
      linked: s.linked ? { user_id: "me", name: "Ngozi Owner" } : null,
      suggested: !s.linked,
      me: s.linked,
    };
    const musa = { user_id: MUSA, name: "Musa Bello", email: "musa@amara.test", role: "APPROVER", can_approve: true, can_pay: false, can_confirm: false, linked: null, suggested: false, me: false };
    return { approvers: [ngozi, musa], me: s.linked ? ngozi : null, may_link: true, is_owner: true };
  };
  await page.route(
    (u) => u.pathname.startsWith("/v1/payroll"),
    async (r) => {
      const u = new URL(r.request().url());
      const method = r.request().method();
      if (method !== "GET") s.posts.push({ path: u.pathname, body: r.request().postDataJSON() });
      if (u.pathname === "/v1/payroll")
        return json(r, 200, s.state === "none" || s.state === "ended"
          ? { connected: false, ended: s.state === "ended", is_owner: true, runs: [] }
          : { connected: true, connection: "payrolla", test: true, auth: s.state === "key" ? "key" : "oauth", is_owner: true, company: { name: "Amara Foods Ltd", pay_frequency: "MONTHLY" }, runs: [] });
      if (u.pathname === "/v1/payroll/connection") return json(r, 200, connection());
      if (u.pathname === "/v1/payroll/connect") {
        // Payrolla allows Taskiem and sends the browser back.
        s.state = "one_step";
        return json(r, 200, { url: "/payroll/settings?linked=0192e2e0-0000-7000-8000-0000000c0001", reconnect: false, expires_in: 600 });
      }
      if (u.pathname === "/v1/payroll/approvers") return json(r, 200, approvers());
      if (u.pathname === `/v1/payroll/approvers/${NGOZI}/link` && method === "POST")
        return json(r, 202, { challenge: { challenge_id: "ch1", channel: "SMS", destination_hint: "•••• 4567", expires_at: "2026-11-24T10:05:00Z" }, sent_to: "Payrolla sent a code to your phone ending 4567.", safer: true });
      if (u.pathname === `/v1/payroll/approvers/${NGOZI}/link/confirm`) {
        const body = r.request().postDataJSON() as { code: string };
        if (s.expireNext) {
          s.expireNext = false;
          return json(r, 410, { error: "That code has expired.", code: "code_expired" });
        }
        if (body.code !== "482910") return json(r, 422, { error: "That code isn't right. Check it and type it again.", code: "code_invalid" });
        s.linked = true;
        return json(r, 200, { approver: { user_id: NGOZI, name: "Ngozi Eze", role: "OWNER", can_approve: true, can_pay: true } });
      }
      if (u.pathname === `/v1/payroll/approvers/${NGOZI}/link` && method === "DELETE") {
        s.linked = false;
        return json(r, 200, { unlinked: true });
      }
      return json(r, 404, { error: "not found", code: "not_found" });
    },
  );
  return s;
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
  test(`an owner new to Payrolla signs up and connects in one step, then links their Payrolla account with one code, ${width} px`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width, height: 844 });
    await signUp(page);
    const s = await standIn(page, "none");

    await page.goto("/payroll");
    await page.getByTestId("payroll-connect").click();
    await expect(page).toHaveURL(/\/payroll\/settings$/);
    await expect(page.getByRole("heading", { level: 1, name: "Payroll settings" })).toBeVisible();
    await expect(page.getByTestId("payroll-connection")).toHaveAttribute("data-state", "none");
    await fits(page, width);
    await noAxe(page, "Payroll settings, not connected");

    await page.getByTestId("payroll-sign-up").click();
    await page.getByTestId("payroll-rc").fill("RC 1234567");
    await page.getByTestId("payroll-connect-now").click();
    await expect(page.getByTestId("payroll-connected-now")).toContainText("Payrolla is connected.");
    expect(s.posts.find((p) => p.path === "/v1/payroll/connect")?.body).toEqual({ mode: "test", sign_up: true, rc_number: "RC 1234567" });
    await expect(page.getByTestId("payroll-company-name")).toContainText("Amara Foods Ltd");

    // Who can approve: the same email first, with Link my Payrolla account.
    const rows = page.getByTestId("payroll-approver");
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toContainText("Ngozi Eze");
    await expect(rows.nth(0)).toContainText("Owner · Approves and pays · Looks like you (same email)");
    await expect(rows.nth(1)).toContainText("No phone for codes yet");
    await expect(rows.nth(1).getByTestId("payroll-link-open")).toHaveCount(0);
    await fits(page, width);
    await noAxe(page, "Payroll settings, connected, who can approve");

    await rows.nth(0).getByTestId("payroll-link-open").click();
    const sheet = page.getByRole("dialog", { name: "Link to Ngozi Eze in Payrolla" });
    await sheet.getByTestId("payroll-link-send").click();
    await expect(sheet.getByTestId("payroll-link-sent")).toHaveText("Payrolla sent a code to your phone ending 4567. Type it here.");
    await expect(sheet).toContainText("A code from an authenticator app is safer");
    await sheet.getByTestId("payroll-link-code").fill("000 000");
    await sheet.getByTestId("payroll-link-confirm").click();
    await expect(sheet.getByRole("alert")).toHaveText("That code isn't right. Check it and type it again.");
    await fits(page, width);
    await noAxe(page, "the code sheet, a wrong code");
    // An expired code offers a new one.
    s.expireNext = true;
    await sheet.getByTestId("payroll-link-code").fill("482910");
    await sheet.getByTestId("payroll-link-confirm").click();
    await expect(sheet.getByRole("alert")).toHaveText("That code has expired.");
    await expect(sheet.getByTestId("payroll-link-confirm")).toHaveCount(0);
    await sheet.getByTestId("payroll-link-resend").click();
    await sheet.getByTestId("payroll-link-code").fill("482 910");
    await sheet.getByTestId("payroll-link-confirm").click();
    await expect(sheet).toHaveCount(0);
    await expect(rows.nth(0)).toContainText("Linked to you");
    await expect(rows.nth(0).getByTestId("payroll-unlink")).toBeVisible();
    // Each try at a code had its own key; the codes went only to the confirm route.
    const confirms = s.posts.filter((p) => p.path.endsWith("/confirm")).map((p) => p.body as { key: string; code: string });
    expect(new Set(confirms.map((c) => c.key)).size).toBe(confirms.length);
    expect(confirms.at(-1)?.code).toBe("482910");
    await noAxe(page, "Payroll settings, linked");
  });

  test(`a key connection's owner sees Reconnect Payrolla, and an ended connection says so, ${width} px`, async ({ page }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width, height: 844 });
    await signUp(page);
    const s = await standIn(page, "key");
    await page.goto("/payroll");
    await expect(page.getByTestId("payroll-key-reconnect")).toContainText("Connect again in one step so you can run payroll here.");
    await fits(page, width);
    await noAxe(page, "Payroll, a key connection");
    await page.goto("/payroll/settings");
    await expect(page.getByTestId("payroll-connection")).toHaveAttribute("data-state", "key");
    await expect(page.getByTestId("payroll-reconnect")).toHaveText("Reconnect Payrolla");
    await expect(page.getByTestId("payroll-approvers")).toHaveCount(0);
    await noAxe(page, "Payroll settings, a key connection");

    s.state = "ended";
    await page.goto("/payroll");
    await expect(page.getByTestId("payroll-ended")).toContainText("Payrolla is no longer connected.");
    await expect(page.getByTestId("payroll-reconnect-link")).toHaveAttribute("href", "/payroll/settings");
    await page.goto("/payroll/settings");
    await expect(page.getByTestId("payroll-ended")).toContainText("Payrolla is no longer connected.");
    await fits(page, width);
    await noAxe(page, "Payroll settings, ended");
    await page.getByTestId("payroll-reconnect").click();
    await expect(page.getByTestId("payroll-connected-now")).toBeVisible();
    expect(s.posts.find((p) => p.path === "/v1/payroll/connect")?.body).toEqual({});
  });
}
