import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// A customer's own account number from an invoice (docs/matching.md
// "Customers' own account numbers"; docs/invoices.md "Matching"): on
// "The customer's money", "Give this customer an account number"
// registers an account the business already has for the customer, typed
// in by hand (the e2e server reaches no payments provider, so issuing one
// is not what is tested), and the account and its reference show on the
// panel of every invoice of the customer's.
test.use({ bypassCSP: true }); // axe is injected into the page

const H = { "X-Taskiem-Request": "1" };

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

/** An invoice for ₦25,000, made in the API and marked sent in the database. */
async function sentInvoice(page: Page, customer: { name: string; phone: string }): Promise<{ id: string; number: string }> {
  const r = await page.request.post("/v1/invoices", {
    headers: { ...H, "Idempotency-Key": `e2e:${randomUUID()}` },
    data: { customer, amount_kobo: 2500000, description: "Catering" },
  });
  expect(r.ok(), await r.text()).toBeTruthy();
  const x = (await r.json()) as { id: string; number: string };
  sql(`UPDATE customer_invoices SET sent_at = now(), updated_at = now() WHERE id = '${x.id}'`);
  return x;
}

test("a customer is given an account number from their invoice, and it shows on the panel", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page);
  const ngozi = { name: "Ngozi Eze", phone: "+2348031230009" };
  const first = await sentInvoice(page, ngozi);
  const second = await sentInvoice(page, ngozi);
  const number = `99${String(Date.now()).slice(-8)}`;

  await page.goto(`/invoices/${first.number}`);
  const money = page.getByTestId("customer-money");
  await expect(money).toContainText("None. Money into an account number of the customer's own");
  await money.getByRole("button", { name: "Give this customer an account number" }).click();
  const form = money.getByRole("form", { name: "Give this customer an account number" });
  await expect(form.getByRole("button", { name: /Register this account|Issue the account number/ })).toBeVisible();
  // Another test may have connected a payments provider that can issue
  // account numbers: then the form offers it first, and by hand is a choice.
  const where = form.getByRole("combobox", { name: "Where from" });
  if (await where.count()) {
    await expect(form).toContainText("Issue one with");
    await where.selectOption("by_hand");
  } else {
    await expect(form).toContainText("None of your connections can issue account numbers");
  }

  // Nothing is sent without the connection, number and reference.
  await form.getByRole("button", { name: "Register this account" }).click();
  await expect(form.getByRole("alert")).toContainText("Name the connection the account is on, like paystack/main.");
  await form.getByLabel("Connection").fill("paystack/main");
  await form.getByLabel("Account number").fill(number);
  await form.getByRole("button", { name: "Register this account" }).click();
  await expect(form.getByRole("alert")).toContainText("The account's reference is required");
  await form.getByLabel("Reference").fill(`CUS_e2e_${number}`);
  await form.getByLabel("Bank").fill("Titan Trust Bank");
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`/invoices/${first.number} giving an account number (${scheme})`, vs)).toEqual([]);
  }
  await form.getByRole("button", { name: "Register this account" }).click();
  await expect(page.locator(".toast").filter({ hasText: "Account number registered" })).toBeVisible();

  // The panel lists it with its reference, and the form is closed.
  const accounts = money.getByTestId("customer-account");
  await expect(accounts).toHaveCount(1);
  await expect(accounts.first()).toHaveText(`${number} · Titan Trust Bank (paystack/main), reference CUS_e2e_${number}`);
  await expect(form).toHaveCount(0);
  expect(sql(`SELECT account_number || ' ' || channel FROM customer_accounts WHERE reference = 'CUS_e2e_${number}'`)).toBe(`${number} paystack/main`);

  // The customer's other invoice shows the same account.
  await page.goto(`/invoices/${second.number}`);
  await expect(page.getByTestId("customer-money").getByTestId("customer-account")).toHaveText(new RegExp(`^${number} `));

  // Another customer cannot be given the same account.
  const other = await sentInvoice(page, { name: "Bola Ade", phone: "+2348031230010" });
  await page.goto(`/invoices/${other.number}`);
  const otherMoney = page.getByTestId("customer-money");
  await otherMoney.getByRole("button", { name: "Give this customer an account number" }).click();
  const otherForm = otherMoney.getByRole("form", { name: "Give this customer an account number" });
  const otherWhere = otherForm.getByRole("combobox", { name: "Where from" });
  if (await otherWhere.count()) await otherWhere.selectOption("by_hand");
  await otherForm.getByLabel("Connection").fill("paystack/main");
  await otherForm.getByLabel("Account number").fill(number);
  await otherForm.getByLabel("Reference").fill(`CUS_e2e_${number}`);
  await otherForm.getByRole("button", { name: "Register this account" }).click();
  await expect(otherForm).toContainText("That account is already another customer's.");
  await expect(otherMoney.getByTestId("customer-account")).toHaveCount(0);
});
