import { expect, test, type Page } from "@playwright/test";

// Invitations: the members page lists pending ones and withdraws them; a
// person with an account but no organisation signs in to answer theirs,
// and joins on accepting.

const H = { "X-Taskiem-Request": "1" };

async function signIn(page: Page, email: string) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
}

/** Makes someone with an account who belongs nowhere, then invites them. */
async function inviteLeaver(page: Page, email: string) {
  const added = await page.request.post("/v1/members", { headers: H, data: { email, password: "correct horse battery", roles: ["viewer"] } });
  expect(added.status()).toBe(201);
  const { user_id } = (await added.json()) as { user_id: string };
  expect((await page.request.delete(`/v1/members/${user_id}/roles/viewer`, { headers: H })).status()).toBe(204);
  const invited = await page.request.post("/v1/members", { headers: H, data: { email, roles: ["builder"] } });
  expect(invited.status()).toBe(201);
}

test("pending invitations are listed and withdrawn; an invitee with no organisation signs in and joins", async ({ page }) => {
  const stamp = Date.now();
  const bob = `bob-${stamp}@e2e.test`;
  const carol = `carol-${stamp}@e2e.test`;
  await signIn(page, "owner@e2e.test");
  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();
  await inviteLeaver(page, bob);
  await inviteLeaver(page, carol);

  await page.goto("/members");
  const pending = page.getByTestId("pending-invitations");
  await expect(pending.getByRole("cell", { name: bob })).toBeVisible();
  await expect(pending.getByRole("cell", { name: carol })).toBeVisible();
  page.once("dialog", (d) => void d.accept());
  await pending.getByRole("row", { name: new RegExp(carol) }).getByRole("button", { name: "Withdraw" }).click();
  await expect(pending.getByRole("cell", { name: carol })).toHaveCount(0);
  await page.request.post("/v1/auth/logout", { headers: H });
  await page.context().clearCookies();

  // Carol's invitation is gone: with no organisation she cannot sign in.
  await signIn(page, carol);
  await expect(page.getByRole("alert")).toContainText("no active membership");

  // Bob signs in to his invitations only.
  await signIn(page, bob);
  const mine = page.getByTestId("my-invitations");
  await expect(page.getByText("You do not belong to an organisation on Taskiem yet")).toBeVisible();
  await expect(mine.getByRole("cell", { name: "E2E" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Main" })).toHaveCount(0);
  expect((await page.request.get("/v1/workflows")).status()).toBe(403);
  await mine.getByRole("button", { name: "Accept" }).click();
  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Main" }).getByText("builder")).toBeVisible();
  expect((await page.request.get("/v1/workflows")).status()).toBe(200);
});
