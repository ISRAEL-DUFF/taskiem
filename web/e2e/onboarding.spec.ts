import { expect, test } from "@playwright/test";

// Gate G4's path in the browser: sign up, pick the first starter (which
// needs nothing connected: no-technical-steps N4), publish, run it once,
// and see the time from signup to the first successful run.

test("sign up, set up a template, and run it once", async ({ page }) => {
  const started = Date.now();
  const email = `ada-${started}@stores.test`;

  await page.goto("/login");
  await page.getByRole("link", { name: "Create an account" }).click();
  await page.getByLabel("Business or organisation name").fill("Ada Stores");
  await page.getByLabel("Your name").fill("Ada Obi");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Create account" }).click();

  // Signed in, on Get started, with nothing done yet.
  await expect(page.getByRole("heading", { name: "Get started" })).toBeVisible();
  await expect(page.getByTestId("onboarding-progress")).toHaveText("0 of 8 done");
  await expect(page.getByRole("link", { name: /Get started \(0\/8\)/ })).toBeVisible();

  // The guided first workflow: the first starter needs nothing connected.
  await page.getByRole("link", { name: /Remind me every day/ }).click();
  await expect(page.getByRole("heading", { name: "Remind me every day" })).toBeVisible();
  await page.getByLabel("What to remind you of").fill("Count the cash and lock the shop.");
  await page.getByRole("button", { name: "Create and publish" }).click();
  await expect(page.getByText("Published.")).toBeVisible();
  await page.getByRole("button", { name: "Run it now" }).click();
  await expect(page.getByText("It worked.")).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("guide-run-status").locator(".badge")).toHaveText("completed");

  // The checklist follows, and the time to the first run is shown.
  await page.getByRole("link", { name: "← Get started" }).click();
  await expect(page.getByTestId("first-run-time")).toContainText("after you signed up");
  for (const id of ["connection", "workflow", "publish", "first_run"]) {
    await expect(page.getByTestId(`check-${id}`)).toHaveClass("done");
  }
  await expect(page.getByTestId("check-invite")).not.toHaveClass("done");
  await expect(page.getByTestId("check-verify_email")).not.toHaveClass("done");

  // Gate G4: under 15 minutes, as the server measured it.
  const ob = await (await page.request.get("/v1/onboarding")).json();
  expect(ob.seconds_to_first_run).toBeGreaterThanOrEqual(0);
  expect(ob.seconds_to_first_run).toBeLessThan(15 * 60);
  expect(ob.seconds_to_first_run).toBeLessThanOrEqual(Math.ceil((Date.now() - started) / 1000));

  // Inviting waits for the confirmed email.
  const invite = await page.request.post("/v1/members", {
    headers: { "X-Taskiem-Request": "1" },
    data: { email: `op-${started}@stores.test`, password: "correct horse battery", roles: ["operator"] },
  });
  expect(invite.status()).toBe(403);
});
