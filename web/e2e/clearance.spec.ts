import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";
import { H, PASSWORD, familySchool, sql } from "./familyschool";

// Clearance and the gate (docs/spec/schools.md 12, B8; docs/schools.md
// "Clearance"): a school with two siblings invoiced; one part-paid, one
// unpaid. The clearance list says partly paid and not paid; paying the
// rest clears the first at once; the gate answers CLEARED for the paid
// child and SEE THE BURSAR for the other, with no amount anywhere; an
// override with a reason clears the unpaid child until its end date, and
// no longer past it; a gate session sees the one screen at any address,
// and the server refuses it everything else.
test.use({ bypassCSP: true }); // axe is injected into the page

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

/** Searches the gate screen on the page and taps the child; answers the verdict section. */
async function gateCheck(page: Page, letters: string, child: string) {
  const q = page.getByLabel("Child's name");
  await q.fill("");
  await q.fill(letters);
  const hits = page.getByTestId("gate-hits");
  await expect(hits).toBeVisible();
  await hits.getByRole("button", { name: new RegExp(child) }).click();
  const answer = page.getByTestId("gate-answer");
  await expect(answer).toBeVisible();
  await expect(answer).toContainText(child);
  return answer;
}

test("the clearance list, the gate's answers, an override until its end date, and a gate session held to one screen", async ({ page, browser }) => {
  test.setTimeout(300_000);
  await familySchool(page, "Gate Family Academy");

  // The list: partly paid and not paid, never cleared by a part payment.
  await page.goto("/schools/clearance");
  await expect(page.getByRole("heading", { level: 1, name: "Clearance" })).toBeVisible();
  await expect(page.getByTestId("clearance-totals")).toContainText("2 students: 0 cleared, 1 partly paid, 1 not paid.");
  const rows = page.getByTestId("clearance-row");
  await expect(rows).toHaveCount(2);
  const first = rows.filter({ hasText: "FAM/001" });
  const second = rows.filter({ hasText: "FAM/002" });
  await expect(first).toContainText("Partly paid");
  await expect(first).toContainText("₦92,999.50");
  await expect(second).toContainText("Not paid");
  await axeBoth(page, "/schools/clearance");

  // Paying the rest clears at once: nothing is stored.
  const paid = await page.request.post("/v1/schools/payments", { headers: { ...H, "Idempotency-Key": "gate-rest" }, data: { student: "FAM/001", amount_kobo: 9_299_950, how: "cash" } });
  expect(paid.status(), await paid.text()).toBe(201);
  await page.reload();
  await expect(first).toContainText("Cleared");
  await expect(first).toContainText("paid in full");
  await expect(page.getByTestId("clearance-totals")).toContainText("1 cleared, 0 partly paid, 1 not paid");

  // The gate, as the bursar: the paid child is cleared, the other sees
  // the bursar; the recent list keeps both; no amount on the screen.
  await page.getByRole("link", { name: "Open the gate screen" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "The gate" })).toBeVisible();
  const cleared = await gateCheck(page, "Ebu", "Ebube Eze");
  await expect(cleared).toContainText("CLEARED");
  await expect(cleared).toHaveAttribute("data-status", "cleared");
  await axeBoth(page, "/schools/gate (cleared)");
  await page.getByRole("button", { name: "Check another child" }).click();
  const bursar = await gateCheck(page, "Chi", "Chidi Eze");
  await expect(bursar).toContainText("SEE THE BURSAR");
  await expect(bursar).toHaveAttribute("data-status", "see_bursar");
  await expect(page.getByTestId("gate-recent").locator("li")).toHaveCount(2);
  await expect(page.getByTestId("gate-screen")).not.toContainText("₦");
  await axeBoth(page, "/schools/gate (see the bursar)");

  // An override with a reason clears the unpaid child; the gate follows;
  // the student's page shows it.
  await page.goto("/schools/clearance");
  await second.getByRole("button", { name: "Clear by override" }).click();
  const form = page.getByRole("form", { name: "Clear by override" });
  await form.getByLabel("Reason").fill("Bereavement in the family");
  await form.getByRole("button", { name: "Clear the student" }).click();
  await expect(page.getByText(/FAM\/002 cleared by override until/)).toBeVisible();
  await expect(second).toContainText("Cleared");
  await expect(second.getByTestId("override-line")).toContainText("Bereavement in the family");
  await expect(page.getByTestId("overrides").getByTestId("override")).toHaveCount(1);
  await expect(page.getByTestId("override")).toContainText("In force");
  await axeBoth(page, "/schools/clearance (override)");
  await page.goto("/schools/gate");
  await expect(await gateCheck(page, "Chi", "Chidi Eze")).toContainText("CLEARED");
  await page.goto("/schools/clearance");
  await second.getByRole("link", { name: "FAM/002" }).click();
  await expect(page.getByRole("heading", { level: 2, name: "Clearance" })).toBeVisible();
  await expect(page.getByTestId("clearance-status")).toContainText("Cleared");
  await expect(page.getByTestId("clearance-status")).toContainText("cleared by override until");
  await expect(page.getByTestId("student-overrides")).toContainText("Bereavement in the family");
  await axeBoth(page, "/schools/students/{id} (clearance)");

  // Past its end date the override no longer counts.
  sql(`UPDATE school_clearance_overrides SET starts_on = (now() AT TIME ZONE 'Africa/Lagos')::date - 10, ends_on = (now() AT TIME ZONE 'Africa/Lagos')::date - 1 WHERE tenant_id = (SELECT id FROM tenants WHERE name = 'Gate Family Academy')`);
  await page.goto("/schools/clearance");
  await expect(second).toContainText("Not paid");
  await expect(page.getByTestId("override")).toContainText("Ended");
  await page.goto("/schools/gate");
  await expect(await gateCheck(page, "Chi", "Chidi Eze")).toContainText("SEE THE BURSAR");

  // A gate session: the one screen at any address, no menu; the search
  // and the answer in both themes; the server refuses everything else;
  // signing out clears the recent list.
  const email = `gate-${Date.now()}@gateschool.test`;
  const added = await page.request.post("/v1/members", { headers: H, data: { email, password: PASSWORD, roles: ["gate"] } });
  expect(added.status(), await added.text()).toBe(201);
  const gate = await (await browser.newContext()).newPage();
  await gate.goto("/login");
  await gate.getByLabel("Email").fill(email);
  await gate.getByLabel("Password").fill(PASSWORD);
  await gate.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(gate.getByTestId("gate-screen")).toBeVisible();
  await expect(gate.getByRole("heading", { level: 1 })).toContainText("Gate Family Academy");
  await expect(gate.locator("nav#main-nav")).toHaveCount(0);
  await gate.goto("/schools/students");
  await expect(gate.getByTestId("gate-screen")).toBeVisible();
  await expect(gate.getByRole("link", { name: "Students" })).toHaveCount(0);
  const refused = await gate.request.get("/v1/schools/students", { headers: H });
  expect(refused.status()).toBe(403);
  expect(((await refused.json()) as { gate_only?: boolean }).gate_only).toBe(true);
  expect((await gate.request.get("/v1/schools/clearance", { headers: H })).status()).toBe(403);
  await gate.getByLabel("Child's name").fill("Eb");
  await expect(gate.getByText("Type at least 3 letters")).toBeVisible();
  await expect(await gateCheck(gate, "Ebu", "Ebube Eze")).toContainText("CLEARED");
  await axeBoth(gate, "the gate (cleared)");
  await gate.getByRole("button", { name: "Check another child" }).click();
  await expect(await gateCheck(gate, "Chi", "Chidi Eze")).toContainText("SEE THE BURSAR");
  await expect(gate.getByTestId("gate-screen")).not.toContainText("₦");
  await expect(gate.getByTestId("gate-recent").locator("li")).toHaveCount(2);
  await axeBoth(gate, "the gate (see the bursar)");
  // One-handed on a phone: no horizontal scroll at a narrow width.
  await gate.setViewportSize({ width: 360, height: 740 });
  await gate.getByRole("button", { name: "Check another child" }).click();
  await expect(await gateCheck(gate, "Ebu", "Ebube Eze")).toContainText("CLEARED");
  expect(await gate.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await axeBoth(gate, "the gate (phone)");
  await gate.getByRole("button", { name: "Sign out" }).click();
  await expect(gate.getByRole("heading", { name: "Sign in to Taskiem" })).toBeVisible();
  expect(await gate.evaluate(() => sessionStorage.getItem("taskiem-gate-recent"))).toBeNull();
});
