import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";
import { addStep, doneWithStep, newWorkflow } from "./builder";

// The workflow builder (docs/workflow-builder.md): start from what runs
// it, add steps at a +, an If with Yes and No, test with sample data, and
// the same on a phone as a list.

// SCREENSHOT_DIR=... saves screenshots of the builder for review.
const shot = async (page: Page, name: string) => {
  if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: `${process.env.SCREENSHOT_DIR}/${name}.png` });
};

// axe is injected into the page, which the Content Security Policy would refuse.
test.use({ bypassCSP: true });

const signIn = async (page: Page) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Workflows" }).click();
};

test("build from a blank start: steps at a +, an If, a test with sample data", async ({ page }) => {
  await signIn(page);
  await newWorkflow(page, "Thank customers who pay", "A payment comes in");
  await expect(page.getByTestId("node-trigger")).toContainText("Your payments account tells it about a payment");
  await shot(page, "builder-empty");

  // The first step: a payment's fields are there to use before any test.
  await page.getByRole("button", { name: "Add the first step" }).click();
  const picker = page.getByRole("dialog", { name: "What happens next?" });
  await expect(picker.getByRole("list", { name: "Suggested" })).toBeVisible();
  await expect(picker.getByRole("list", { name: "Groups" }).getByRole("button", { name: /^Payments/ })).toBeVisible();
  await shot(page, "builder-picker");
  await picker.getByLabel("Search").fill("change the data");
  await picker.getByRole("button", { name: /^Change the data/ }).click();
  const win = page.getByTestId("step-window");
  await expect(win.getByRole("region", { name: "What came in" })).toContainText("Amount (₦)");
  await doneWithStep(page);

  // An If after it, a step on its Yes way; No is left empty.
  await addStep(page, "if", /^If Go one way/, "Change the data");
  await expect(win.getByText("Go the Yes way when")).toBeVisible();
  await doneWithStep(page);
  await expect(page.getByTestId("node-check")).toContainText("Is it so?");
  await page.getByRole("button", { name: "Add a step to this path" }).first().click();
  await picker.getByLabel("Search").fill("wait");
  await picker.getByRole("button", { name: /^Wait An hour/ }).click();
  await doneWithStep(page);

  // Undo takes the wait away; redo brings it back.
  await page.getByRole("button", { name: "Undo" }).click();
  await expect(page.getByTestId("node-wait")).toHaveCount(0);
  await page.getByRole("button", { name: "Redo" }).click();
  await expect(page.getByTestId("node-wait")).toBeVisible();

  // Test with sample data: every step ran, nothing was sent.
  await page.getByRole("button", { name: "Test workflow" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Tested with sample data" })).toContainText("Nothing was sent");
  for (const id of ["change", "check", "wait"]) await expect(page.getByTestId(`node-${id}`)).toHaveAttribute("data-status", "completed");
  await shot(page, "builder-tested");
  let vs = await axe(page);
  expect(vs, describe("builder after a test", vs)).toEqual([]);

  // Its window shows what came in and out of the test.
  await page.getByTestId("node-change").click();
  await expect(win.getByRole("region", { name: "What came out" })).toContainText("It ran");
  // The sample's 1000 kobo, in naira.
  await expect(win.locator(".in-field", { hasText: "Amount (₦)" })).toContainText("10");
  await expect(win).toContainText("Step 1 of 3");
  await win.getByRole("button", { name: /Is it so\?/ }).click();
  await expect(win.getByRole("heading", { name: "Is it so?" })).toBeVisible();
  await shot(page, "builder-window");
  vs = await axe(page);
  expect(vs, describe("a step's window", vs)).toEqual([]);
  await doneWithStep(page);

  // Saved: the If keeps No without steps in it (wd/v1 needs a step in a
  // list, so an empty No is left out), and comes back with Yes and No.
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.locator(".builder-saved")).toHaveText("Saved");
  const id = page.url().split("/workflows/")[1]?.split("?")[0] ?? "";
  const r = await page.request.get(`/v1/workflows/${id}/versions/2`);
  const def = ((await r.json()) as { definition: { steps: { id: string; config?: Record<string, unknown> }[] } }).definition;
  expect(def.steps.map((s) => s.id)).toEqual(["change", "check"]);
  expect(def.steps[1]?.config?.default).toBeUndefined();
  await page.reload();
  await expect(page.getByTestId("node-wait")).toBeVisible();
  await expect(page.getByRole("button", { name: "Add a step to this path" })).toBeVisible();
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the steps are a list, each opens full screen", async ({ page }) => {
    await page.goto("/login");
    await page.getByLabel("Email").fill("owner@e2e.test");
    await page.getByLabel("Password").fill("correct horse battery");
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
    await page.goto("/workflows/new");
    await page.getByLabel("Its name").fill("Phone flow");
    await page.getByRole("button", { name: /^When you start it/ }).click();
    const list = page.getByTestId("phone-steps");
    await expect(list).toBeVisible();
    await expect(page.locator(".react-flow")).toBeHidden();

    await list.getByRole("button", { name: "Add the first step" }).click();
    const picker = page.getByRole("dialog", { name: "What happens next?" });
    await picker.getByLabel("Search").fill("wait");
    await picker.getByRole("button", { name: /^Wait An hour/ }).click();
    await doneWithStep(page);
    await expect(list.getByTestId("phone-step-wait")).toBeVisible();
    await list.getByRole("button", { name: "Test workflow" }).click();
    await expect(list.getByTestId("phone-step-wait").getByRole("img", { name: "Ran" })).toBeVisible();
    await shot(page, "builder-phone");

    await list.getByTestId("phone-step-wait").click();
    await expect(page.getByTestId("step-window")).toBeVisible();
    const wide = await page.evaluate(() => document.documentElement.scrollWidth);
    expect(wide).toBeLessThanOrEqual(390);
    await shot(page, "builder-phone-window");
    const vs = await axe(page);
    expect(vs, describe("a step's window on a phone", vs)).toEqual([]);
  });
});
