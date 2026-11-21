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

// What a newer server adds (stubbed until it is there): a file read again
// each time it is sent, one too big kept back, Excel's UTF-16 text read,
// a plan's warnings and lines saved already, a file changed since it was
// checked, and what saving left.
test("a file read again, too big, in UTF-16, and what a plan and saving say", async ({ page }) => {
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 390, height: 844 });
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: `Ada Kitchen ${letters()}`, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@saved.test`, name: "Ada Obi", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
  const sent: { csv: string; confirm?: string }[] = [];
  const answers: { status: number; body: unknown }[] = [];
  await page.route("**/v1/saved/import", (q) => {
    sent.push(q.request().postDataJSON() as { csv: string; confirm?: string });
    const a = answers.shift() ?? { status: 500, body: { error: "No answer." } };
    return q.fulfill({ status: a.status, contentType: "application/json", body: JSON.stringify(a.body) });
  });
  await page.goto("/settings/saved");
  await page.getByTestId("saved").getByRole("button", { name: "Import from a file" }).click();
  const sheet = page.getByRole("dialog", { name: "Import from a file" });
  const check = sheet.getByRole("button", { name: "Check the file" });

  // Too big: said, nothing sent.
  await pick(page, "big.csv", "title\n" + "x".repeat(256 * 1024));
  await check.click();
  await expect(sheet).toContainText("The file is too big: up to 256 KB.");
  expect(sent).toHaveLength(0);

  // Refused, put right and chosen again under the same name: the new text is sent.
  answers.push({ status: 422, body: { error: "Some lines cannot be saved: put them right and send the file again.", code: "import_unreadable", problems: [{ line: 3, message: "Choose the bank." }], total: 2 } });
  await pick(page, "bad.csv", BAD);
  await check.click();
  await expect(sheet.getByTestId("import-problem")).toContainText("Line 3");
  const fixed = "title,account_number,bank\nChi Stores,0123456780,Access\nDayo,0123456789,GTBank\n";
  const plan = {
    saved: false,
    hash: "h1",
    total: 2,
    rows: [
      { line: 2, name: "chi-stores", title: "Chi Stores", kind: "bank_account", hint: "Access ••6780", warning: "The account name is checked once saved." },
      { line: 3, name: "dayo", title: "Dayo", kind: "bank_account", hint: "GTBank ••6789", skip: true },
    ],
  };
  answers.push({ status: 200, body: plan });
  await pick(page, "bad.csv", fixed);
  await check.click();
  const shown = sheet.getByTestId("import-plan");
  await expect(shown).toContainText("This file saves 1 bank account. 1 line is saved already: skipped.");
  expect(sent[1]?.csv).toBe(fixed);
  await expect(shown.locator("[data-name='chi-stores'] [data-testid='import-warning']")).toHaveText("The account name is checked once saved.");
  await expect(shown.locator("[data-name='dayo']")).toContainText("saved already: skipped");
  await axeBoth(page, "a plan with a warning and a line skipped", 390);

  // Changed since it was checked: the plan goes, to check it again.
  answers.push({ status: 409, body: { error: "The file changed since it was checked: check it again.", code: "import_changed" } });
  await sheet.getByRole("button", { name: "Save 1 thing" }).click();
  await expect(sheet).toContainText("The file changed since it was checked");
  await expect(sheet.getByTestId("import-plan")).toHaveCount(0);
  expect(sent[2]).toEqual({ csv: fixed, confirm: "h1" });

  // Checked again and saved: what was skipped, not checked and left.
  answers.push({ status: 200, body: plan });
  await check.click();
  answers.push({
    status: 200,
    body: { saved: true, hash: "h1", total: 2, added: ["chi-stores"], failed: [], skipped: ["dayo"], unchecked: [{ line: 2, name: "chi-stores", problem: "The bank did not answer." }], left: [{ line: 4, name: "tola" }] },
  });
  await sheet.getByRole("button", { name: "Save 1 thing" }).click();
  await expect(sheet.getByTestId("import-status")).toHaveText("Saved 1 thing: @chi-stores. 1 was saved already: skipped. 1 line is not saved yet: send the file again.");
  await expect(sheet.getByTestId("import-unchecked")).toContainText("The bank did not answer.");
  await expect(sheet).toContainText("Check names on the Saved page.");
  await expect(sheet.getByTestId("import-skipped")).toHaveText("@dayo");
  await expect(sheet.getByTestId("import-left")).toContainText("Line 4: @tola");
  await axeBoth(page, "saved, with lines skipped and left", 390);

  // Excel's "Unicode text": UTF-16 with tabs, read as its words.
  await sheet.getByRole("button", { name: "Import another file" }).click();
  const tsv = "title\tphone\nỌlá Stores\t08031234567\n";
  answers.push({ status: 200, body: { saved: false, hash: "h2", total: 1, rows: [{ line: 2, name: "ola-stores", title: "Ọlá Stores", kind: "person", hint: "" }] } });
  await page.getByTestId("import-file").setInputFiles({ name: "people.txt", mimeType: "text/plain", buffer: Buffer.concat([Buffer.from([0xff, 0xfe]), Buffer.from(tsv, "utf16le")]) });
  await check.click();
  await expect(sheet.getByTestId("import-plan")).toContainText("This file saves 1 person.");
  expect(sent.at(-1)?.csv).toBe(tsv);
});
