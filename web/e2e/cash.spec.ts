import { execFileSync } from "node:child_process";
import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Cash at the desk, approvals and receipts (docs/spec/schools.md B5;
// docs/schools.md "Cash and transfers", "Receipts"): a posting under the
// cash threshold is counted at once with a receipt and its PDF; one at or
// above it waits for a second person, who approves it (the requester
// cannot) and a receipt follows; another is declined with a reason; a
// payment undone voids its receipt, which stays. The school is a business
// of its own, so no other test's students are in its lists.
test.use({ bypassCSP: true }); // axe is injected into the page

const H = { "X-Taskiem-Request": "1" };
const PASSWORD = "correct horse battery";

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

const STUDENTS = ["Name,Admission number,Class,Guardian name,Phone", "Ada Okafor,CSH/001,Primary 5 C4,Uche Okafor,08035551001", "Obi Okafor,CSH/002,Primary 5 C4,Uche Okafor,08035551001"].join(
  "\n",
);

/** A school of its own with a sent term invoice per student: the owner's email. */
async function setUp(page: Page): Promise<string> {
  const email = `owner-${Date.now()}@cashschool.test`;
  const r = await page.request.post("/v1/signup", { headers: H, data: { tenant: "Okafor Cash Academy", email, name: "Owner", password: PASSWORD } });
  expect(r.status(), await r.text()).toBe(201);
  // Adding members needs a confirmed email; the e2e server sends none.
  sql(`UPDATE users SET email_verified_at = now() WHERE lower(email) = lower('${email}')`);
  const on = await page.request.put("/v1/settings/schools", { headers: H, data: { enabled: true } });
  expect(on.ok(), await on.text()).toBeTruthy();
  const cl = await page.request.post("/v1/schools/classes", { headers: H, data: { name: "Primary 5", arm: "C4", order: 50 } });
  expect(cl.ok(), await cl.text()).toBeTruthy();
  const classID = ((await cl.json()) as { id: string }).id;
  const plan = await page.request.post("/v1/schools/students/import", { headers: H, data: { csv: STUDENTS } });
  expect(plan.ok(), await plan.text()).toBeTruthy();
  const saved = await page.request.post("/v1/schools/students/import", { headers: H, data: { csv: STUDENTS, confirm: ((await plan.json()) as { hash: string }).hash } });
  expect(saved.ok(), await saved.text()).toBeTruthy();
  const sess = await page.request.post("/v1/schools/sessions", { headers: H, data: { label: "2026/2027" } });
  expect(sess.ok(), await sess.text()).toBeTruthy();
  const term = await page.request.post(`/v1/schools/sessions/${((await sess.json()) as { id: string }).id}/terms`, {
    headers: H,
    data: { name: "First term", starts_on: "2026-09-14", ends_on: "2099-12-11", fees_due_on: "2099-09-07" },
  });
  expect(term.ok(), await term.text()).toBeTruthy();
  const termID = ((await term.json()) as { id: string }).id;
  expect((await page.request.post(`/v1/schools/terms/${termID}/start`, { headers: H })).ok()).toBeTruthy();
  const fees = await page.request.put(`/v1/schools/terms/${termID}/fees/${classID}`, { headers: H, data: { items: [{ name: "Tuition", amount_kobo: 10_000_000, kind: "required" }] } });
  expect(fees.ok(), await fees.text()).toBeTruthy();
  const p = await page.request.post(`/v1/schools/terms/${termID}/invoice`, { headers: H, data: {} });
  expect(p.ok(), await p.text()).toBeTruthy();
  const inv = await page.request.post(`/v1/schools/terms/${termID}/invoice`, { headers: H, data: { confirm: ((await p.json()) as { plan: { hash: string } }).plan.hash } });
  expect(inv.ok(), await inv.text()).toBeTruthy();
  // The e2e server sends no messages to customers (as schools.spec.ts):
  // the term's invoices are marked sent in the database.
  sql(`UPDATE customer_invoices SET sent_at = now(), updated_at = now() WHERE sent_at IS NULL
    AND tenant_id = (SELECT m.tenant_id FROM memberships m JOIN users u ON u.id = m.user_id WHERE lower(u.email) = lower('${email}') LIMIT 1)`);
  return email;
}

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

/** A second owner, signed in on a page of their own. */
async function secondOwner(browser: Browser, page: Page): Promise<Page> {
  const email = `second-${Date.now()}@cashschool.test`;
  const added = await page.request.post("/v1/members", { headers: H, data: { email, password: PASSWORD, roles: ["owner"] } });
  expect(added.status(), await added.text()).toBe(201);
  const ctx = await browser.newContext();
  const other = await ctx.newPage();
  const login = await other.request.post("/v1/auth/login", { headers: H, data: { email, password: PASSWORD } });
  expect(login.ok(), await login.text()).toBeTruthy();
  return other;
}

async function postPayment(page: Page, student: string, amount: string, how = "Cash") {
  await page.goto("/schools/payments");
  await expect(page.getByRole("heading", { level: 1, name: "Payments" })).toBeVisible();
  const form = page.getByRole("form", { name: "Post a payment" });
  await form.getByLabel("Student (admission number)").fill(student);
  await form.getByLabel("Amount (₦)").fill(amount);
  await form.getByLabel("How it was paid").selectOption({ label: how });
  await form.getByRole("button", { name: "Post the payment" }).click();
  await expect(page.getByTestId("posted")).toBeVisible();
  return page.getByTestId("posted");
}

test("cash is posted with a receipt, waits for a second person over the threshold, and a receipt is voided when its payment is undone", async ({ page, browser }) => {
  test.setTimeout(240_000);
  await setUp(page);

  // The owner sets the cash threshold.
  await page.goto("/settings/schools");
  const cash = page.locator("section", { has: page.getByRole("heading", { name: "Cash threshold" }) });
  await cash.getByLabel("A second person at or above an amount").check();
  await cash.getByLabel("Cash threshold (₦)").fill("50,000");
  await cash.getByRole("button", { name: "Save the cash threshold" }).click();
  await expect(page.getByText("Cash threshold saved")).toBeVisible();
  await axeBoth(page, "/settings/schools (cash threshold)");

  // Under the threshold: posted at once, towards Ada's invoice, with a receipt.
  const posted = await postPayment(page, "CSH/001", "20,000");
  await expect(posted).toContainText("₦20,000 posted: ₦20,000 towards INV-");
  await expect(posted).toContainText("(receipt RCT-2026T1-0001)");
  await expect(page.getByTestId("posting").first()).toHaveAttribute("data-status", "posted");
  await axeBoth(page, "/schools/payments (a posting)");

  // The receipt: listed, found by number, its PDF.
  await page.getByRole("navigation", { name: "Schools" }).getByRole("link", { name: "Receipts" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Receipts" })).toBeVisible();
  const first = page.getByTestId("receipt").first();
  await expect(first).toContainText("RCT-2026T1-0001");
  await expect(first).toContainText("CSH/001");
  await expect(first).toContainText("Valid");
  const pdfLink = first.getByRole("link", { name: "Open PDF of receipt RCT-2026T1-0001" });
  const pdf = await page.request.get((await pdfLink.getAttribute("href")) ?? "");
  expect(pdf.status()).toBe(200);
  expect(pdf.headers()["content-type"]).toBe("application/pdf");
  expect((await pdf.body()).subarray(0, 8).toString()).toBe("%PDF-1.4");
  await page.getByRole("form", { name: "Find a receipt" }).getByLabel("Receipt number").fill("rct-2026t1-0001");
  await page.getByRole("form", { name: "Find a receipt" }).getByRole("button", { name: "Find" }).click();
  await expect(page.getByRole("heading", { name: "Receipt RCT-2026T1-0001" })).toBeVisible();
  await expect(page.getByTestId("receipt")).toHaveCount(1);
  await axeBoth(page, "/schools/receipts");

  // At the threshold: pending; the owner who asked cannot decide it.
  const waiting = await postPayment(page, "CSH/002", "50,000", "Transfer to the school's account");
  await expect(waiting).toContainText("Cash posting of ₦50,000 for CSH/002 (Primary 5 C4) is recorded and waits for a second person");
  await expect(page.getByTestId("posting").filter({ hasText: "CSH/002" })).toHaveAttribute("data-status", "pending");
  await page.goto("/approvals");
  const mine = page.getByTestId("school-request");
  await expect(mine).toHaveCount(1);
  await expect(mine).toContainText("You asked for this, so a second person decides it.");
  await expect(mine.getByRole("button", { name: "Approve" })).toHaveCount(0);
  await axeBoth(page, "/approvals (a school request of one's own)");

  // A second owner approves it: posted, with its receipt.
  const other = await secondOwner(browser, page);
  await other.goto("/approvals");
  const req = other.getByTestId("school-request");
  await expect(req).toHaveCount(1);
  await expect(req).toContainText("Cash posting of ₦50,000 for CSH/002 (Primary 5 C4)");
  await expect(req).toContainText("Paid transfer to the school's account");
  await axeBoth(other, "/approvals (a school request)");
  await req.getByRole("button", { name: "Approve" }).click();
  await expect(other.getByText("Approved and applied")).toBeVisible();
  await expect(other.getByTestId("school-request")).toHaveCount(0);
  await page.goto("/schools/receipts");
  await expect(page.getByTestId("receipt")).toHaveCount(2);
  await expect(page.getByTestId("receipt").first()).toContainText("RCT-2026T1-0002");

  // Another, declined with a reason: nothing posted.
  await postPayment(page, "CSH/002", "60,000");
  await other.goto("/approvals");
  await other.getByTestId("school-request").getByRole("button", { name: "Decline" }).click();
  await other.getByLabel("Why (required, audited)").fill("No transfer on the statement");
  await other.getByRole("button", { name: "Decline it" }).click();
  await expect(other.getByText("Declined", { exact: true })).toBeVisible();
  await page.goto("/schools/payments?status=declined");
  const declined = page.getByTestId("posting");
  await expect(declined).toHaveCount(1);
  await expect(declined).toContainText("No transfer on the statement");
  await page.goto("/schools/receipts");
  await expect(page.getByTestId("receipt")).toHaveCount(2);

  // Undoing Ada's payment on her invoice voids its receipt, which stays.
  const list = await page.request.get("/v1/schools/receipts?number=RCT-2026T1-0001");
  const receipt = ((await list.json()) as { receipts: { invoice_id: string; part_id: string }[] }).receipts[0];
  if (!receipt) throw new Error("the first receipt is gone");
  await page.goto(`/invoices/${receipt.invoice_id}`);
  await expect(page.getByTestId("payment-receipt")).toContainText("Receipt RCT-2026T1-0001");
  await page.getByRole("button", { name: /^Undo the payment/ }).click();
  await page.getByRole("button", { name: "Undo payment" }).click();
  await expect(page.getByText("Payment undone")).toBeVisible();
  await expect(page.getByTestId("payment-receipt")).toContainText("(voided)");
  await page.goto("/schools/receipts?number=RCT-2026T1-0001");
  const voided = page.getByTestId("receipt");
  await expect(voided).toHaveAttribute("data-voided", "true");
  await expect(voided).toContainText("Voided");
  await expect(voided).toContainText("the payment was undone");
  await other.context().close();
});
