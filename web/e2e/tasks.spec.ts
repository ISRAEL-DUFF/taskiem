import { execFileSync } from "node:child_process";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Tasks for staff (docs/spec/chat-five-more-flows.md, flow 2; phase 3
// plan, step 4): the owner gives a task to Bisi by a time, sees it in
// Everyone's and reminds her; gives herself two and does one and can't do
// the other; and sets one to repeat every day, then stops it. The chat side
// (DONE, CAN'T, the photo, nudges, late) is in api/staff_test.go.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

async function owner(page: Page) {
  const email = `${letters()}-${Math.random().toString(36).slice(2, 8)}@tasks.test`;
  const r = await page.request.post("/v1/signup", { headers: H, data: { tenant: `Ada Kitchen ${letters()}`, email, name: "Ada Obi", password: "correct horse battery" } });
  expect(r.status(), await r.text()).toBe(201);
  // The e2e server sends no email: the owner's address is marked confirmed.
  sql(`UPDATE users SET email_verified_at = now() WHERE lower(email) = lower('${email}')`);
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(300);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

async function give(page: Page, what: string, who: string, when?: { kind: "By a time" | "Every day"; time: string }) {
  await page.getByRole("button", { name: "Give a task" }).click();
  const form = page.getByRole("dialog", { name: "Give a task" });
  await form.getByLabel("What is to be done").fill(what);
  await form.getByLabel("Who it is for").selectOption({ label: who });
  if (when) {
    await form.getByRole("combobox", { name: /^When/ }).selectOption({ label: when.kind });
    await form.getByLabel(when.kind === "By a time" ? "By" : "Due at", { exact: true }).fill(when.time);
  }
  await form.getByRole("button", { name: "Give the task" }).click();
  await expect(form).toHaveCount(0);
}

for (const width of [390, 360]) {
  test(`tasks are given, reminded, done, sent back and repeated, at ${width} px`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width, height: 844 });
    await owner(page);
    const added = await page.request.post("/v1/members", {
      headers: H,
      data: { email: `bisi-${letters()}@tasks.test`, name: "Bisi Okafor", password: "correct horse battery", roles: ["cashier"] },
    });
    expect(added.status(), await added.text()).toBe(201);
    await page.goto("/tasks");
    await expect(page.getByRole("heading", { name: "Tasks", level: 1 })).toBeVisible();
    await expect(page.getByText("You have no tasks.")).toBeVisible();

    // To Bisi, by 23:59 today: in Everyone's, with Remind.
    await page.getByRole("button", { name: "Give a task" }).click();
    await axeBoth(page, "giving a task");
    await page.getByRole("dialog", { name: "Give a task" }).getByRole("button", { name: "Cancel" }).click();
    await give(page, "Count the cash float", "Bisi Okafor", { kind: "By a time", time: "23:59" });
    await page.getByRole("button", { name: "Everyone's" }).click();
    const rows = page.getByTestId("task");
    const float = rows.filter({ hasText: "Count the cash float" });
    await expect(float).toContainText("Bisi Okafor");
    await expect(float).toContainText("by 23:59");
    await float.getByRole("button", { name: /^Remind/ }).click();
    await expect(page.getByText("Reminded Bisi Okafor")).toBeVisible();
    await axeBoth(page, "everyone's tasks");

    // Two for Ada herself: one done, one she can't do.
    await page.getByRole("button", { name: "Mine" }).click();
    await give(page, "Check the fridge", "Ada Obi");
    await give(page, "Buy diesel", "Ada Obi");
    await rows.filter({ hasText: "Check the fridge" }).getByRole("button", { name: /^Done/ }).click();
    await expect(rows.filter({ hasText: "Check the fridge" })).toContainText("Done");
    await rows.filter({ hasText: "Buy diesel" }).getByRole("button", { name: /^Can't/ }).click();
    const cant = page.getByRole("dialog", { name: "Can't do it" });
    await cant.getByLabel("Why").fill("No fuel station open");
    await axeBoth(page, "can't do a task");
    await cant.getByRole("button", { name: "Send it back" }).click();
    await expect(rows.filter({ hasText: "Buy diesel" })).toContainText(`can't: "No fuel station open"`);
    await axeBoth(page, "my tasks");

    // Every day at 07:00, then stopped.
    await give(page, "Open the shop", "Bisi Okafor", { kind: "Every day", time: "07:00" });
    await page.getByRole("button", { name: "Everyone's" }).click();
    const repeat = page.getByTestId("task-repeat").filter({ hasText: "Open the shop" });
    await expect(repeat).toContainText("Every day at 07:00");
    page.once("dialog", (d) => void d.accept());
    await repeat.getByRole("button", { name: /^Stop/ }).click();
    await expect(repeat).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  });
}
