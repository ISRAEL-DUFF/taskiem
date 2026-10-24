import { execFileSync } from "node:child_process";
import { createServer, type Server } from "node:http";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Students' account numbers (docs/spec/schools.md B4; docs/schools.md
// "Account numbers"): the owner chooses the issuing connection, a batch is
// planned, confirmed and followed to the end with the students it could
// not issue and why, a student's page shows their number with a copy
// button, and a payment into it, found only by the look-back, pays the
// student's invoice. iswallet is a fake on the port serve.sh points
// TASKIEM_ISWALLET_URL at; it opens one account per reference, as iswallet
// does, and refuses anyone named Refuse as a wallet without KYC would.
// The school is a business of its own, so no other test's students are in
// its batch.
test.use({ bypassCSP: true }); // axe is injected into the page

const port = 12828;
const accounts = new Map<string, { account_number: string; account_name: string }>();
const credits: unknown[] = [];
let issued = 0;
let fake: Server;

test.beforeAll(async () => {
  fake = createServer((req, res) => {
    let body = "";
    req.on("data", (c: Buffer) => (body += c.toString("utf8")));
    req.on("end", () => {
      const json = (status: number, v: unknown) => {
        res.writeHead(status, { "Content-Type": "application/json" });
        res.end(JSON.stringify(v));
      };
      if (req.headers.authorization !== "Bearer isw_e2e_key") return json(401, { error: { code: "UNAUTHORIZED", message: "bad key" } });
      if (req.method === "POST" && req.url === "/v1/wallets/wal_e2e/virtual-accounts/batch") {
        const items = (JSON.parse(body) as { accounts: { reference: string; account_name?: string }[] }).accounts;
        const out = items.map((a) => {
          if ((a.account_name ?? "").includes("REFUSE"))
            return { reference: a.reference, status: "failed", error_code: "KYC_REQUIRED", error_message: "the director's BVN is missing" };
          let x = accounts.get(a.reference);
          if (!x) {
            issued++;
            x = { account_number: `98${String(accounts.size + 1).padStart(8, "0")}`, account_name: a.account_name ?? "" };
            accounts.set(a.reference, x);
          }
          return { reference: a.reference, status: "created", bank_name: "Wema Bank", ...x };
        });
        return json(200, { wallet_id: "wal_e2e", accounts: out });
      }
      if (req.method === "GET" && (req.url ?? "").startsWith("/v1/wallets/wal_e2e/transactions")) {
        return json(200, { transactions: credits, pagination: { has_more: false } });
      }
      json(404, { error: { code: "NOT_FOUND", message: req.url } });
    });
  });
  await new Promise<void>((resolve) => fake.listen(port, "127.0.0.1", resolve));
});

test.afterAll(() => new Promise<void>((resolve) => fake.close(() => resolve())));

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

const H = { "X-Taskiem-Request": "1" };

/** A school of its own (its students are no other test's), signed up and with Schools on. */
async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: "Okafor Academy", email: `bursar-${Date.now()}@okafor.test`, name: "Owner", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
  const on = await page.request.put("/v1/settings/schools", { headers: H, data: { enabled: true } });
  expect(on.ok(), await on.text()).toBeTruthy();
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

const STUDENTS = [
  "Name,Admission number,Class,Guardian name,Phone,Email",
  "Ada Okafor,ACC/001,Primary 6 B4,Uche Okafor,08035550001,uche@example.com",
  "Kemi Refuse,ACC/002,Primary 6 B4,Tola Refuse,08035550002,",
].join("\n");

test("students are issued their own account numbers once, and a payment into one pays their invoice", async ({ page }) => {
  test.setTimeout(240_000);
  await signUp(page);
  // An iswallet connection in prod, a class and two students.
  const c = await page.request.post("/v1/connections", {
    headers: H,
    data: { environment: "prod", connector: "iswallet@1", name: "school_e2e", credentials: { api_key: "isw_e2e_key" } },
  });
  expect(c.ok() || c.status() === 409, `connection: ${c.status()} ${await c.text()}`).toBeTruthy();
  const cl = await page.request.post("/v1/schools/classes", { headers: H, data: { name: "Primary 6", arm: "B4", order: 60 } });
  expect(cl.ok() || cl.status() === 409, await cl.text()).toBeTruthy();
  const plan = await page.request.post("/v1/schools/students/import", { headers: H, data: { csv: STUDENTS } });
  expect(plan.ok(), await plan.text()).toBeTruthy();
  const saved = await page.request.post("/v1/schools/students/import", { headers: H, data: { csv: STUDENTS, confirm: ((await plan.json()) as { hash: string }).hash } });
  expect(saved.ok(), await saved.text()).toBeTruthy();

  // The owner chooses the issuing connection and its wallet.
  await page.goto("/settings/schools");
  const setting = page.locator("section", { has: page.getByRole("heading", { name: "Students' account numbers" }) });
  await expect(setting.getByTestId("issuers")).toContainText("iswallet (iswallet/school_e2e)");
  await expect(setting.getByTestId("issuers")).toContainText("director's BVN");
  await setting.getByLabel("Issuing connection").selectOption("iswallet/school_e2e");
  await expect(setting.getByRole("alert")).toHaveText("iswallet needs the wallet the numbers pay into.");
  await setting.getByLabel("Wallet the numbers pay into").fill("wal_e2e");
  await setting.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("Account number settings saved")).toBeVisible();
  await axeBoth(page, "/settings/schools (account numbers)");

  // Plan, confirm, follow. Other tests add students meanwhile: a plan
  // that changed is checked again.
  await page.goto("/schools/accounts");
  await expect(page.getByRole("heading", { level: 1, name: "Account numbers" })).toBeVisible();
  const job = page.getByTestId("accounts-job");
  for (let tries = 0; tries < 3; tries++) {
    await page.getByRole("button", { name: /^(Check who gets one|Check again)$/ }).click();
    const issue = page.getByTestId("accounts-plan").getByRole("button", { name: /^Issue \d+ account numbers?$/ });
    await expect(issue).toBeVisible();
    await issue.click();
    if (await job.waitFor({ timeout: 10_000 }).then(() => true, () => false)) break;
  }
  await expect(job.getByRole("status")).toContainText(/^Done: \d+ accounts? issued of \d+\./, { timeout: 90_000 });
  const refused = page.getByTestId("accounts-problem").filter({ hasText: "ACC/002" });
  await expect(refused).toContainText("KYC_REQUIRED");
  await expect(refused).toContainText("Not issued");
  await axeBoth(page, "/schools/accounts (a batch done)");

  // Checked again: Ada has hers, and nothing is issued twice.
  const before = issued;
  const again = await page.request.post("/v1/schools/accounts/issue", { headers: H, data: {} });
  const p2 = ((await again.json()) as { plan: { already_issued: number } }).plan;
  expect(p2.already_issued).toBeGreaterThan(0);
  const ada = sql("SELECT id FROM school_students WHERE admission_number = 'ACC/001'");
  const one = await page.request.post(`/v1/schools/students/${ada}/account`, { headers: H, data: {} });
  expect(one.status(), await one.text()).toBe(200);
  expect(issued).toBe(before);

  // Her page: the number, a copy button, the name masked.
  await page.goto(`/schools/students/${ada}`);
  const panel = page.locator("section", { has: page.getByRole("heading", { name: "Account number" }) });
  const number = ((await one.json()) as { account_number: string }).account_number;
  await expect(panel.getByTestId("student-account-number")).toContainText(`${number.slice(0, 4)} ${number.slice(4, 7)} ${number.slice(7)}`);
  await expect(panel.getByRole("button", { name: `Copy the account number ${number}` })).toBeVisible();
  await expect(panel).toContainText("names the account itself");
  await expect(panel).toContainText("O•••");
  await axeBoth(page, "/schools/students/:id (an account number)");

  // An invoice of hers, sent; a payment into her number, which only the
  // look-back finds (iswallet's credit webhook is not mapped), pays it.
  const inv = await page.request.post("/v1/invoices", {
    headers: { ...H, "Idempotency-Key": `e2e:acc:${Date.now()}` },
    data: { customer: { name: "Uche Okafor", phone: "+2348035550001", key: `student:${ada}` }, amount_kobo: 2500000, description: "Fees" },
  });
  expect(inv.ok(), await inv.text()).toBeTruthy();
  const invoice = (await inv.json()) as { id: string };
  sql(`UPDATE customer_invoices SET sent_at = now(), updated_at = now() WHERE id = '${invoice.id}'`);
  const reference = [...accounts.entries()].find(([, a]) => a.account_number === number)?.[0];
  expect(reference).toBeTruthy();
  credits.push({
    provider_reference: `PRV-${Date.now()}`,
    amount_credited: 2465000,
    fee_charged: 35000,
    currency: "NGN",
    narration: "School fees",
    occurred_at: new Date().toISOString(),
    counterparty: { name: "UCHE OKAFOR", account_number: "0123456789", bank_name: "GTBank" },
    virtual_account: { reference, account_number: number },
  });
  sql("UPDATE money_lookbacks SET last_run_at = NULL WHERE channel = 'iswallet/school_e2e'");
  await expect
    .poll(async () => ((await (await page.request.get(`/v1/invoices/${invoice.id}`)).json()) as { invoice: { status: string } }).invoice.status, { timeout: 120_000, intervals: [2_000] })
    .toBe("paid");
});
