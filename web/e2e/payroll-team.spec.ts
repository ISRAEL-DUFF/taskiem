import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Staff and pay from Taskiem (P4b-2, docs/spec/chat-pay-people.md): an
// owner adds a staff member to payroll with their account checked, raises
// someone's pay from a date, asks for a new salary account (held for
// another approver); Account changes approves one with a code (a wrong
// code, then the right one) and rejects one with a note; and a staff
// member changes their own account from My pay with the code Payrolla
// texts them. Payrolla is stood in for: the e2e server has no Payrolla.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: `Team ${letters()}`, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@payroll.test`, name: "Ngozi Owner", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

const json = (r: Route, status: number, body: unknown) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

const MUSA = "0192e2e0-0000-7000-8000-00000000b001";
const ZAINAB = "e0000000-0000-4000-8000-000000000001";

const zainab = () => ({
  id: ZAINAB,
  name: "Zainab Bello",
  first_name: "Zainab",
  last_name: "Bello",
  phone: "+2348011112201",
  department: "Operations",
  status: "active",
  bank: { bank: "Guaranty Trust Bank", bank_code: "058", account_name: "ZAINAB A. BELLO", last4: "6789" },
  pay: { gross_kobo: 45000000, basic_kobo: 0, housing_kobo: 0, transport_kobo: 0, pensionable: true, from: "2026-01-01" },
  waiting_change: null as unknown,
  staff: null,
});

const change = (id: string, via: string, asked?: string, mine = false) => ({
  id,
  employee_id: ZAINAB,
  employee: "Zainab Bello",
  bank: "Access Bank",
  account_name: "ZAINAB A. BELLO",
  last4: "2222",
  via,
  asked_by: asked,
  mine,
  requested_at: "2026-11-24T09:00:00Z",
});

/** Payrolla, stood in for, through Taskiem's own routes. */
async function standIn(page: Page) {
  const s = {
    team: [zainab()] as Record<string, unknown>[],
    changes: [change("c1", "in_taskiem", "Ada"), change("c2", "by_employee")],
    posts: [] as { path: string; method: string; body: unknown }[],
    myWaiting: null as unknown,
  };
  await page.route(
    (u) => u.pathname.startsWith("/v1/payroll"),
    async (r) => {
      const u = new URL(r.request().url());
      const method = r.request().method();
      const body = method === "GET" ? undefined : (r.request().postDataJSON() as Record<string, unknown>);
      if (method !== "GET") s.posts.push({ path: u.pathname, method, body });
      const p = u.pathname;
      if (p === "/v1/payroll")
        return json(r, 200, { connected: true, connection: "payrolla", test: true, auth: "oauth", is_owner: true, company: { name: "Amara Foods Ltd", pay_frequency: "MONTHLY" }, runs: [] });
      if (p === "/v1/payroll/banks")
        return json(r, 200, { banks: [{ code: "044", name: "Access Bank" }, { code: "057", name: "Zenith Bank" }, { code: "058", name: "Guaranty Trust Bank" }] });
      if (p === "/v1/payroll/team" && method === "GET")
        return json(r, 200, { team: s.team, staff: [{ user_id: MUSA, name: "Musa Ali", phone: "+2348022223333" }], may: { prepare: true, approve: true, see_pay: true }, connection: "payrolla" });
      if (p === "/v1/payroll/name-check") return json(r, 200, { checked: true, account_name: "MUSA I. ALI", bank: "Zenith Bank", last4: "3333" });
      if (p === "/v1/payroll/team" && method === "POST") {
        const m = { ...zainab(), id: "e2", name: "Musa Ali", first_name: "Musa", last_name: "Ali", department: "", bank: { bank: "Zenith Bank", bank_code: "057", account_name: "MUSA I. ALI", last4: "3333" }, staff: { user_id: MUSA, name: "Musa Ali" } };
        s.team.push(m);
        return json(r, 201, { employee: m });
      }
      if (p === `/v1/payroll/team/${ZAINAB}` && method === "PATCH") return json(r, 200, { employee: s.team[0], words: "Their new pay starts 1 January 2027." });
      if (p === `/v1/payroll/team/${ZAINAB}/bank`) {
        s.team[0] = { ...s.team[0], waiting_change: change("c3", "in_taskiem", "Ngozi", true) };
        return json(r, 200, { employee: s.team[0], words: "Held until another approver approves it. They're paid into ••6789 until then." });
      }
      if (p === "/v1/payroll/bank-changes") return json(r, 200, { changes: s.changes, may: { prepare: true, approve: true, see_pay: true } });
      if (p === "/v1/payroll/bank-changes/c1/code")
        return json(r, 202, { challenge: { challenge_id: "ch1", channel: "SMS", destination_hint: "•••• 4567", expires_at: "2026-11-24T10:05:00Z" }, sent_to: "Payrolla sent a code to your phone ending 4567.", safer: true });
      if (p === "/v1/payroll/bank-changes/c1/approve") {
        if ((body as { code: string }).code !== "615201") return json(r, 422, { error: "That code isn't right. Check it and type it again.", code: "code_invalid" });
        s.changes = s.changes.filter((c) => c.id !== "c1");
        return json(r, 200, { change: change("c1", "in_taskiem", "Ada"), words: "Approved. Payrolla pays them into the new account from now on." });
      }
      if (p === "/v1/payroll/bank-changes/c2/reject") {
        s.changes = s.changes.filter((c) => c.id !== "c2");
        return json(r, 200, { change: change("c2", "by_employee"), words: "Rejected. They're still paid into their current account." });
      }
      if (p === "/v1/payroll/me" && method === "GET")
        return json(r, 200, { linked: true, me: { name: "Zainab Bello", phone: "+2348011117801", status: "active", bank: zainab().bank, waiting_change: s.myWaiting } });
      if (p === "/v1/payroll/me/bank-change")
        return json(r, 202, {
          challenge: { challenge_id: "ch9", channel: "SMS", destination_hint: "•••• 7801", expires_at: "2026-11-24T10:10:00Z" },
          account_name: "ZAINAB A. BELLO",
          last4: "2222",
          bank: "Access Bank",
          sent_to: "Payrolla sent a code to your phone ending 7801.",
        });
      if (p === "/v1/payroll/me/bank-change/confirm") {
        s.myWaiting = change("c4", "by_employee");
        return json(r, 200, { change: s.myWaiting, words: "Held for your employer to approve. You're paid into ••6789 until then." });
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
  test(`Team: add a staff member with their account checked, change pay, ask for a new account, ${width} px`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width, height: 844 });
    await signUp(page);
    const s = await standIn(page);

    await page.goto("/payroll");
    await page.getByTestId("payroll-team-link").click();
    await expect(page.getByRole("heading", { level: 1, name: "Team" })).toBeVisible();
    const rows = page.getByTestId("payroll-member");
    await expect(rows).toHaveCount(1);
    await expect(rows.nth(0)).toContainText("Operations · Guaranty Trust Bank ••6789");
    await expect(rows.nth(0)).toContainText("₦450,000");
    await fits(page, width);
    await noAxe(page, "Team");

    // Add Musa from the staff: filled in; the bank's name shown before saving.
    await page.getByTestId("payroll-add-staff").click();
    const add = page.getByRole("dialog", { name: "Add Musa Ali to payroll" });
    await expect(add.getByTestId("payroll-first")).toHaveValue("Musa");
    await expect(add.getByTestId("payroll-last")).toHaveValue("Ali");
    await add.getByTestId("payroll-gross").fill("300,000");
    await add.getByTestId("payroll-bank").selectOption("057");
    await add.getByTestId("payroll-account").fill("3333333333");
    await add.getByTestId("payroll-check-account").click();
    await expect(add.getByTestId("payroll-account-name")).toHaveText("MUSA I. ALI");
    await fits(page, width);
    await noAxe(page, "Add someone, the account checked");
    await add.getByTestId("payroll-add-save").click();
    await expect(page.getByTestId("payroll-added-account")).toHaveText("MUSA I. ALI");
    const sent = s.posts.find((p) => p.path === "/v1/payroll/team")?.body as Record<string, unknown>;
    expect(sent).toMatchObject({ first_name: "Musa", last_name: "Ali", staff_user: MUSA, phone: "+2348022223333", pay: { gross_kobo: 30000000 }, bank: { bank_code: "057", account_number: "3333333333" } });
    await page.getByRole("button", { name: "Done" }).click();
    await expect(rows).toHaveCount(2);

    // Raise Zainab's pay from 1 January.
    await rows.nth(0).getByTestId("payroll-change").click();
    const ch = page.getByRole("dialog", { name: "Change Zainab Bello" });
    await expect(ch).toContainText("A phone number is changed in Payrolla. Payrolla keeps no job title.");
    await ch.getByTestId("payroll-change-gross").fill("500,000");
    await ch.getByTestId("payroll-change-from").fill("2027-01-01");
    await noAxe(page, "Change someone");
    await ch.getByTestId("payroll-change-save").click();
    await expect(page.getByTestId("payroll-said")).toHaveText("Their new pay starts 1 January 2027.");
    const patched = s.posts.find((p) => p.method === "PATCH")?.body as Record<string, unknown>;
    expect(patched).toMatchObject({ pay: { gross_kobo: 50000000, from: "2027-01-01" } });
    expect(patched).not.toHaveProperty("phone");

    // A new account for Zainab: held for another approver.
    await rows.nth(0).getByTestId("payroll-new-account").click();
    const bank = page.getByRole("dialog", { name: "New salary account for Zainab Bello" });
    await bank.getByTestId("payroll-bank").selectOption("044");
    await bank.getByTestId("payroll-account").fill("2222222222");
    await bank.getByTestId("payroll-bank-save").click();
    await expect(page.getByTestId("payroll-said")).toHaveText("Held until another approver approves it. They're paid into ••6789 until then.");
    await expect(rows.nth(0)).toContainText("Account change waiting");
    await fits(page, width);
    await noAxe(page, "Team, a change waiting");
  });

  test(`Account changes: approve with a code, reject with a note, ${width} px`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width, height: 844 });
    await signUp(page);
    const s = await standIn(page);
    await page.goto("/payroll/account-changes");
    const rows = page.getByTestId("payroll-change-row");
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toContainText("ZAINAB A. BELLO, Access Bank ••2222 · Asked by Ada in Taskiem");
    await expect(rows.nth(1)).toContainText("Asked by the employee");
    await fits(page, width);
    await noAxe(page, "Account changes");

    await rows.nth(0).getByTestId("payroll-change-approve").click();
    const sheet = page.getByRole("dialog");
    await sheet.getByTestId("payroll-approve-send").click();
    await expect(sheet.getByTestId("payroll-approve-sent")).toHaveText("Payrolla sent a code to your phone ending 4567. Type it here.");
    await sheet.getByTestId("payroll-approve-code").fill("000000");
    await sheet.getByTestId("payroll-approve-confirm").click();
    await expect(sheet.getByRole("alert")).toHaveText("That code isn't right. Check it and type it again.");
    await fits(page, width);
    await noAxe(page, "Approve an account change, a wrong code");
    await sheet.getByTestId("payroll-approve-code").fill("615 201");
    await sheet.getByTestId("payroll-approve-confirm").click();
    await expect(page.getByTestId("payroll-said")).toHaveText("Approved. Payrolla pays them into the new account from now on.");
    const tries = s.posts.filter((p) => p.path.endsWith("/approve")).map((p) => (p.body as { key: string }).key);
    expect(new Set(tries).size).toBe(2);

    await expect(rows).toHaveCount(1);
    await rows.nth(0).getByTestId("payroll-change-reject").click();
    const rj = page.getByRole("dialog", { name: "Reject this account change?" });
    await rj.getByTestId("payroll-reject-note").fill("Call Zainab first");
    await rj.getByTestId("payroll-reject-yes").click();
    await expect(page.getByTestId("payroll-said")).toHaveText("Rejected. They're still paid into their current account.");
    await expect(rows).toHaveCount(0);
    await noAxe(page, "Account changes, none waiting");
  });

  test(`My pay: change my own salary account with the code Payrolla texts me, ${width} px`, async ({ page }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width, height: 844 });
    await signUp(page);
    await standIn(page);
    await page.goto("/payroll/me");
    await expect(page.getByTestId("mypay")).toContainText("ZAINAB A. BELLO");
    await expect(page.getByTestId("mypay")).toContainText("+2348011117801");
    await fits(page, width);
    await noAxe(page, "My pay");
    await page.getByTestId("mypay-change").click();
    const sheet = page.getByRole("dialog", { name: "Change my salary account" });
    await sheet.getByTestId("payroll-bank").selectOption("044");
    await sheet.getByTestId("payroll-account").fill("2222222222");
    await sheet.getByTestId("mypay-send").click();
    await expect(sheet.getByTestId("mypay-account-name")).toHaveText("ZAINAB A. BELLO ••2222");
    await expect(sheet.getByTestId("mypay-sent")).toContainText("Payrolla sent a code to your phone ending 7801.");
    await fits(page, width);
    await noAxe(page, "My pay, the code sheet");
    await sheet.getByTestId("mypay-code").fill("731 501");
    await sheet.getByTestId("mypay-confirm").click();
    await expect(page.getByTestId("payroll-said")).toHaveText("Held for your employer to approve. You're paid into ••6789 until then.");
    await expect(page.getByTestId("mypay-waiting")).toContainText("ZAINAB A. BELLO, Access Bank ••2222");
    await noAxe(page, "My pay, a change waiting");
  });
}
