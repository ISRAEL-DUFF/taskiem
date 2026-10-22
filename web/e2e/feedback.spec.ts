import { expect, test } from "@playwright/test";

const H = { "X-Taskiem-Request": "1" };

const failing = {
  schema: "wd/v1",
  id: "wf_bellCheck",
  version: 1,
  name: "Bell check",
  trigger: { type: "manual" },
  steps: [{ id: "check", type: "transform", config: { output: { x: "=int('not a number')" } } }],
};

// Telling people what happened (no-technical-steps N12): an action says how
// it went, an unexpected error carries a reference, and a failed run is in
// the bell with nothing set up.
test("actions say how they went, and the bell tells what happened", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();

  // A setting saved says so.
  await page.getByRole("link", { name: "Notifications" }).click();
  await expect(page.getByRole("heading", { name: "Notifications", exact: true })).toBeVisible();
  const finishedByEmail = page.getByLabel("Your runs that finish by email");
  await finishedByEmail.click();
  await expect(page.locator(".toast").filter({ hasText: "Saved" })).toBeVisible();
  await expect(finishedByEmail).toBeChecked();
  await finishedByEmail.click();
  await expect(finishedByEmail).not.toBeChecked();

  // An unexpected error is said in plain words, with its reference.
  await page.route("**/v1/me/notification-settings", (route) =>
    route.request().method() === "PUT"
      ? route.fulfill({ status: 500, json: { error: "Something went wrong on our side. Reference E-TEST42", code: "internal", reference: "E-TEST42" } })
      : route.continue(),
  );
  await finishedByEmail.click();
  await expect(page.getByRole("alert").filter({ hasText: "Reference E-TEST42" })).toBeVisible();
  await page.unroute("**/v1/me/notification-settings");

  // A run that fails is in the bell, without any alert set up.
  const made = await page.request.post("/v1/workflows", { headers: H, data: { name: "Bell check", definition: failing } });
  const { id } = (await made.json()) as { id: string };
  expect((await page.request.post(`/v1/workflows/${id}/versions/1/publish`, { headers: H })).ok()).toBeTruthy();
  expect((await page.request.post(`/v1/workflows/${id}/runs`, { headers: H, data: { input: {} } })).ok()).toBeTruthy();
  await expect
    .poll(async () => ((await (await page.request.get("/v1/notifications?unread=true")).json()) as { notifications: { title: string }[] }).notifications.map((n) => n.title), {
      timeout: 30_000,
    })
    .toContain("Bell check failed");
  await page.reload();
  const bell = page.getByRole("button", { name: /^Notifications, \d+ new$/ }).first();
  await bell.click();
  const panel = page.getByRole("dialog", { name: "Notifications" });
  await expect(panel.getByRole("region", { name: "Today" }).getByText("Bell check failed", { exact: true })).toBeVisible();
  await panel.getByRole("button", { name: /Bell check failed/ }).click();
  await expect(page).toHaveURL(/\/runs\//);
  const unread = ((await (await page.request.get("/v1/notifications?unread=true")).json()) as { notifications: { title: string }[] }).notifications.map((n) => n.title);
  expect(unread).not.toContain("Bell check failed");
});
