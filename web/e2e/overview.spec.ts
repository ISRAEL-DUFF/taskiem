import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";
import { H, familySchool } from "./familyschool";

// The proprietor (docs/spec/schools.md 13, B9; docs/schools.md
// "Overview", "The daily close"): a school with two siblings invoiced,
// one part-paid in cash. The Overview says what was collected against
// what was expected, how parents paid, the pass-through kept apart, the
// top debtors masked, the clearance; the daily close is taken once by
// hand, listed and opened, and the Start page lists the school's
// templates. On a phone, with no horizontal scroll.
test.use({ bypassCSP: true }); // axe is injected into the page

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

test("the overview, the daily close taken once, and the school's templates on the Start page", async ({ page }) => {
  test.setTimeout(300_000);
  await familySchool(page, "Overview Family Academy");

  // The term: ₦236,000 expected over two children, ₦25,000.50 paid in
  // cash by a person; none of it pass-through (own fees first).
  await page.goto("/schools/overview");
  await expect(page.getByRole("heading", { level: 1, name: "Overview" })).toBeVisible();
  await expect(page.getByTestId("overview-term")).toHaveText("First term 2026/2027");
  const collected = page.getByTestId("overview-collected");
  await expect(collected).toContainText("₦25,000.50 of ₦236,000 collected (10%)");
  await expect(collected).toContainText("2 of 2 students invoiced");
  await expect(collected).toContainText("Nothing is due yet.");
  await expect(page.getByTestId("overview-week")).toContainText("₦25,000.50");
  await expect(page.getByTestId("overview-outstanding")).toContainText("₦210,999.50");
  await expect(page.getByTestId("overview-matched")).toContainText("0%");
  await expect(page.getByTestId("overview-matched")).toContainText("₦25,000.50 by a person");
  await expect(page.getByTestId("overview-pass-through")).toContainText("₦0");
  await expect(page.getByTestId("overview-clearance")).toContainText("0 of 2");
  await expect(page.getByTestId("overview-last-close")).toContainText("None yet");
  await expect(page.getByTestId("overview-how-paid")).toContainText("Cash: ₦25,000.50 (1 payment)");
  await expect(page.getByTestId("overview-aging")).toContainText("Not yet due: 2 students, ₦210,999.50");
  const debtors = page.getByTestId("top-debtor");
  await expect(debtors).toHaveCount(2);
  await expect(debtors.first()).toContainText("FAM/002");
  await expect(debtors.first()).toContainText("₦118,000");
  await expect(debtors.first()).toContainText("C•••");
  await expect(page.getByTestId("overview-top-debtors")).not.toContainText("Chidi Eze");
  await axeBoth(page, "/schools/overview");

  // The close: none yet; taken now by hand, once; listed and opened;
  // the Overview's tile follows.
  await expect(page.getByTestId("closes-none")).toBeVisible();
  await page.getByTestId("close-run").click();
  await expect(page.getByRole("status").filter({ hasText: "is closed" })).toContainText("is closed: reconciles.");
  await expect(page.getByTestId("close-row")).toHaveCount(1);
  const report = page.getByTestId("close-report");
  await expect(report).toBeVisible();
  await expect(report.getByTestId("close-status")).toHaveText("Reconciles");
  await expect(report).toContainText("₦0 in on students (0 payments)");
  await expect(report).toContainText("Nothing waits and everything reconciles.");
  await axeBoth(page, "/schools/overview (closed)");
  const again = await page.request.post("/v1/schools/close", { headers: H, data: {} });
  expect(again.status(), await again.text()).toBe(200);
  expect(((await again.json()) as { taken_now: boolean }).taken_now).toBe(false);
  await page.reload();
  await expect(page.getByTestId("overview-last-close")).not.toContainText("None yet");
  await expect(page.getByTestId("overview-last-close")).toContainText("reconciles");
  await expect(page.getByTestId("close-row")).toHaveCount(1);

  // A phone: nothing scrolls sideways.
  await page.setViewportSize({ width: 360, height: 740 });
  await page.reload();
  await expect(page.getByTestId("overview-collected")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await axeBoth(page, "/schools/overview (phone)");

  // The Start page lists the school's templates for a business with the
  // area on.
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/start");
  const school = page.getByTestId("school-start");
  await expect(school).toBeVisible();
  await expect(school.getByRole("link", { name: "Overview" })).toBeVisible();
  for (const id of ["school-issue-accounts", "school-invoice-term", "school-receipt", "school-daily-close", "school-fee-reminders", "school-plan-decided", "school-gate-list"]) {
    await expect(school.getByTestId(`school-template-${id}`)).toBeVisible();
  }
  await school.getByTestId("school-template-school-daily-close").click();
  await expect(page.getByRole("heading", { level: 1, name: "Send the proprietor the daily close every morning" })).toBeVisible();
});
