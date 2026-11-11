import { expect, test } from "@playwright/test";
import { addStep, doneWithStep, newWorkflow } from "./builder";

test("the run canvas lights up as the run goes", async ({ page }) => {
  // Signs in, builds and publishes a workflow, starts a run and cancels it:
  // some 25 steps, which on a loaded runner took each 1 to 5 s to settle and
  // outlasted 60 s (CI timed out on it five times, at a different step
  // each time). It gets 120 s, as the other long tests have.
  test.setTimeout(120_000);
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Workflows" }).click();

  await newWorkflow(page, "Live flow");
  await addStep(page, "change the data", "Change the data");
  await doneWithStep(page);
  await addStep(page, "wait", /^Wait An hour/, "Change the data");
  await doneWithStep(page);
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/is published; new runs use it/)).toBeVisible();
  await page.getByRole("button", { name: "Start run" }).click();
  await page.getByRole("button", { name: "Start", exact: true }).click();
  await expect(page).toHaveURL(/\/runs\//);

  // Streamed: the first step completes and the wait lights up.
  const canvas = page.getByLabel("Run canvas");
  await expect(canvas.getByTestId("node-change")).toHaveAttribute("data-status", "completed", { timeout: 30_000 });
  await expect(canvas.getByTestId("node-wait")).toHaveAttribute("data-status", "waiting");
  await expect(page.getByText("live", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Cancel run" }).click();
  await page.getByRole("dialog", { name: "Cancel this run?" }).getByRole("button", { name: "Cancel and reverse" }).click();
  await expect(page.locator("h1 + .badge").first()).toHaveText("cancelled");
  await expect(canvas.getByTestId("node-wait")).toHaveAttribute("data-status", "cancelled");
  await expect(page.getByText("live", { exact: true })).toHaveCount(0);
});
