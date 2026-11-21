import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Saved things from a file (saved objects S3; POST /v1/saved/import): on
// the business's Saved page, a CSV is checked whole and shown as rows,
// then saved; a file with a line that cannot be taken saves nothing and
// says each line and why. Against the real server.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

// SCREENSHOT_DIR=... saves screenshots for review.
const shot = async (page: Page, name: string) => {
  if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: `${process.env.SCREENSHOT_DIR}/${name}.png`, fullPage: true });
};

async function axeBoth(page: Page, where: string, width: number) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(300);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
    await shot(page, `saved-import-${where.replace(/\W+/g, "-")}-${width}-${scheme}`);
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
  expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
}

const GOOD = "Title,Name,Bank,Account Number,Phone,Street,Area\n" + "Bayo Rice Supplies,bayo,GTBank,0123456789,,,\n" + "Bisi Okafor,bisi,,,08031234567,,\n" + "Ikeja shop,,,,,12 Allen Avenue,Ikeja\n";

const BAD = "title,account_number,bank\nChi Stores,0123456780,Access\nDayo,0123456789,\n";

const pick = (page: Page, name: string, body: string) => page.getByTestId("import-file").setInputFiles({ name, mimeType: "text/csv", buffer: Buffer.from(body) });

for (const width of [390, 360]) {
  test(`import saved things from a file, and a bad line, at ${width} px`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 844 });
    const r = await page.request.post("/v1/signup", {
      headers: H,
      data: { tenant: `Ada Kitchen ${letters()}`, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@saved.test`, name: "Ada Obi", password: "correct horse battery" },
    });
    expect(r.status(), await r.text()).toBe(201);
    await page.goto("/settings/saved");
    const card = page.getByTestId("saved");
    await expect(card.getByRole("button", { name: "Import from a file" })).toBeVisible();
    await card.getByRole("button", { name: "Import from a file" }).click();
    const sheet = page.getByRole("dialog", { name: "Import from a file" });
    await expect(sheet.getByTestId("import-example")).toHaveAttribute("download", "saved-things-example.csv");
    // The example downloads as it is.
    const [download] = await Promise.all([page.waitForEvent("download"), sheet.getByTestId("import-example").click()]);
    expect(download.suggestedFilename()).toBe("saved-things-example.csv");
    // The server reads it: one of each kind.
    await page.getByTestId("import-file").setInputFiles((await download.path()) ?? "");
    await sheet.getByRole("button", { name: "Check the file" }).click();
    await expect(sheet.getByTestId("import-plan")).toContainText("This file saves 4 things: 1 bank account, 1 person, 1 place, 1 text.");

    // A bad line: nothing saved, each line and why.
    await pick(page, "bad.csv", BAD);
    await sheet.getByRole("button", { name: "Check the file" }).click();
    await expect(sheet.getByTestId("import-refusal")).toContainText("Some lines cannot be saved");
    await expect(sheet.getByTestId("import-problem")).toHaveCount(1);
    await expect(sheet.getByTestId("import-problem")).toContainText("Line 3");
    await expect(sheet.getByTestId("import-problem")).toContainText("Choose the bank.");
    await axeBoth(page, "a bad line", width);

    // A good file: the plan as rows, then saved.
    await pick(page, "good.csv", GOOD);
    await expect(sheet.getByTestId("import-refusal")).toHaveCount(0);
    await sheet.getByRole("button", { name: "Check the file" }).click();
    const plan = sheet.getByTestId("import-plan");
    await expect(plan).toContainText("This file saves 3 things: 1 bank account, 1 person, 1 place.");
    await expect(plan.getByTestId("import-row")).toHaveCount(3);
    await expect(plan.locator("[data-name='bayo']")).toContainText("@bayo · A bank account");
    await expect(plan.locator("[data-name='bayo']")).toContainText("••6789");
    await expect(plan.locator("[data-name='bayo']")).not.toContainText("0123456789");
    await expect(plan.locator("[data-name='ikeja-shop']")).toContainText("A place");
    await axeBoth(page, "the plan", width);
    await sheet.getByRole("button", { name: "Save 3 things" }).click();
    await expect(sheet.getByTestId("import-status")).toHaveText("Saved 3 things: @bayo, @bisi, @ikeja-shop.");
    await expect(sheet.getByTestId("import-plan")).toHaveCount(0);
    await axeBoth(page, "saved", width);
    // The list is read again behind it.
    await sheet.getByRole("button", { name: "Done" }).click();
    await expect(card.getByTestId("saved-row")).toHaveCount(3);
    await expect(card.locator("[data-name='bisi']")).toBeVisible();
    await card.scrollIntoViewIfNeeded();
    await shot(page, `saved-import-card-${width}`);

    // The same file again: each name is taken now.
    await card.getByRole("button", { name: "Import from a file" }).click();
    await pick(page, "good.csv", GOOD);
    await sheet.getByRole("button", { name: "Check the file" }).click();
    await expect(sheet.getByTestId("import-problem")).toHaveCount(3);
    await expect(sheet.getByTestId("import-problem").first()).toContainText("@bayo is saved already");
  });
}
