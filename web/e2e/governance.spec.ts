import { createHmac } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";

// RFC 6238 code for a base32 secret, as an authenticator app computes it.
function totp(secret: string, offsetSteps = 0): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const ch of secret.replace(/=+$/, "")) bits += alphabet.indexOf(ch).toString(2).padStart(5, "0");
  const key = Buffer.from((bits.match(/.{8}/g) ?? []).map((b) => parseInt(b, 2)));
  const step = Math.floor(Date.now() / 1000 / 30) + offsetSteps;
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(step));
  const mac = createHmac("sha1", key).update(msg).digest();
  const off = (mac[mac.length - 1] ?? 0) & 0x0f;
  return String((mac.readUInt32BE(off) & 0x7fffffff) % 1_000_000).padStart(6, "0");
}

async function signIn(page: Page, email: string) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();
}

test("a policy routes an approval that needs an authenticator code", async ({ page, browser }) => {
  await signIn(page, "owner@e2e.test");
  const api = async (method: string, path: string, data?: unknown) => {
    const r = await page.request.fetch(path, { method, data, headers: { "X-Taskiem-Request": "1" } });
    expect(r.ok(), `${method} ${path}: ${r.status()} ${await r.text()}`).toBeTruthy();
    return (await r.json().catch(() => ({}))) as Record<string, unknown>;
  };
  await api("POST", "/v1/members", { email: "ann@e2e.test", password: "correct horse battery", roles: ["approver", "treasury"] });

  // The owner writes the policy on the Policies page.
  await page.getByRole("link", { name: "Approval policies" }).click();
  await page.getByLabel("Name").fill("treasury_payouts");
  await page.locator("textarea.mono").fill(JSON.stringify({ rules: [{ levels: [{ role: "treasury" }], step_up: "totp" }] }));
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("policy-treasury_payouts-1")).toContainText("active");

  const wf = await api("POST", "/v1/workflows", {
    name: "Treasury payout",
    definition: {
      schema: "wd/v1", id: "wf_treasuryPayout", version: 1, name: "Treasury payout", trigger: { type: "manual" },
      steps: [{ id: "ok", type: "approval", config: { policy: "treasury_payouts", subject: { amount: "=trigger.body.amount" } } }],
    },
  });
  await api("POST", `/v1/workflows/${String(wf.id)}/versions/1/publish`);
  const run = await api("POST", `/v1/workflows/${String(wf.id)}/runs`, { input: { amount: 250000 } });

  // Ann enrols an authenticator, then approves with a code.
  const ctx = await browser.newContext();
  const ann = await ctx.newPage();
  await signIn(ann, "ann@e2e.test");
  await ann.getByRole("link", { name: "ann@e2e.test" }).click();
  await ann.getByLabel("Your password, to set up an authenticator").fill("correct horse battery");
  await ann.getByRole("button", { name: "Set up an authenticator" }).click();
  const secret = ((await ann.getByTestId("totp-secret").textContent()) ?? "").trim();
  await ann.getByLabel("Code from the app").fill(totp(secret));
  await ann.getByRole("button", { name: "Confirm" }).click();
  await expect(ann.getByText("An authenticator is enrolled.")).toBeVisible();

  await ann.getByRole("link", { name: "Approvals" }).click();
  const card = ann.getByTestId("approval");
  await expect(card).toContainText("a code from your authenticator app");
  await card.getByRole("button", { name: "Approve" }).click();
  await ann.getByLabel("Authenticator code").fill(totp(secret, 1));
  await ann.getByRole("dialog").getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(ann.getByText("Nothing waiting for you.")).toBeVisible();

  await expect.poll(async () => ((await api("GET", `/v1/runs/${String(run.run_id)}`)).run as { status: string }).status).toBe("completed");
  await ctx.close();

  // The approval shows in the owner's approvals report, with its step-up.
  await page.getByRole("link", { name: "Reports" }).click();
  await expect(page.getByTestId("report-table")).toContainText("ann@e2e.test");
  await expect(page.getByTestId("report-table")).toContainText("totp");
});
