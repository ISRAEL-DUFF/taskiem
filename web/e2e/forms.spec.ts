import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// SCREENSHOT_DIR=... saves screenshots of the form screens for review.
const shot = async (page: Page, name: string) => {
  if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: `${process.env.SCREENSHOT_DIR}/${name}.png`, fullPage: true });
};

// axe is injected into the page, which the Content Security Policy would refuse.
test.use({ bypassCSP: true });

test("an owner builds a members form in the canvas, finds it on Forms and fills it in", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Workflows" }).click();

  await page.getByRole("button", { name: "New workflow" }).click();
  await page.getByLabel("Name").fill("Leave request");
  await page.getByRole("button", { name: "Create" }).click();
  await expect(page.getByTestId("node-start")).toBeVisible();

  // The trigger becomes a form: the panel turns into the form editor.
  await page.getByRole("tab", { name: "Trigger & settings" }).click();
  await page.getByRole("combobox", { name: "Type" }).first().selectOption("form");
  const editor = page.getByRole("region", { name: "Form", exact: true });
  await expect(editor.getByLabel("Form title")).toHaveValue("Leave request");
  await editor.getByRole("radio", { name: /Members of your organisation/ }).check();

  // The starter question is filled from the member's account.
  const name = editor.getByTestId("field-full_name");
  await name.getByRole("button", { name: "Full name", exact: true }).click();
  await name.getByLabel("From the member's account").selectOption("member.name");

  // Two more questions; names follow the labels.
  await editor.getByRole("button", { name: "+ Add a question" }).click();
  let q = editor.getByTestId(/^field-/).last();
  await q.getByLabel("Question", { exact: true }).fill("Reason");
  q = editor.getByTestId("field-reason");
  await q.getByRole("combobox", { name: "Type", exact: true }).selectOption("long_text");
  await q.getByLabel("Required", { exact: true }).check();
  await editor.getByRole("button", { name: "+ Add a question" }).click();
  q = editor.getByTestId(/^field-/).last();
  await q.getByLabel("Question", { exact: true }).fill("Days off");
  q = editor.getByTestId("field-days_off");
  await q.getByRole("combobox", { name: "Type", exact: true }).selectOption("number");
  await q.getByLabel("Required", { exact: true }).check();

  // A question a form never asks shows its problem at once.
  await editor.getByRole("button", { name: "+ Add a question" }).click();
  q = editor.getByTestId(/^field-/).last();
  await q.getByLabel("Question", { exact: true }).fill("PIN");
  await expect(q.getByText(/a form never asks for passwords, PINs/)).toBeVisible();
  await q.getByRole("button", { name: "Remove PIN" }).click();

  // The preview renders the form as people see it.
  const preview = page.getByRole("region", { name: "Preview", exact: true });
  await expect(preview.getByLabel(/Reason/)).toBeVisible();
  await expect(preview.getByLabel(/Days off/)).toHaveAttribute("type", "number");
  await expect(preview.getByLabel(/Full name/)).toHaveAttribute("readonly", "");

  await editor.getByTestId("field-reason").getByRole("button", { name: "Reason", exact: true }).click();
  await shot(page, "form-editor");
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/is published; new runs use it/)).toBeVisible({ timeout: 30_000 });

  // The Forms page lists it, live.
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Forms", exact: true }).click();
  const card = page.getByRole("article", { name: "Leave request" }).first();
  await expect(card.getByText("live", { exact: true })).toBeVisible();
  await expect(card.getByText("Members, in the app")).toBeVisible();
  await shot(page, "forms");
  const vs = await axe(page);
  expect(vs, describe("/forms", vs)).toEqual([]);

  // The member fills it in, in the app.
  await card.getByRole("link", { name: /\/forms\// }).click();
  await expect(page).toHaveURL(/\/forms\/[0-9a-f-]+$/);
  await expect(page.getByRole("heading", { level: 1, name: "Leave request" })).toBeVisible();
  await expect(page.getByLabel(/Full name/)).toHaveValue("Owner");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByText("This question needs an answer.").first()).toBeVisible();
  await page.getByLabel(/Reason/).fill("Family wedding in Enugu");
  await page.getByLabel(/Days off/).fill("3");
  await shot(page, "member-form");
  const fill = await axe(page);
  expect(fill, describe("/forms/:id", fill)).toEqual([]);
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByRole("heading", { name: "Sent" })).toBeVisible();
  const ref = (await page.locator(".form-done strong").textContent()) ?? "";
  expect(ref).not.toBe("");

  // Its answers are on the Forms page, with the run.
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Forms", exact: true }).click();
  await page.getByRole("article", { name: "Leave request" }).first().getByRole("link", { name: "See the answers" }).click();
  await expect(page.getByRole("button", { name: ref })).toBeVisible();
  await page.getByRole("button", { name: ref }).click();
  await expect(page.getByRole("dialog").getByText("Family wedding in Enugu")).toBeVisible();
});
