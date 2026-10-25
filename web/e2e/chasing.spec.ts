import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";
import { H, PUBLIC, familySchool, familyLink, sql } from "./familyschool";

// Chasing (docs/spec/schools.md 11, B7; docs/schools.md "Debtors"): a
// school whose fees were due last month; Debtors ages the two siblings;
// a promise is recorded for one; rung 1 goes to both, and the promised
// one is skipped and said so; after the check (run from the page, as it
// runs every hour by itself) a promise whose date passed with the balance
// unmoved is flagged missed; a plan the parent asked for and the school
// approved, with an instalment now past, shows as in arrears. The owner's
// wording for the ladder is set in Settings with a preview. The e2e
// server sends no SMS, so each send reaches nobody and says so.
test.use({ bypassCSP: true }); // axe is injected into the page

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

test("debtors are aged, reminded by the ladder, promises kept or missed, and a plan in arrears shown", async ({ page, browser }) => {
  test.setTimeout(300_000);
  await familySchool(page, "Chase Family Academy");
  // The fees were due last month: the term's date and the invoices' follow,
  // in Lagos days, as the server ages them (the test database runs in UTC).
  sql(`UPDATE school_terms SET fees_due_on = (now() AT TIME ZONE 'Africa/Lagos')::date - 20 WHERE id IN (SELECT t.id FROM school_terms t JOIN tenants x ON x.id = t.tenant_id WHERE x.name = 'Chase Family Academy')`);
  sql(`UPDATE customer_invoices SET due_on = (now() AT TIME ZONE 'Africa/Lagos')::date - 20, updated_at = now() WHERE tenant_id = (SELECT id FROM tenants WHERE name = 'Chase Family Academy')`);

  // The owner's wording: a placeholder that does not exist is refused, a
  // good one previewed and saved.
  await page.goto("/settings/schools");
  const wording = page.locator("section", { has: page.getByRole("heading", { name: "Reminder wording" }) });
  await expect(wording).toBeVisible();
  const rung1 = wording.getByLabel("Rung 1: gentle");
  await rung1.fill("Pay {balance} for {child} by {tomorrow}.");
  await expect(wording.getByRole("alert")).toContainText("{tomorrow} is not a placeholder");
  await rung1.fill("Dear parent, {balance} is due for {child}. Pay to {account_number}.");
  await expect(wording.getByTestId("preview-1")).toContainText("Dear parent, ₦40,000 is due for Ebube. Pay to 0123456789 (Wema Bank). Your fees page: https://pay.example/s/abc");
  await wording.getByRole("button", { name: "Save the wording" }).click();
  await expect(page.getByText("Wording saved")).toBeVisible();
  await axeBoth(page, "/settings/schools (wording)");

  // Debtors: both siblings, 14 to 30 days past due, the totals at the top.
  await page.goto("/schools/debtors");
  await expect(page.getByRole("heading", { level: 1, name: "Debtors" })).toBeVisible();
  await expect(page.getByTestId("total-14_30")).toContainText("2 students");
  const rows = page.getByTestId("debtor");
  await expect(rows).toHaveCount(2);
  await expect(rows.filter({ hasText: "FAM/001" })).toContainText("20 days past due");
  await expect(rows.filter({ hasText: "FAM/001" })).toContainText("₦92,999.50");
  await axeBoth(page, "/schools/debtors");

  // A promise for the second child.
  const second = rows.filter({ hasText: "FAM/002" });
  await second.getByRole("button", { name: "Record a promise" }).click();
  const soon = new Date(Date.now() + 3 * 86_400_000).toISOString().slice(0, 10);
  await second.getByLabel("Promised to pay by").fill(soon);
  await second.getByLabel("Note (optional)").fill("Salary comes on the 25th");
  await second.getByRole("button", { name: "Save the promise" }).click();
  await expect(page.getByText("Promise recorded")).toBeVisible();
  await expect(second).toContainText("Promised by");

  // Rung 1 to both: one sent (to nobody: the e2e server sends no SMS), one
  // skipped for the promise, each said so. The counter shows the cap.
  await page.getByLabel("Select FAM/001").check();
  await page.getByLabel("Select FAM/002").check();
  await expect(page.getByTestId("selection-counter")).toContainText("2 of 50 selected");
  await page.getByLabel("Rung").selectOption("1");
  await page.getByRole("button", { name: "Send reminder 1 to 2 families" }).click();
  const results = page.getByTestId("reminder-results");
  await expect(results).toBeVisible();
  await expect(results).toContainText("1 sent, 1 skipped.");
  await expect(results.getByTestId("reminder-result").filter({ hasText: "FAM/001" })).toContainText("sent (rung 1)");
  await expect(results.getByTestId("reminder-result").filter({ hasText: "FAM/002" })).toContainText("skipped: a promise to pay by");
  await expect(rows.filter({ hasText: "FAM/001" })).toContainText("Rung 1");
  await axeBoth(page, "/schools/debtors (sent)");

  // The day after the promise's date, the check finds the balance unmoved:
  // promise missed, flagged on Debtors and on the student's page.
  sql(`UPDATE school_promises SET promised_on = current_date - 1 WHERE tenant_id = (SELECT id FROM tenants WHERE name = 'Chase Family Academy')`);
  await page.getByRole("button", { name: "Check promises and plans now" }).click();
  await expect(page.getByText("Checked: 1 promise missed, 0 kept, 0 instalments newly in arrears.")).toBeVisible();
  await expect(second).toContainText("Promise missed");
  await page.getByLabel("Show").selectOption("missed");
  await expect(page.getByTestId("debtor")).toHaveCount(1);
  await page.getByLabel("Show").selectOption("");

  // The parent asks for a plan on the family page; the school approves it.
  const link = (await familyLink(page)).replace("https://127.0.0.1", PUBLIC);
  const parent = await (await browser.newContext()).newPage();
  await parent.goto(link);
  const plan = parent.locator("section", { has: parent.getByRole("heading", { name: "Ask for a payment plan" }) });
  const inst = new Date(Date.now() + 2 * 86_400_000).toISOString().slice(0, 10);
  await plan.getByLabel("Amount 1 (₦)").fill("50,000");
  await plan.getByLabel("By date 1").fill(inst);
  await plan.getByLabel("A short message").fill("Half now, half later");
  await plan.getByRole("button", { name: "Ask for this plan" }).click();
  await expect
    .poll(async () => ((await (await page.request.get("/v1/schools/plans", { headers: H })).json()) as { plans: { id: string }[] }).plans.length, { timeout: 20_000 })
    .toBe(1);
  const req = ((await (await page.request.get("/v1/schools/plans", { headers: H })).json()) as { plans: { id: string }[] }).plans[0];
  const decided = await page.request.post(`/v1/schools/plans/${req?.id}`, { headers: H, data: { decision: "approved" } });
  expect(decided.ok(), await decided.text()).toBeTruthy();
  await page.reload();
  await expect(rows.filter({ hasText: "FAM/001" })).toContainText("Plan in good standing");

  // The instalment's date passes unpaid: the check marks it, and the plan
  // is in arrears on Debtors and on the student's page.
  sql(`UPDATE school_plan_requests SET decided_instalments = jsonb_build_array(jsonb_build_object('amount_kobo', 5000000, 'due_on', ((now() AT TIME ZONE 'Africa/Lagos')::date - 3)::text))
    WHERE tenant_id = (SELECT id FROM tenants WHERE name = 'Chase Family Academy')`);
  await page.getByRole("button", { name: "Check promises and plans now" }).click();
  await expect(page.getByText("Checked: 0 promises missed, 0 kept, 1 instalment newly in arrears.")).toBeVisible();
  await expect(rows.filter({ hasText: "FAM/001" })).toContainText("Plan in arrears");
  await rows.filter({ hasText: "FAM/001" }).getByRole("link", { name: "FAM/001" }).click();
  await expect(page.getByRole("heading", { level: 2, name: "Chasing" })).toBeVisible();
  await expect(page.getByTestId("plan-standing")).toContainText("Plan in arrears");
  await expect(page.getByTestId("arrears")).toContainText("instalment 1");
  await expect(page.getByTestId("reminders")).toContainText("1: gentle");
  await axeBoth(page, "/schools/students/{id} (chasing)");
});
