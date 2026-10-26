import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";
import { H, PUBLIC, familyLink } from "./familyschool";

// The pilot walkthrough (docs/spec/schools-open-items.md, "Pilot
// checklist"): the demo school serve.sh seeds with `taskiem demo school
// --create-tenant` (a command only the e2e build has), walked
// on a phone as the bursar: the Overview, a student's page with their
// account number and receipts, To match with the held credit and the
// families' claims, Approvals with the cash posting (as the approver, who
// decides it), Debtors with the promise and the plans, Clearance with the
// override, the gate as the gate user, the family page from a fresh link,
// and yesterday's close. Every page passes the accessibility checks in
// both themes.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } }); // axe is injected into the page; a phone

type Person = { email: string; password: string };
type People = { proprietor: Person; bursar: Person; approver: Person; gate: Person };

/** The demo's people, as the CLI printed them when serve.sh seeded the school. */
function people(): People {
  const text = readFileSync(fileURLToPath(new URL("../../bin/e2e-demo.txt", import.meta.url)), "utf8");
  const one = (role: string): Person => {
    const m = text.match(new RegExp(`^${role}\\s+(\\S+)\\s+(\\S+)$`, "m"));
    if (!m) throw new Error(`bin/e2e-demo.txt has no ${role}:\n${text}`);
    return { email: m[1] ?? "", password: m[2] ?? "" };
  };
  return { proprietor: one("proprietor"), bursar: one("bursar"), approver: one("approver"), gate: one("gate") };
}

async function signIn(page: Page, p: Person) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(p.email);
  await page.getByLabel("Password").fill(p.password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page).not.toHaveURL(/\/login/);
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

/** Nothing scrolls sideways on the phone. */
async function fits(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
}

async function as(browser: Browser, p: Person): Promise<Page> {
  const page = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await signIn(page, p);
  return page;
}

test("the pilot path through the demo school on a phone", async ({ page, browser }) => {
  test.setTimeout(300_000);
  const who = people();
  await signIn(page, who.bursar);

  // The Overview: the term, every student invoiced, most money matched by
  // itself, how parents paid, yesterday's close.
  await page.goto("/schools/overview");
  await expect(page.getByRole("heading", { level: 1, name: "Overview" })).toBeVisible();
  await expect(page.getByTestId("overview-term")).toContainText("First term");
  await expect(page.getByTestId("overview-collected")).toContainText("40 of 40 students invoiced");
  await expect(page.getByTestId("overview-matched")).not.toContainText("0%");
  await expect(page.getByTestId("overview-how-paid")).toContainText("Account number");
  await expect(page.getByTestId("overview-how-paid")).toContainText("Cash");
  await expect(page.getByTestId("overview-last-close")).toContainText("reconciles");
  await expect(page.getByTestId("top-debtor")).toHaveCount(5);
  await fits(page);
  await axeBoth(page, "/schools/overview (demo)");

  // A student's page: their account number and their receipt.
  const listed = await page.request.get("/v1/schools/students?status=active&limit=100", { headers: H });
  expect(listed.ok(), await listed.text()).toBeTruthy();
  const students = ((await listed.json()) as { students: { id: string; admission_number: string }[] }).students;
  expect(students.length).toBe(40);
  const first = students.find((s) => s.admission_number === "DEMO/001");
  await page.goto(`/schools/students/${first?.id}`);
  await expect(page.getByRole("heading", { level: 2, name: "Account number" })).toBeVisible();
  const account = page.getByTestId("student-account-number");
  await expect(account).toBeVisible();
  await expect(account).toContainText("Demo Bank");
  await expect(account.getByRole("button", { name: /^Copy the account number/ })).toBeVisible();
  await expect(page.getByRole("heading", { level: 2, name: "Receipts" })).toBeVisible();
  await expect(page.getByTestId("receipt")).toHaveCount(1);
  await expect(page.getByTestId("receipt")).toContainText("Valid");
  await fits(page);
  await axeBoth(page, "/schools/students/:id (demo)");

  // To match: the credit held with its reason, and the two families who
  // say they paid.
  await page.goto("/invoices/match");
  await expect(page.getByRole("heading", { level: 1, name: "To match" })).toBeVisible();
  const held = page.getByTestId("waiting-payment").filter({ has: page.getByTestId("held-reason") });
  await expect(held).toHaveCount(1);
  await expect(held.getByTestId("held-reason")).toContainText("paid into another account number");
  await expect(page.getByRole("heading", { level: 2, name: "Families say they paid" })).toBeVisible();
  await expect(page.getByTestId("claim")).toHaveCount(2);
  await fits(page);
  await axeBoth(page, "/invoices/match (demo)");

  // Debtors: the promise, the plan in good standing and the one in arrears.
  await page.goto("/schools/debtors");
  await expect(page.getByRole("heading", { level: 1, name: "Debtors" })).toBeVisible();
  const rows = page.getByTestId("debtor");
  await expect(rows.filter({ hasText: "Promised by" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "Plan in arrears" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "Plan in good standing" })).toHaveCount(1);
  await fits(page);
  await axeBoth(page, "/schools/debtors (demo)");

  // Clearance: the totals, and the override in force.
  await page.goto("/schools/clearance");
  await expect(page.getByRole("heading", { level: 1, name: "Clearance" })).toBeVisible();
  await expect(page.getByTestId("clearance-totals")).toContainText("40 students: 25 cleared, 7 partly paid, 8 not paid.");
  await expect(page.getByTestId("override")).toHaveCount(1);
  await expect(page.getByTestId("override")).toContainText("In force");
  await fits(page);
  await axeBoth(page, "/schools/clearance (demo)");

  // Approvals, as the approver: the bursar's cash posting and discount
  // wait; the cash posting is approved and its receipt follows.
  const approver = await as(browser, who.approver);
  await approver.goto("/approvals");
  const requests = approver.getByTestId("school-request");
  await expect(requests).toHaveCount(2);
  const cash = requests.filter({ hasText: "Cash posting of ₦120,000" });
  await expect(cash).toHaveCount(1);
  await expect(requests.filter({ hasText: "discount" })).toHaveCount(1);
  await fits(approver);
  await axeBoth(approver, "/approvals (demo)");
  await cash.getByRole("button", { name: "Approve" }).click();
  await expect(approver.getByText("Approved and applied")).toBeVisible();
  await expect(approver.getByTestId("school-request")).toHaveCount(1);
  const posted = students.find((s) => s.admission_number === "DEMO/032");
  const receipts = await page.request.get(`/v1/schools/receipts?student=${posted?.id}`, { headers: H });
  expect(((await receipts.json()) as { receipts: unknown[] }).receipts.length).toBe(1);
  await approver.context().close();

  // The gate, as the gate user: one screen, a cleared child, no amount.
  const gate = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await gate.goto("/login");
  await gate.getByLabel("Email").fill(who.gate.email);
  await gate.getByLabel("Password").fill(who.gate.password);
  await gate.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(gate.getByTestId("gate-screen")).toBeVisible();
  await expect(gate.locator("nav#main-nav")).toHaveCount(0);
  await gate.getByLabel("Child's name").fill("Tomi");
  const hits = gate.getByTestId("gate-hits");
  await expect(hits).toBeVisible();
  await hits.getByRole("button", { name: /Tomiwa Adeyemi/ }).click();
  const answer = gate.getByTestId("gate-answer");
  await expect(answer).toContainText("CLEARED");
  await expect(gate.getByTestId("gate-screen")).not.toContainText("₦");
  expect((await gate.request.get("/v1/schools/students", { headers: H })).status()).toBe(403);
  await fits(gate);
  await axeBoth(gate, "the gate (demo)");
  await gate.context().close();

  // The family page, from a fresh link the bursar sends.
  const link = (await familyLink(page)).replace("https://127.0.0.1", PUBLIC);
  const parent = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await parent.goto(link);
  await expect(parent.getByRole("heading", { level: 1, name: "School fees" })).toBeVisible();
  await expect(parent.getByRole("navigation", { name: "Children" })).toBeVisible();
  await expect(parent.getByText("Demo Bank").first()).toBeVisible();
  await fits(parent);
  await axeBoth(parent, "the family page (demo)");
  await parent.context().close();

  // The close: yesterday's was taken when the school was seeded; it
  // reconciles, with what waits listed; taking it again changes nothing.
  await page.goto("/schools/overview");
  await expect(page.getByTestId("close-row")).toHaveCount(1);
  await page.getByTestId("close-row").getByRole("button", { name: "Open" }).click();
  const report = page.getByTestId("close-report");
  await expect(report).toBeVisible();
  await expect(report.getByTestId("close-status")).toHaveText("Reconciles");
  await expect(report).toContainText("waits for a second person");
  await expect(report).toContainText("says it paid");
  await fits(page);
  await axeBoth(page, "/schools/overview (the demo's close)");
  const again = await page.request.post("/v1/schools/close", { headers: H, data: {} });
  expect(again.status(), await again.text()).toBe(200);
  expect(((await again.json()) as { taken_now: boolean }).taken_now).toBe(false);
});
