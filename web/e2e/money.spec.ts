import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// The Money area (decision 0045, T6; docs/money-log.md "Money"): uploading
// a bank statement in the bank's own columns, entering an expense, setting
// the fee agreed with a connection, the overview and its journal export
// (T7), and closing a leak. The leak is written
// straight into the e2e database (as the detector would write it; serve.sh
// makes the database), so the test does not wait for the detector.
test.use({ bypassCSP: true }); // axe is injected into the page

function dsn(): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1");
}

function sql(text: string): string {
  return execFileSync("psql", [dsn(), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

// A statement as a bank writes it: its own column names, money in and out apart.
const BANK_CSV = [
  "Trans Date,Value Date,Narration,Reference,Debit,Credit,Balance",
  "01/10/2026,01/10/2026,Transfer from ADA OBI,E2E-STMT-1,,\"25,000.00\",\"125,000.00\"",
  "02/10/2026,02/10/2026,Diesel for the generator,E2E-STMT-2,\"12,000.00\",,\"113,000.00\"",
  "03/10/2026,03/10/2026,SMS alert charges,,52.50,,\"112,947.50\"",
].join("\n");

const file = (name: string, text: string) => ({ name, mimeType: "text/csv", buffer: Buffer.from(text) });

test("a statement in a bank's own columns is matched once, then uploaded again without adding twice", async ({ page }) => {
  await signIn(page);
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Money" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Money" })).toBeVisible();
  await page.getByRole("navigation", { name: "Money" }).getByRole("link", { name: "Upload statement" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Upload a statement" })).toBeVisible();

  const channel = `gtbank/e2e-${randomUUID().slice(0, 8)}`;
  await page.getByRole("combobox", { name: /^Account/ }).fill(channel);
  await page.getByLabel("Statement file (CSV)").setInputFiles(file("statement.csv", BANK_CSV));
  await page.getByRole("button", { name: "Upload statement" }).click();

  // The common format's columns are not in the file: the columns are matched on the form, guessed from their names.
  await expect(page.getByTestId("statement-refusal")).toContainText("match its columns");
  const match = page.getByTestId("column-match");
  await expect(match).toBeVisible();
  await expect(match.getByRole("combobox", { name: /^Date/ })).toHaveValue("Trans Date");
  await expect(match.getByRole("combobox", { name: /^Description/ })).toHaveValue("Narration");
  await expect(match.getByLabel("Two columns: money in and money out")).toBeChecked();
  await expect(match.getByRole("combobox", { name: /^Money in/ })).toHaveValue("Credit");
  await expect(match.getByRole("combobox", { name: /^Money out/ })).toHaveValue("Debit");
  await expect(match.getByRole("combobox", { name: /^Reference \(optional\)/ })).toHaveValue("Reference");
  // The file's own names are the choices.
  await match.getByRole("combobox", { name: /^Date/ }).selectOption("Value Date");
  await match.getByRole("combobox", { name: /^Date/ }).selectOption("Trans Date");
  await axeBoth(page, "/money/statements (matching columns)");
  await page.getByRole("button", { name: "Upload statement" }).click();
  await expect(page.getByTestId("statement-status")).toHaveText("Added 3 lines, skipped 0 already in the log.");
  await expect(page.getByTestId("column-match")).toHaveCount(0);

  // The matching is remembered for the account: the same file again adds nothing.
  await page.getByLabel("Statement file (CSV)").setInputFiles(file("statement.csv", BANK_CSV));
  await page.getByRole("button", { name: "Upload statement" }).click();
  await expect(page.getByTestId("statement-status")).toHaveText("Added 0 lines, skipped 3 already in the log.");
  const saved = await (await page.request.get(`/v1/money/statement-format?channel=${encodeURIComponent(channel)}`)).json();
  expect(saved.columns).toMatchObject({ date: "Trans Date", description: "Narration", in: "Credit", out: "Debit" });

  // A file in the common format goes in without matching, its reference column being optional.
  await page.getByRole("combobox", { name: /^Account/ }).fill(`access/e2e-${randomUUID().slice(0, 8)}`);
  await page.getByLabel("Statement file (CSV)").setInputFiles(file("common.csv", "date,description,amount\n2026-10-01,Sale,5000\n2026-10-02,Bank charge,-52.50\n"));
  await page.getByRole("button", { name: "Upload statement" }).click();
  await expect(page.getByTestId("statement-status")).toHaveText("Added 2 lines, skipped 0 already in the log.");
  await expect(page.getByTestId("column-match")).toHaveCount(0);

  // A file with lines Taskiem cannot read adds nothing and says which lines.
  await page.getByLabel("Statement file (CSV)").setInputFiles(file("common.csv", "date,description,amount\n2026-10-01,Sale,5000\nyesterday,Refund,-100\n"));
  await page.getByRole("button", { name: "Upload statement" }).click();
  const refusal = page.getByTestId("statement-refusal");
  await expect(refusal).toContainText("nothing was added");
  await expect(refusal.getByRole("list", { name: "Lines that cannot be read" })).toContainText(/Line 3: the date "yesterday"/);
  await axeBoth(page, "/money/statements (lines that cannot be read)");
});

test("an expense is entered once, and a fee rate is set", async ({ page }) => {
  await signIn(page);
  await page.goto("/money/entries");
  await expect(page.getByRole("heading", { level: 1, name: "Cash and expenses" })).toBeVisible();
  await page.getByRole("button", { name: "Add to the money log" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "Give the amount." })).toBeVisible();
  await page.getByLabel("Amount (₦)").fill("12,000");
  await page.getByLabel("Description").fill("Diesel for the generator");
  await page.getByLabel("Vendor (optional)").fill("Total");
  await page.getByLabel("Category (optional)").fill("Fuel");
  await page.getByRole("button", { name: "Add to the money log" }).click();
  await expect(page.getByTestId("entry-status")).toHaveText("Expense of ₦12,000 added to the money log.");
  await expect(page.getByLabel("Amount (₦)")).toHaveValue("");
  const n = sql("SELECT count(*) FROM money_entries WHERE kind = 'expense' AND vendor = 'Total' AND category = 'fuel' AND amount_kobo = 1200000 AND direction = 'out'");
  expect(Number(n)).toBeGreaterThanOrEqual(1);
  await axeBoth(page, "/money/entries");

  await page.getByRole("navigation", { name: "Money" }).getByRole("link", { name: "Fees" }).click();
  await expect(page.getByText("A fee above the agreed rate becomes a leak")).toBeVisible();
  await page.getByLabel("Connection").fill("paystack/main");
  await page.getByLabel("Percent of the amount").fill("1.5");
  await page.getByLabel("Flat fee (₦)").fill("100");
  await page.getByLabel("Cap (₦, optional)").fill("2,000");
  await page.getByRole("button", { name: "Save fee" }).click();
  await expect(page.getByTestId("fee-status")).toHaveText("Fee for paystack/main saved: 1.5% + ₦100, at most ₦2,000.");
  const row = page.getByTestId("fee-rate").filter({ hasText: "paystack/main" });
  await expect(row).toContainText("1.5%");
  await expect(row).toContainText("₦2,000");
  // Changing it keeps the connection and fills in what was saved.
  await row.getByRole("button", { name: "Change the fee for paystack/main" }).click();
  await expect(page.getByLabel("Percent of the amount")).toHaveValue("1.5");
  await page.getByLabel("Percent of the amount").fill("1.4");
  await page.getByRole("button", { name: "Save fee" }).click();
  await expect(page.getByTestId("fee-status")).toContainText("1.4% + ₦100");
  const rates = await (await page.request.get("/v1/money/fee-rates")).json();
  expect(rates.fee_rates).toContainEqual({ channel: "paystack/main", percent_bp: 140, flat_kobo: 10000, cap_kobo: 200000 });
  await axeBoth(page, "/money/fees");
});

test("the overview shows the period's money, and the journal downloads as CSV", async ({ page }) => {
  await signIn(page);
  // An expense through the API, on an account of its own so its row is this test's alone.
  const channel = `till-${randomUUID().slice(0, 8)}`;
  const made = await page.request.post("/v1/money/entries", {
    headers: { "X-Taskiem-Request": "1", "Idempotency-Key": randomUUID() },
    data: { kind: "expense", amount_kobo: 765400, description: "Overview e2e diesel", channel },
  });
  expect(made.status(), await made.text()).toBe(201);

  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Money" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Money" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Money" }).getByRole("link", { name: "Overview" })).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("combobox", { name: "Period" })).toHaveValue("today");
  await expect(page.getByTestId("money-out")).toContainText("Money out");
  const row = page.getByTestId("channel-row").filter({ hasText: channel });
  await expect(row).toContainText("₦7,654");
  await expect(row.locator('[data-label="Movements"]')).toHaveText("1");
  await expect(page.getByTestId("overview-leaks").getByRole("link", { name: "See the leaks" })).toHaveAttribute("href", "/money/leaks");
  await expect(page.getByTestId("exports")).toContainText("debits equal credits");
  await axeBoth(page, "/money");

  // Dates chosen by hand; dates that cannot be a period are refused on the page.
  await page.getByRole("combobox", { name: "Period" }).selectOption("custom");
  await page.getByLabel("From", { exact: true }).fill("2026-10-05");
  await page.getByLabel("To", { exact: true }).fill("2026-10-01");
  await expect(page.getByRole("alert")).toHaveText("The last day is on or after the first.");
  await expect(page.getByTestId("exports")).toHaveCount(0);
  await page.getByRole("combobox", { name: "Period" }).selectOption("today");

  // The journal downloads for the period, a CSV with the expense on it.
  const download = page.waitForEvent("download");
  await page.getByRole("link", { name: "Download accounting journal" }).click();
  const got = await download;
  expect(got.suggestedFilename()).toMatch(/^journal-\d{4}-\d{2}-\d{2}-to-\d{4}-\d{2}-\d{2}\.csv$/);
  const text = await readFile((await got.path()) as string, "utf8");
  expect(text.split("\n")[0]).toBe("date,journal_id,account,debit,credit,description,reference");
  expect(text).toContain("Overview e2e diesel");

  // Old links to the leaks still land on them.
  await page.goto("/money?status=closed");
  await expect(page).toHaveURL(/\/money\/leaks\?status=closed$/);
  await expect(page.getByRole("heading", { level: 1, name: "Leaks" })).toBeVisible();
});

test("a leak is assigned and closed as recovered", async ({ page }) => {
  await signIn(page);
  const tenant = sql("SELECT id FROM tenants WHERE name = 'E2E'");
  const id = randomUUID();
  sql(
    `INSERT INTO money_leaks (id, tenant_id, kind, subject, amount_kobo, detail, opened_at)
       VALUES ('${id}', '${tenant}', 'unmatched', 'e2e:${id}', 2500000, '₦25,000 came in on gtbank/current on 01 Oct 2026 and matches no invoice', now())`,
  );
  await page.goto("/money/leaks");
  await expect(page.getByTestId("leak-totals")).toContainText("Open leaks");
  const card = page.getByTestId("leak").filter({ hasText: "₦25,000 came in on gtbank/current" });
  await expect(card).toContainText("Money with no invoice");
  await expect(card).toContainText("Open");
  await expect(card.getByTestId("assigned")).toHaveText("No one");

  await card.getByLabel("Assign to").selectOption({ label: "Me (Owner)" });
  await card.getByRole("button", { name: "Assign" }).click();
  await expect(page.getByTestId("leak-status")).toHaveText("Money with no invoice assigned to you.");
  await expect(card.getByTestId("assigned")).toHaveText("you");

  await card.getByRole("button", { name: "Close…" }).click();
  await card.getByRole("button", { name: "Close leak" }).click();
  await expect(card.getByRole("alert")).toHaveText("Choose how it was closed.");
  await card.getByLabel(/^Recovered/).check();
  await card.getByLabel("Amount recovered (₦)").fill("20,000");
  await card.getByLabel("Note (optional)").fill("The customer paid it back.");
  await axeBoth(page, "/money/leaks (closing a leak)");
  const before = await (await page.request.get("/v1/money/leaks")).json();
  await card.getByRole("button", { name: "Close leak" }).click();
  await expect(page.getByTestId("leak-status")).toHaveText("Money with no invoice closed: recovered ₦20,000.");
  await expect(card).toHaveCount(0);
  const after = await (await page.request.get("/v1/money/leaks")).json();
  expect(after.totals.recovered_kobo - before.totals.recovered_kobo).toBe(2000000);
  expect(after.totals.open).toBe(before.totals.open - 1);

  // Closed leaks say how they were closed, by whom, and the amount.
  await page.getByLabel("Show").selectOption("closed");
  const closed = page.getByTestId("leak").filter({ hasText: "₦25,000 came in on gtbank/current" });
  await expect(closed.getByTestId("closed-how")).toContainText("Recovered ₦20,000, closed by you");
  await expect(closed).toContainText("The customer paid it back.");
  await expect(closed).toContainText("Recovered");
  await expect(closed.getByRole("button")).toHaveCount(0);
  await axeBoth(page, "/money/leaks?status=closed");
});
