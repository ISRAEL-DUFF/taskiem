import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// What your business uses (docs/spec/business-areas.md): a new business
// has every part on; an owner turns Money off and its pages leave the menu
// and say so; the shop needs Orders; turning it back on restores everything.
// At a phone's width, passing axe in both themes.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@areas.test`, name: tenant, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

test("an owner turns Money off and on again", async ({ page }) => {
  await signUp(page, `Areas Shop ${letters()}`);
  await page.goto("/settings/areas");
  await expect(page.getByRole("heading", { level: 1, name: "What your business uses" })).toBeVisible();
  const money = page.getByTestId("area-money");
  await expect(money).toHaveAttribute("data-on", "true");
  await expect(page.getByTestId("area-schools")).toHaveAttribute("data-on", "false");
  await axeBoth(page, "What your business uses");

  // Orders cannot go off under the shop, so its switch is locked.
  await expect(page.getByLabel("Orders")).toBeDisabled();

  // Money off: asked first, then its pages say so.
  await page.getByLabel("Money and invoices").uncheck();
  const ask = page.getByRole("dialog");
  await expect(ask).toContainText("Nothing is deleted");
  await ask.getByRole("button", { name: "Turn off" }).click();
  await expect(money).toHaveAttribute("data-on", "false");
  await page.goto("/invoices");
  await expect(page.getByText("Money and invoices is not turned on for your business.")).toBeVisible();
  await expect(page.getByRole("link", { name: "Turn it on" })).toBeVisible();
  await axeBoth(page, "A part that is off");
  // The API refuses it too, in plain words.
  const refused = await page.request.get("/v1/invoices", { headers: H });
  expect(refused.status()).toBe(404);
  expect((await refused.json()).area_off).toBe("money");

  // And on again: the page is back.
  await page.getByRole("link", { name: "Turn it on" }).click();
  await page.getByLabel("Money and invoices").check();
  await expect(money).toHaveAttribute("data-on", "true");
  await page.goto("/invoices");
  await expect(page.getByText("is not turned on for your business.")).toHaveCount(0);
});
