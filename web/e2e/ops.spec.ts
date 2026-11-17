import { execFileSync } from "node:child_process";
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

  // A report on a business's listing in Discover (taskiem-for-everyone §10):
  // the listing is taken out, with a reason and the passkey, and put back.
  const tenant = sql("SELECT id FROM tenants WHERE name = 'E2E'");
  sql(
    `INSERT INTO page_reports (id, tenant_id, kind, site_id, reason, message, ip_hash) VALUES (gen_random_uuid(), '${tenant}', 'discover', '${tenant}', 'prohibited', 'Selling medicines', decode(repeat('ab', 32), 'hex'))`,
  );
  await page.goto(`${base}/ops/reports`);
  const report = page.getByRole("row").filter({ hasText: "Selling medicines" });
  await expect(report).toContainText("Discover listing");
  await report.getByRole("button", { name: /Remove or restore/ }).click();
  const panel = page.getByTestId("ops-discover-action");
  await expect(panel.getByRole("button", { name: "Remove from Discover with passkey" })).toBeDisabled();
  await panel.getByLabel(/Reason/).fill("Selling medicines without a licence");
  page.once("dialog", (d) => void d.accept());
  await panel.getByRole("button", { name: "Remove from Discover with passkey" }).click();
  await expect(panel.getByRole("status")).toHaveText("Taken out of Discover. The owners are told, with your reason.");
  expect(sql(`SELECT removed_note FROM discover_settings WHERE tenant_id = '${tenant}'`)).toBe("Selling medicines without a licence");
  await panel.getByRole("button", { name: "Restore with passkey" }).click();
  await expect(panel.getByRole("status")).toHaveText("Put back in Discover. The owners are told.");
  expect(sql(`SELECT removed_at IS NULL FROM discover_settings WHERE tenant_id = '${tenant}'`)).toBe("t");
});

function sql(text: string): string {
  const db = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!db) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [db.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}
