import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { expect, test } from "@playwright/test";

// The operator console with Chrome's virtual authenticator: an operator
// enrols a passkey from the one-time link the CLI printed (serve.sh), signs
// out and back in with it, and approves p_e2e_ledger 1.1.0 from the review
// queue (seed-catalogue.sh), confirming every checklist item; the approval
// asks for the passkey again. WebAuthn needs a host name, so this browses
// localhost.
const base = "http://localhost:18080";

test("an operator signs in with a passkey and approves a catalogue submission", async ({ page }) => {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  const link = readFileSync(fileURLToPath(new URL("../../bin/e2e-ops-enrol.txt", import.meta.url)), "utf8").trim();
  expect(link).toContain(`${base}/ops/enrol#`);

  // Tenants' sign-in does not reach the console.
  await page.goto(`${base}/ops`);
  await expect(page.getByRole("heading", { name: "Taskiem operator console" })).toBeVisible();

  await page.goto(link);
  await expect(page.getByRole("heading", { name: "Enrol your passkey" })).toBeVisible();
  await page.getByLabel("Name for this passkey").fill("Virtual key");
  await page.getByRole("button", { name: "Create passkey" }).click();
  await expect(page.getByRole("heading", { name: "Review queue" })).toBeVisible();

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/ops\/login/);
  await page.getByRole("button", { name: "Sign in with your passkey" }).click();
  await expect(page.getByRole("heading", { name: "Review queue" })).toBeVisible();
  await expect(page.getByText("ops@taskiem.test")).toBeVisible();

  await page.getByRole("link", { name: "p_e2e_ledger 1.1.0" }).click();
  await expect(page.getByRole("heading", { name: "p_e2e_ledger 1.1.0" })).toBeVisible();
  await expect(page.getByRole("list", { name: "Automated checks" })).toContainText("signature");
  await expect(page.getByText("You review as")).toContainText("ops@taskiem.test");

  const approve = page.getByRole("button", { name: "Approve" });
  await page.getByLabel("Note to the publisher").fill("Classes, hosts and fixtures checked against the provider's documentation");
  await expect(approve).toBeDisabled();
  const boxes = page.getByRole("list", { name: "Review checklist" }).getByRole("checkbox");
  await expect(boxes).toHaveCount(8);
  for (const box of await boxes.all()) await box.check();
  await approve.click();
  await expect(page.getByRole("status")).toContainText("Submission approved");
  await expect(page.getByText("Reviewed by ops@taskiem.test")).toBeVisible();

  // The approval is in the platform audit chain.
  await page.getByRole("link", { name: "Platform audit" }).click();
  await expect(page.getByRole("cell", { name: "catalogue.approve" })).toBeVisible();
  await expect(page.getByText("Chain intact")).toBeVisible();
});
