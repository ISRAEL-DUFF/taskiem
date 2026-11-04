// The workflow builder, for the browser tests: make a workflow from
// "When should it run?", and add steps from "What happens next?".
import { expect, type Page } from "@playwright/test";

/** From Workflows: New workflow, its name, what starts it; the builder opens. */
export async function newWorkflow(page: Page, name: string, start = "When you start it") {
  await page.getByRole("link", { name: "New workflow" }).click();
  await page.getByLabel("Its name").fill(name);
  await page.getByRole("button", { name: new RegExp(`^${start}`) }).click();
  await expect(page.getByTestId("builder")).toBeVisible();
}

/** Adds a step (at the end, or after one by its name) found by searching; its window opens. */
export async function addStep(page: Page, search: string, pick: string | RegExp, after?: string) {
  if (after) await page.getByRole("button", { name: `Add a step after ${after}` }).click();
  else await page.getByRole("button", { name: "Add a step", exact: true }).click();
  const picker = page.getByRole("dialog", { name: "What happens next?" });
  await picker.getByLabel("Search").fill(search);
  await picker.getByRole("button", { name: typeof pick === "string" ? new RegExp(`^${pick}`) : pick }).first().click();
  await expect(page.getByTestId("step-window")).toBeVisible();
}

/** Closes the open step's window. */
export async function doneWithStep(page: Page) {
  await page.getByTestId("step-window").getByRole("button", { name: "Done" }).click();
  await expect(page.getByTestId("step-window")).toHaveCount(0);
}
