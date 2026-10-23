import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// A prod Paystack connection shows the addresses to paste into Paystack's
// dashboard so the money log hears it without a workflow (decision 0045),
// with a copy button, what each carries and the polling note, and passes
// axe in both themes. axe is injected, which the console's Content
// Security Policy would refuse.
test.use({ bypassCSP: true });

async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

for (const scheme of ["light", "dark"] as const) {
  test(`a rail connection shows its money log webhook, and passes axe in the ${scheme} theme`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await signIn(page);
    const r = await page.request.post("/v1/connections", {
      data: { environment: "prod", connector: "paystack@1", name: "moneylog_e2e", mode: "test", credentials: { secret_key: "sk_test_e2e_never_shown" } },
      headers: { "X-Taskiem-Request": "1" },
    });
    expect(r.ok() || r.status() === 409, `add connection: ${r.status()} ${await r.text()}`).toBeTruthy();
    await page.goto("/connections");
    await expect(page.locator(".skeleton")).toHaveCount(0);
    const section = page.locator("section", { has: page.getByRole("heading", { name: "Webhook for the money log" }) });
    await expect(section).toBeVisible();
    await expect(section.getByText("Payments you receive")).toBeVisible();
    await expect(section.getByText(/\/connectors\/paystack@1\/charge_event\?connection=moneylog_e2e&env=prod$/)).toBeVisible();
    await expect(section.getByRole("button", { name: "Copy the Payments you receive address for Paystack (moneylog_e2e)" })).toBeVisible();
    await expect(section.getByText("Carries payments received and payments returned.").first()).toBeVisible();
    await expect(section.getByText(/Taskiem also checks every 5 minutes/).first()).toBeVisible();
    await expect(section.getByText(/Live Webhook URL/)).toBeVisible();
    await expect(page.getByText("sk_test_e2e_never_shown")).toHaveCount(0);
    const vs = await axe(page);
    expect(vs, describe("/connections", vs)).toEqual([]);
  });
}
