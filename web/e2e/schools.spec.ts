import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// The Schools area (decision 0046, B2; docs/schools.md): classes, the
// import of a school's students (a bad line adds nothing; a good file is
// shown, then saved; the same file again adds nobody twice), and starting
// a term, which enrols each student once. serve.sh turns the area on for
// the E2E business.
test.use({ bypassCSP: true }); // axe is injected into the page

async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

const file = (name: string, text: string) => ({ name, mimeType: "text/csv", buffer: Buffer.from(text) });

const BAD = [
  "Name,Admission number,Class,Guardian name,Phone",
  "Ebube Eze,E2E/001,JSS 1 A,Ngozi Eze,08031234567",
  "Chidi Eze,E2E/002,JSS 7,Ngozi Eze,08031234567",
  "Musa Bello,E2E/003,JSS 2 A,Aisha Bello,0805",
].join("\n");

const GOOD = [
  "Name,Admission number,Class,Guardian name,Phone,Email,Relationship",
  "Ebube Eze,E2E/001,JSS 1 A,Ngozi Eze,08031234567,ngozi@example.com,Mother",
  "Chidi Eze,E2E/002,JSS 2 A,Ngozi Eze,08031234567,,",
  "Musa Bello,E2E/003,JSS 1 A,Aisha Bello,08051112222,,Mother",
].join("\n");

async function total(page: Page): Promise<number> {
  const r = await page.request.get("/v1/schools/students?status=active&limit=1000");
  expect(r.status(), await r.text()).toBe(200);
  return ((await r.json()) as { students: { admission_number: string }[] }).students.filter((s) => s.admission_number.startsWith("E2E/")).length;
}

test("students are imported whole or not at all, never twice, and a term enrols them once", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page);
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Schools" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Students" })).toBeVisible();

  // Classes first: the file names them.
  await page.getByRole("navigation", { name: "Schools" }).getByRole("link", { name: "Classes and terms" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Classes and terms" })).toBeVisible();
  const addClass = page.getByRole("form", { name: "Add a class" });
  for (const { name, arm, order } of [
    { name: "JSS 1", arm: "A", order: "7" },
    { name: "JSS 2", arm: "A", order: "8" },
  ]) {
    await addClass.getByLabel("Name").fill(name);
    await addClass.getByLabel("Arm (optional)").fill(arm);
    await addClass.getByLabel("Order").fill(order);
    await addClass.getByRole("button", { name: "Add class" }).click();
    await expect(page.getByTestId("class").filter({ hasText: `${name} ${arm}` })).toBeVisible();
  }

  // A file with bad lines saves nothing and says which lines and why.
  await page.goto("/schools/import");
  await expect(page.getByRole("heading", { level: 1, name: "Import students" })).toBeVisible();
  await page.getByLabel("Students file (CSV)").setInputFiles(file("students.csv", BAD));
  await page.getByRole("button", { name: "Check the file" }).click();
  const refusal = page.getByTestId("import-refusal");
  await expect(refusal).toContainText("nothing was saved");
  const lines = refusal.getByRole("list", { name: "Lines that cannot be imported" });
  await expect(lines).toContainText("Line 4:");
  await expect(lines).not.toContainText("Line 2:");
  await axeBoth(page, "/schools/import (lines that cannot be imported)");
  expect(await total(page)).toBe(0);

  // A good file is shown first, with its families, then saved.
  await page.getByLabel("Students file (CSV)").setInputFiles(file("students.csv", GOOD));
  await page.getByRole("button", { name: "Check the file" }).click();
  const plan = page.getByTestId("import-plan");
  await expect(plan).toContainText("This file will add 3 students, in 2 new families.");
  await expect(plan.getByTestId("import-family")).toHaveCount(2);
  await expect(plan.getByTestId("import-family").first()).toContainText("E2E/001, E2E/002");
  await axeBoth(page, "/schools/import (the plan)");
  expect(await total(page)).toBe(0);
  await plan.getByRole("button", { name: "Save 3 students" }).click();
  await expect(page.getByTestId("import-status")).toHaveText("Saved: 3 students added, 0 updated.");
  expect(await total(page)).toBe(3);

  // The same file again updates them: nobody twice.
  await page.getByLabel("Students file (CSV)").setInputFiles(file("students.csv", GOOD));
  await page.getByRole("button", { name: "Check the file" }).click();
  await expect(page.getByTestId("import-plan")).toContainText("This file will update 3 students already here, in 2 families already here.");
  await page.getByRole("button", { name: "Save 3 students" }).click();
  await expect(page.getByTestId("import-status")).toHaveText("Saved: 0 students added, 3 updated.");
  expect(await total(page)).toBe(3);

  // The list, masked, then with names.
  await page.getByRole("navigation", { name: "Schools" }).getByRole("link", { name: "Students" }).click();
  const row = page.getByTestId("student").filter({ hasText: "E2E/001" });
  await expect(row).toContainText("E•••");
  await page.getByLabel("Show names").click();
  await expect(page.getByLabel("Show names")).toBeChecked();
  await expect(page.getByTestId("student").filter({ hasText: "E2E/001" })).toContainText("Ebube Eze");
  await axeBoth(page, "/schools");

  // A session, a term, and starting it: each student enrolled once.
  await page.goto("/schools/classes");
  const addSession = page.getByRole("form", { name: "Add a session" });
  await addSession.getByLabel("Session").fill("2026/2027");
  await addSession.getByRole("button", { name: "Add session" }).click();
  const session = page.getByTestId("session").filter({ hasText: "Session 2026/2027" });
  await expect(session).toBeVisible();
  const addTerm = session.getByRole("form", { name: "Add a term to 2026/2027" });
  await addTerm.getByLabel("Term").fill("First term");
  await addTerm.getByLabel("Starts").fill("2026-09-14");
  await addTerm.getByLabel("Ends").fill("2026-12-11");
  await addTerm.getByLabel("Fees due").fill("2026-09-07");
  await addTerm.getByRole("button", { name: "Add term" }).click();
  const term = session.getByTestId("term").filter({ hasText: "First term" });
  await expect(term).toContainText("Not started");
  await term.getByRole("button", { name: "Start First term 2026/2027" }).click();
  await term.getByRole("button", { name: "Start First term now" }).click();
  await expect(session.getByRole("status")).toHaveText("First term 2026/2027 is the current term: 3 students enrolled.");
  await expect(term).toContainText("Current term");
  await expect(page.getByTestId("current-term")).toContainText("First term");
  await axeBoth(page, "/schools/classes");

  // The enrollments, once each, over the API.
  const sessions = (await (await page.request.get("/v1/schools/sessions")).json()) as { sessions: { label: string; terms: { id: string; name: string; enrolled: number }[] }[] };
  const first = sessions.sessions.find((s) => s.label === "2026/2027")?.terms.find((t) => t.name === "First term");
  expect(first?.enrolled).toBe(3);
  const again = await page.request.post(`/v1/schools/terms/${first?.id}/start`, { headers: { "X-Taskiem-Request": "1" } });
  expect(((await again.json()) as { enrolled: number }).enrolled).toBe(0);

  // A student's page: their family and class history.
  await page.goto("/schools");
  await page.getByRole("link", { name: "E2E/001" }).click();
  await expect(page.getByRole("heading", { name: "Class history" })).toBeVisible();
  await expect(page.getByTestId("family")).toContainText("E2E/002");
  await expect(page.getByRole("cell", { name: "First term 2026/2027 (current)" })).toBeVisible();
  await axeBoth(page, "/schools/students/:id");
});
