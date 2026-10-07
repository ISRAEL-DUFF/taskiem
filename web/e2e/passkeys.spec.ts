import { expect, test } from "@playwright/test";

// Passkeys with Chrome's virtual authenticator: enrol under Account, sign
// out, sign back in with the passkey. WebAuthn needs a host name, not an
// IP address, so this test browses localhost.
const base = "http://localhost:18080";

test("add a passkey and sign in with it", async ({ page }) => {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });

  await page.goto(`${base}/login`);
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();

  await page.goto(`${base}/account`);
  await page.getByLabel("Name for this passkey").fill("Virtual key");
  await page.getByLabel("Your password, to add or remove a passkey").fill("correct horse battery");
  await page.getByRole("button", { name: "Add a passkey" }).click();
  await expect(page.getByRole("cell", { name: "Virtual key" })).toBeVisible();

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login/);
  await page.getByRole("button", { name: "Sign in with a passkey" }).click();
  // Back where the member signed out.
  await expect(page.getByRole("heading", { name: "Account" })).toBeVisible();
  await expect(page.getByRole("cell", { name: "Virtual key" })).toBeVisible();
});
