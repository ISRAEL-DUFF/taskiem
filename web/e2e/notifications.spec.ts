import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Settings > Notifications (docs/alerts.md, docs/money-reports.md): the
// money summaries are a group of their own, on for owners by default, off
// for others who can see invoices, and absent for those who cannot; a group
// the page does not know is shown plainly. axe is injected into the page,
// which the console's Content Security Policy would refuse.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const PASSWORD = "correct horse battery";

async function signIn(page: Page, email: string) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

async function openSettings(page: Page) {
  await page.goto("/settings/notifications");
  await expect(page.getByRole("heading", { name: "Notifications", exact: true })).toBeVisible();
  await expect(page.getByText("Failed runs", { exact: true })).toBeVisible();
}

async function asMember(browser: Browser, owner: Page, email: string, role: string) {
  const made = await owner.request.post("/v1/members", { headers: H, data: { email, password: PASSWORD, roles: [role] } });
  expect(made.status(), await made.text()).toBe(201);
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await signIn(page, email);
  await openSettings(page);
  return page;
}

test("the money summaries can be turned off, and on by others who can see invoices", async ({ page, browser }) => {
  await signIn(page, "owner@e2e.test");
  await openSettings(page);

  // An owner gets both by default, and the page says so.
  await expect(page.getByText("Money summaries", { exact: true })).toBeVisible();
  await expect(page.getByText(/Owners get them unless they turn them off/)).toBeVisible();
  const daily = page.getByLabel("Daily money summary (chat)");
  const monthly = page.getByLabel("Monthly money email");
  await expect(daily).toBeChecked();
  await expect(monthly).toBeChecked();
  await daily.click();
  await expect(page.locator(".toast").filter({ hasText: "Saved" })).toBeVisible();
  await expect(daily).not.toBeChecked();
  await expect(monthly).toBeChecked();
  await page.reload();
  await expect(page.getByLabel("Daily money summary (chat)")).not.toBeChecked();
  await page.getByLabel("Daily money summary (chat)").click();
  await expect(page.getByLabel("Daily money summary (chat)")).toBeChecked();

  // The page passes axe.
  const vs = await axe(page);
  expect(vs, describe("/settings/notifications", vs)).toEqual([]);

  // A viewer can see invoices: the group is there, off until they turn it on.
  const stamp = Date.now();
  const viewer = await asMember(browser, page, `viewer-${stamp}@e2e.test`, "viewer");
  await expect(viewer.getByText("Money summaries", { exact: true })).toBeVisible();
  await expect(viewer.getByLabel("Daily money summary (chat)")).not.toBeChecked();
  await expect(viewer.getByLabel("Monthly money email")).not.toBeChecked();
  await viewer.getByLabel("Monthly money email").click();
  await expect(viewer.getByLabel("Monthly money email")).toBeChecked();
  await viewer.context().close();

  // A builder cannot: no money group.
  const builder = await asMember(browser, page, `builder-${stamp}@e2e.test`, "builder");
  await expect(builder.getByText("Setup problems", { exact: true })).toBeVisible();
  await expect(builder.getByText("Money summaries", { exact: true })).toHaveCount(0);
  await builder.context().close();
});

test("a group the page does not know is shown plainly", async ({ page }) => {
  await signIn(page, "owner@e2e.test");
  await page.route("**/v1/me/notification-settings", (route) =>
    route.request().method() === "GET"
      ? route.fulfill({
          json: {
            settings: [
              { group: "failures", chat: true, email: true },
              { group: "late_payments", chat: false, email: true },
            ],
          },
        })
      : route.continue(),
  );
  await openSettings(page);
  await expect(page.getByText("Late payments", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Late payments on WhatsApp or Telegram")).not.toBeChecked();
  await expect(page.getByLabel("Late payments by email")).toBeChecked();
  const vs = await axe(page);
  expect(vs, describe("/settings/notifications (an unknown group)", vs)).toEqual([]);
});
