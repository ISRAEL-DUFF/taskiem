import { execFileSync } from "node:child_process";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Staff by phone (docs/spec/chat-five-more-flows.md, phase 3 plan, step 1):
// the owner adds someone by name and number, sees them waiting to join,
// cannot send it again at once, cancels it, and removes someone who left.
// The e2e stack's fake WhatsApp is not running here, so the page says the
// message did not go (api/staff_test.go sends it and joins).
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

async function owner(page: Page) {
  const email = `${letters()}-${Math.random().toString(36).slice(2, 8)}@staff.test`;
  const r = await page.request.post("/v1/signup", { headers: H, data: { tenant: `Ada Kitchen ${letters()}`, email, name: "Ada Obi", password: "correct horse battery" } });
  expect(r.status(), await r.text()).toBe(201);
  // The e2e server sends no email: the owner's address is marked confirmed.
  sql(`UPDATE users SET email_verified_at = now() WHERE lower(email) = lower('${email}')`);
  return email;
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

for (const width of [390, 360]) {
  test(`staff are added by phone, waited for, cancelled and removed, at ${width} px`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width, height: 844 });
    await owner(page);
    // Someone already in the business, to remove.
    const added = await page.request.post("/v1/members", {
      headers: H,
      data: { email: `bisi-${letters()}@staff.test`, name: "Bisi Okafor", password: "correct horse battery", roles: ["cashier"] },
    });
    expect(added.status(), await added.text()).toBe(201);
    await page.goto("/staff");
    await expect(page.getByRole("heading", { name: "Staff", level: 1 })).toBeVisible();
    const people = page.getByTestId("staff-person");
    await expect(people.filter({ hasText: "Ada Obi (you)" })).toBeVisible();
    await expect(people.filter({ hasText: "Bisi Okafor" })).toContainText("Cashier");

    // Add Tunde by phone.
    await page.getByRole("button", { name: "Add staff" }).click();
    const dialog = page.getByRole("dialog", { name: "Add staff" });
    await dialog.getByRole("button", { name: "Send the invitation" }).click();
    await expect(dialog.getByRole("alert")).toHaveText("Give their name.");
    await dialog.getByLabel("Their name").fill("Tunde Bello");
    await dialog.getByLabel("Their mobile number").fill(`0803 ${String(Date.now()).slice(-7, -4)} ${String(Date.now()).slice(-4)}`);
    await dialog.getByLabel("What they do").selectOption("shift_manager");
    await expect(dialog).toContainText("handing the shift over");
    await axeBoth(page, "adding staff");
    await dialog.getByRole("button", { name: "Send the invitation" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByText("Tunde Bello is added, but the WhatsApp message did not go")).toBeVisible();
    const waiting = page.getByTestId("staff-invite").filter({ hasText: "Tunde Bello" });
    await expect(waiting).toContainText("Shift manager");
    await expect(waiting).toContainText("not sent yet");
    await axeBoth(page, "the staff page");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

    // Sent again: it still does not go, and the page says so.
    await waiting.getByRole("button", { name: /^Send again/ }).click();
    await expect(page.getByText("The WhatsApp message did not go. Try again in a little while.")).toBeVisible();
    page.once("dialog", (d) => void d.accept());
    await waiting.getByRole("button", { name: /^Cancel/ }).click();
    await expect(page.getByTestId("staff-invite")).toHaveCount(0);

    // Bisi has left.
    page.once("dialog", (d) => void d.accept());
    await people.filter({ hasText: "Bisi Okafor" }).getByRole("button", { name: /^Remove/ }).click();
    await expect(people.filter({ hasText: "Bisi Okafor" })).toHaveCount(0);
    await expect(people.filter({ hasText: "Ada Obi" }).getByRole("button", { name: /^Remove/ })).toHaveCount(0);
  });
}
