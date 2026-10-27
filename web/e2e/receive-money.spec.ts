import { createServer, type Server } from "node:http";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Receive money with Taskiem (decision 0052; docs/receive-money.md): a
// business with no payments account turns it on from Get started, gets an
// account number of its own under Taskiem's iswallet client, sees it on
// Money with its balance, and withdraws to its bank under an approval.
// iswallet is a fake on the port serve.sh points TASKIEM_ISWALLET_TEST_URL
// at, answering only Taskiem's own (test) key.
test.use({ bypassCSP: true }); // axe is injected into the page

const port = 12829;
let fake: Server;
let wallets = 0;
const sentBVN: string[] = [];

test.beforeAll(async () => {
  fake = createServer((req, res) => {
    let body = "";
    req.on("data", (c: Buffer) => (body += c.toString("utf8")));
    req.on("end", () => {
      const json = (status: number, v: unknown) => {
        res.writeHead(status, { "Content-Type": "application/json", "X-Request-ID": "req_e2e" });
        res.end(JSON.stringify(v));
      };
      if (req.headers.authorization !== "Bearer isw_platform_e2e") return json(401, { error: { code: "UNAUTHORIZED", message: "bad key" } });
      const url = req.url ?? "";
      if (req.method === "POST" && url === "/v1/wallets") {
        const w = JSON.parse(body) as { bvn?: string };
        if (!req.headers["idempotency-key"]) return json(400, { error: { code: "MISSING_IDEMPOTENCY_KEY", message: "Idempotency-Key is required" } });
        sentBVN.push(w.bvn ?? "");
        wallets++;
        return json(201, { wallet_id: `wal_rm_${wallets}`, internal_reference: "6256638601", tier: "TIER_1" });
      }
      const m = url.match(/^\/v1\/wallets\/(wal_rm_\d+)\/(virtual-account|balance)$/);
      if (m && m[2] === "virtual-account") {
        const a = JSON.parse(body) as { reference: string; account_name?: string };
        return json(201, { reference: a.reference, account_number: "8800000001", bank_name: "Wema Bank", account_name: a.account_name ?? "", status: "active" });
      }
      if (m && m[2] === "balance") return json(200, { wallet_id: m[1], balances: [{ currency: "NGN", total: 7500000, available: 7000000, pending: 500000 }] });
      if (url === "/v1/banks") return json(200, { banks: [{ code: "058", name: "GTBank" }, { code: "044", name: "Access Bank" }] });
      if (req.method === "POST" && url === "/v1/name-enquiry") {
        const q = JSON.parse(body) as { account_number: string; bank_code: string };
        return json(200, { account_name: "ADA OBI STORES", account_number: q.account_number, bank_code: q.bank_code });
      }
      json(404, { error: { code: "NOT_FOUND", message: url } });
    });
  });
  await new Promise<void>((resolve) => fake.listen(port, "127.0.0.1", resolve));
});

test.afterAll(() => new Promise<void>((resolve) => fake.close(() => resolve())));

const H = { "X-Taskiem-Request": "1" };

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: "Ada Obi Stores", email: `ada-${Date.now()}@obi.test`, name: "Ada", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

test("a business with no payments account receives money with Taskiem and withdraws to its bank", async ({ page }) => {
  test.setTimeout(120_000);
  await signUp(page);

  // Get started offers it to a business with no payments account.
  await page.goto("/start");
  const offer = page.getByTestId("receive-offer");
  await expect(offer).toContainText("An account number of your own");
  await offer.getByRole("link", { name: "Turn it on" }).click();
  await expect(page).toHaveURL(/\/settings\/payments$/);
  await axeBoth(page, "/settings/payments (off)");

  // Where the money is held, the email and the BVN, sent once.
  const off = page.getByTestId("receive-money-off");
  await expect(off.getByRole("radio", { name: /Only my own money/ })).toBeChecked();
  await off.getByLabel("Your business email").fill("ada@obi.test");
  await off.getByLabel("The director's BVN").fill("2221234567x8");
  await expect(off.getByLabel("The director's BVN")).toHaveValue("22212345678");
  await off.getByRole("button", { name: "Turn it on" }).click();
  await expect(page.getByRole("status").filter({ hasText: "You can receive money now" })).toBeVisible();
  const hero = page.getByTestId("receive-hero");
  await expect(hero.getByTestId("receive-number")).toHaveText("8800 000 001");
  await expect(hero).toContainText("Wema Bank");
  await expect(hero).toContainText("Practice");
  await expect(hero.getByTestId("receive-balance")).toHaveText("₦70,000");
  await expect(hero).toContainText("Held with others, counted as yours");
  expect(sentBVN).toEqual(["22212345678"]);
  await expect(page.locator("body")).not.toContainText("22212345678");
  await expect(page.locator("body")).not.toContainText("6256638601"); // the wallet's own handle, which nobody can pay into

  // Where withdrawals go: the bank names the account; two owners from ₦500,000.
  const w = page.getByTestId("withdrawals");
  const form = w.getByTestId("withdraw-to-form");
  await form.getByRole("combobox", { name: /^Your bank/ }).selectOption({ label: "GTBank" });
  await form.getByLabel("Your account number").fill("0123456789");
  // Two owners need two owners: this business has one.
  await form.getByLabel("Two owners approve from (₦, optional)").fill("500000");
  await form.getByRole("button", { name: "Save" }).click();
  await expect(form.getByRole("alert")).toContainText("one owner");
  await form.getByLabel("Two owners approve from (₦, optional)").fill("");
  await form.getByRole("button", { name: "Save" }).click();
  const to = w.getByTestId("withdraw-to");
  await expect(to).toContainText("ADA OBI STORES");
  await expect(to).toContainText("GTBank · 0123 456 789");
  await expect(to).toContainText("An owner approves each withdrawal.");
  await axeBoth(page, "/settings/payments (on)");

  // A withdrawal waits for approval.
  const wd = w.getByTestId("withdraw");
  await wd.getByLabel("Amount to withdraw (₦)").fill("90000");
  await expect(wd).toContainText("Your wallet holds ₦70,000: withdraw that much or less.");
  await expect(wd.getByRole("button", { name: "Withdraw" })).toBeDisabled();
  await wd.getByLabel("Amount to withdraw (₦)").fill("15000");
  await wd.getByRole("button", { name: "Withdraw" }).click();
  await expect(wd.getByTestId("withdraw-started")).toContainText("Nothing is paid until it is approved");

  // Money shows the number; Connections says Taskiem keeps the account.
  await page.goto("/money");
  await expect(page.getByTestId("receive-card-number")).toHaveText("8800 000 001");
  await page.goto("/connections");
  const conn = page.getByTestId("connection").filter({ hasText: "Your wallet with Taskiem" });
  await expect(conn).toContainText("kept by Taskiem");
  await expect(conn.getByRole("button", { name: "Disconnect" })).toHaveCount(0);

  // Once on, Get started offers it no more.
  await page.goto("/start");
  await expect(page.getByTestId("onboarding-progress")).toBeVisible();
  await expect(page.getByTestId("receive-offer")).toHaveCount(0);
  expect(wallets).toBe(1);
});
