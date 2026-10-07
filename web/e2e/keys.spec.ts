import { expect, test, type Page } from "@playwright/test";
import { totp } from "./totp";

async function signIn(page: Page, email: string) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();
}

test("encryption keys: rotate the tenant key with step-up, and a customer key is checked before use", async ({ page, browser }) => {
  // A second owner, so this test's authenticator stays its own.
  await signIn(page, "owner@e2e.test");
  const r = await page.request.post("/v1/members", {
    data: { email: "keys@e2e.test", password: "correct horse battery", roles: ["owner"] },
    headers: { "X-Taskiem-Request": "1" },
  });
  expect(r.ok(), `add member: ${r.status()} ${await r.text()}`).toBeTruthy();

  const ctx = await browser.newContext();
  const keys = await ctx.newPage();
  await signIn(keys, "keys@e2e.test");
  await keys.getByRole("link", { name: "Encryption keys" }).click();
  await expect(keys.getByRole("heading", { name: "Encryption keys" })).toBeVisible();
  await expect(keys.getByTestId("keys-summary")).toContainText("wrapped by Taskiem's key");

  // Without a second factor, a key change is refused with where to add one.
  keys.once("dialog", (d) => void d.accept());
  await keys.getByRole("button", { name: "Rotate tenant key" }).click();
  await expect(keys.getByRole("dialog")).toContainText("add a passkey or an authenticator app");
  await keys.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();

  // With an authenticator, the rotation is confirmed with a code.
  await keys.getByRole("link", { name: "keys@e2e.test" }).click();
  await keys.getByLabel("Your password, to set up an authenticator").fill("correct horse battery");
  await keys.getByRole("button", { name: "Set up an authenticator" }).click();
  const secret = ((await keys.getByTestId("totp-secret").textContent()) ?? "").trim();
  await keys.getByLabel("Code from the app").fill(totp(secret));
  await keys.getByRole("button", { name: "Confirm" }).click();
  await expect(keys.getByText("An authenticator is enrolled.")).toBeVisible();

  await keys.getByRole("link", { name: "Encryption keys" }).click();
  keys.once("dialog", (d) => void d.accept());
  await keys.getByRole("button", { name: "Rotate tenant key" }).click();
  const confirm = keys.getByRole("dialog");
  await confirm.getByLabel("Code from your authenticator app").fill(totp(secret, 1));
  await confirm.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(keys.getByRole("status")).toContainText(/Version \d+ is current/);
  await expect(keys.getByRole("cell", { name: "current" })).toBeVisible();

  // Bring your own key: nothing is saved unless the key works. A plain
  // http address is refused before any call, and before any step-up.
  const byok = keys.getByTestId("keys-byok");
  await expect(byok.getByRole("button", { name: "Verify and use this key" })).toBeDisabled();
  await byok.getByLabel("Address").fill("http://bao.example.com:8200");
  await byok.getByLabel("Key name").fill("taskiem");
  await byok.getByRole("textbox", { name: /^Token/ }).fill("s.not-a-real-token");
  await byok.getByRole("button", { name: "Verify and use this key" }).click();
  await expect(byok.getByRole("alert")).toContainText("https");
  await expect(keys.getByTestId("keys-summary")).toContainText("wrapped by Taskiem's key");
  await ctx.close();
});
