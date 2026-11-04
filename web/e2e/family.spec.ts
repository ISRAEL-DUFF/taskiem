import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";
import { H, PUBLIC, familySchool, sql } from "./familyschool";

// The family page (docs/spec/schools.md 10 and 11.3, B6; docs/schools.md
// "The family page"): a link sent from the Families page opens the
// family's page on the public host (serve.sh runs it on 18082), with each
// child switchable; "I have paid" becomes a claim in To match, which the
// bursar dismisses; a plan request reaches the student's page and is
// approved; an expired link offers a new one. The school is a business of
// its own.
test.use({ bypassCSP: true }); // axe is injected into the page

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

test("a family opens its page from a link, says it paid, asks for a plan, and gets a new link when one expires", async ({ page, browser }) => {
  test.setTimeout(240_000);
  await familySchool(page, "Eze Family Academy");

  // The Families page sends a link; the owner can copy it.
  await page.goto("/schools/families");
  await expect(page.getByRole("heading", { level: 1, name: "Families" })).toBeVisible();
  await page.getByRole("button", { name: "Send a link" }).first().click();
  await expect(page.getByTestId("family-link-sent")).toBeVisible();
  const href = (await page.getByTestId("family-link").getAttribute("href")) ?? "";
  expect(href).toMatch(/^https:\/\/127\.0\.0\.1\/s\/[A-Za-z0-9_-]{32}$/);
  const link = href.replace("https://127.0.0.1", PUBLIC);

  // The family, with no account, opens it: both children, the chosen one
  // in the address; the fees line by line; the receipt.
  const parent = await (await browser.newContext()).newPage();
  await parent.goto(link);
  await expect(parent.getByRole("heading", { level: 1, name: "School fees" })).toBeVisible();
  await expect(parent.getByText("Eze Family Academy").first()).toBeVisible();
  const children = parent.getByRole("navigation", { name: "Children" });
  await expect(children.getByRole("link", { name: "Ebube Eze" })).toHaveAttribute("aria-current", "page");
  await expect(parent.getByText("Collected on behalf of WAEC.")).toBeVisible();
  await expect(parent.getByRole("cell", { name: "RCT-2026T1-0001" })).toBeVisible();
  await expect(parent.getByText("₦92,999.50").first()).toBeVisible();
  await axeBoth(parent, "the family page");
  await children.getByRole("link", { name: "Chidi Eze" }).click();
  await expect(parent).toHaveURL(/child=/);
  await expect(parent.getByRole("heading", { level: 2, name: "Chidi Eze", exact: true })).toBeVisible();
  await expect(parent.getByText("₦118,000").first()).toBeVisible();
  await expect(parent.getByRole("cell", { name: "RCT-2026T1-0001" })).toHaveCount(0);

  // I have paid: a claim, never a payment.
  const paid = parent.locator("section", { has: parent.getByRole("heading", { name: "I have paid some other way" }) });
  await paid.getByLabel("Amount paid (₦)").fill("40,000");
  await paid.getByLabel("Date paid").fill("2026-10-20");
  await paid.getByLabel("How you paid").selectOption({ label: "Bank transfer" });
  await paid.getByLabel("Reference (optional)").fill("GTB/4471");
  await paid.getByRole("button", { name: "Tell the school I have paid" }).click();
  await expect(parent.getByText("The school will look for your payment")).toBeVisible();
  await expect
    .poll(async () => ((await (await page.request.get("/v1/schools/claims", { headers: H })).json()) as { claims: unknown[] }).claims.length, { timeout: 20_000 })
    .toBe(1);
  await parent.reload();
  await expect(parent.getByText("₦40,000 by bank transfer on 20 Oct 2026")).toBeVisible();
  await expect(parent.getByText("The school is looking for it")).toBeVisible();
  const invoices = (await (await page.request.get("/v1/invoices", { headers: H })).json()) as { invoices: { paid_kobo: number }[] };
  expect(invoices.invoices.reduce((n, i) => n + i.paid_kobo, 0)).toBe(2_500_050);

  // The bursar sees it in To match and dismisses it with a reason.
  await page.goto("/invoices/match");
  await expect(page.getByRole("heading", { level: 2, name: "Families say they paid" })).toBeVisible();
  const claim = page.getByTestId("claim").filter({ hasText: "FAM/002" });
  await expect(claim).toContainText("₦40,000 by bank transfer on 20 Oct 2026, ref GTB/4471");
  await axeBoth(page, "/invoices/match (claims)");
  await claim.getByRole("button", { name: "Dismiss" }).click();
  await claim.getByLabel("Why it is dismissed").fill("No such transfer on the statement");
  await claim.getByRole("button", { name: "Dismiss the claim" }).click();
  await expect(page.getByText("Claim dismissed")).toBeVisible();
  await expect(page.getByRole("heading", { level: 2, name: "Families say they paid" })).toHaveCount(0);
  await parent.reload();
  await expect(parent.getByText("Not found by the school")).toBeVisible();

  // A plan request: on the student's page, approved by the owner, the
  // family told.
  const plan = parent.locator("section", { has: parent.getByRole("heading", { name: "Ask for a payment plan" }) });
  await plan.getByLabel("Amount 1 (₦)").fill("50,000");
  await plan.getByLabel("By date 1").fill("2026-11-30");
  await plan.getByLabel("Amount 2 (₦)").fill("68,000");
  await plan.getByLabel("By date 2").fill("2026-12-20");
  await plan.getByLabel("A short message (optional)").fill("Salary comes on the 25th");
  await plan.getByRole("button", { name: "Ask for this plan" }).click();
  await expect(parent.getByText("The school will look at your request")).toBeVisible();
  const students = (await (await page.request.get("/v1/schools/students?status=active&limit=10", { headers: H })).json()) as { students: { id: string; admission_number: string }[] };
  const chidi = students.students.find((s) => s.admission_number === "FAM/002")?.id ?? "";
  await expect
    .poll(async () => ((await (await page.request.get("/v1/schools/plans", { headers: H })).json()) as { plans: unknown[] }).plans.length, { timeout: 20_000 })
    .toBe(1);
  await page.goto(`/schools/students/${chidi}`);
  const card = page.getByTestId("plan");
  await expect(card).toContainText("₦50,000 by 30 Nov 2026; ₦68,000 by 20 Dec 2026 (₦118,000 in all)");
  await expect(card).toContainText("Salary comes on the 25th");
  await expect(card.getByText("Waiting for a decision")).toBeVisible();
  await axeBoth(page, "/schools/students/:id (plan request)");
  await card.getByRole("button", { name: "Approve…" }).click();
  await card.getByLabel("A note to the family (optional)").fill("Pay the first part before resumption");
  await card.getByRole("button", { name: "Approve the plan" }).click();
  await expect(card.getByText("Approved", { exact: true })).toBeVisible();
  await parent.reload();
  await expect(parent.getByText("Approved", { exact: true })).toBeVisible();
  await expect(parent.getByText("Pay the first part before resumption")).toBeVisible();

  // Expired: the page says so, never "not found", and sends a new link.
  const token = link.slice(link.lastIndexOf("/") + 1);
  sql(`UPDATE school_family_links SET expires_at = now() - interval '1 hour' WHERE token_hash = sha256(convert_to('taskiem family page ${token}', 'UTF8'))`);
  const before = Number(sql(`SELECT count(*) FROM school_family_links WHERE family_id = (SELECT family_id FROM school_family_links WHERE token_hash = sha256(convert_to('taskiem family page ${token}', 'UTF8')))`));
  await parent.goto(link);
  await expect(parent.getByRole("heading", { level: 1, name: "Ask the school for a new link" })).toBeVisible();
  await expect(parent.getByText("not found")).toHaveCount(0);
  await expect(parent.getByText("Ebube")).toHaveCount(0);
  await axeBoth(parent, "the expired link page");
  await parent.getByRole("button", { name: "Send me a new link" }).click();
  await expect(parent.getByText("a new link is on its way")).toBeVisible();
  await expect
    .poll(() => Number(sql(`SELECT count(*) FROM school_family_links WHERE family_id = (SELECT family_id FROM school_family_links WHERE token_hash = sha256(convert_to('taskiem family page ${token}', 'UTF8')))`)), {
      timeout: 20_000,
    })
    .toBe(before + 1);
  // A token nobody minted says the same, with no button.
  await parent.goto(`${PUBLIC}/s/${"A".repeat(32)}`);
  await expect(parent.getByRole("heading", { level: 1, name: "Ask the school for a new link" })).toBeVisible();
  await expect(parent.getByRole("button", { name: "Send me a new link" })).toHaveCount(0);

  // The link life is the owner's setting.
  await page.goto("/settings/schools");
  const days = page.locator("section", { has: page.getByRole("heading", { name: "Family page links" }) });
  await days.getByLabel("Days a link works").fill("7");
  await days.getByRole("button", { name: "Save the link life" }).click();
  await expect(page.getByText("Link life saved")).toBeVisible();
  const settings = (await (await page.request.get("/v1/settings/schools", { headers: H })).json()) as { family_link_days: number };
  expect(settings.family_link_days).toBe(7);
});
