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
        res.writeHead(status, {
          "Content-Type": "application/json",
          "X-Request-ID": "req_e2e",
        });
        res.end(JSON.stringify(v));
      };
      if (req.headers.authorization !== "Bearer isw_platform_e2e")
        return json(401, {
          error: { code: "UNAUTHORIZED", message: "bad key" },
        });
      const url = req.url ?? "";
      if (req.method === "POST" && url === "/v1/wallets") {
        const w = JSON.parse(body) as { bvn?: string };
        if (!req.headers["idempotency-key"])
          return json(400, {
            error: {
              code: "MISSING_IDEMPOTENCY_KEY",
              message: "Idempotency-Key is required",
            },
          });
        sentBVN.push(w.bvn ?? "");
        wallets++;
        return json(201, {
          wallet_id: `wal_rm_${wallets}`,
          internal_reference: "6256638601",
          tier: "TIER_1",
        });
      }
      const m = url.match(
        /^\/v1\/wallets\/(wal_rm_\d+)\/(virtual-account|balance)$/,
      );
      if (m && m[2] === "virtual-account") {
        const a = JSON.parse(body) as {
          reference: string;
          account_name?: string;
        };
        return json(201, {
          reference: a.reference,
          account_number: "8800000001",
          bank_name: "Wema Bank",
          account_name: a.account_name ?? "",
          status: "active",
        });
      }
      if (m && m[2] === "balance")
        return json(200, {
          wallet_id: m[1],
          balances: [
            {
              currency: "NGN",
              total: 7500000,
              available: 7000000,
              pending: 500000,
            },
          ],
        });
      if (url === "/v1/banks")
        return json(200, {
          banks: [
            { code: "058", name: "GTBank" },
            { code: "044", name: "Access Bank" },
            // The bank's full name, as a saved account chosen by name
            // before the wallet was open names it (saved.spec below).
            { code: "000013", name: "Guaranty Trust Bank" },
            // Enough banks that the Saved form offers a search.
            { code: "000015", name: "Zenith Bank" },
            { code: "000016", name: "First Bank of Nigeria" },
            { code: "000003", name: "First City Monument Bank" },
            { code: "000004", name: "United Bank for Africa" },
            { code: "090267", name: "Kuda Microfinance Bank" },
            { code: "000017", name: "Wema Bank" },
          ],
        });
      if (req.method === "POST" && url === "/v1/name-enquiry") {
        const q = JSON.parse(body) as {
          account_number: string;
          bank_code: string;
        };
        return json(200, {
          account_name: "ADA OBI STORES",
          account_number: q.account_number,
          bank_code: q.bank_code,
        });
      }
      json(404, { error: { code: "NOT_FOUND", message: url } });
    });
  });
  await new Promise<void>((resolve) => fake.listen(port, "127.0.0.1", resolve));
});

test.afterAll(
  () => new Promise<void>((resolve) => fake.close(() => resolve())),
);

const H = { "X-Taskiem-Request": "1" };

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: "Ada Obi Stores",
      email: `ada-${Date.now()}@obi.test`,
      name: "Ada",
      password: "correct horse battery",
    },
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

test("a business with no payments account receives money with Taskiem and withdraws to its bank", async ({
  page,
}) => {
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
  await expect(
    off.getByRole("radio", { name: /Only my own money/ }),
  ).toBeChecked();
  await off.getByLabel("Your business email").fill("ada@obi.test");
  await off.getByLabel("The director's BVN").fill("2221234567x8");
  await expect(off.getByLabel("The director's BVN")).toHaveValue("22212345678");
  await off.getByRole("button", { name: "Turn it on" }).click();
  await expect(
    page.getByRole("status").filter({ hasText: "You can receive money now" }),
  ).toBeVisible();
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
  await form
    .getByRole("combobox", { name: /^Your bank/ })
    .selectOption({ label: "GTBank" });
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
  await expect(wd).toContainText(
    "Your wallet holds ₦70,000: withdraw that much or less.",
  );
  await expect(wd.getByRole("button", { name: "Withdraw" })).toBeDisabled();
  await wd.getByLabel("Amount to withdraw (₦)").fill("15000");
  await wd.getByRole("button", { name: "Withdraw" }).click();
  await expect(wd.getByTestId("withdraw-started")).toContainText(
    "Nothing is paid until it is approved",
  );

  // Sending money to the bank automatically: weekly, no approval, only to
  // the saved account; then off again.
  const sw = page.getByTestId("sweep");
  await expect(sw.getByTestId("sweep-line")).toContainText("Off");
  await sw.getByRole("button", { name: "Turn on" }).click();
  await sw.getByLabel("Every week").check();
  await sw.getByLabel("Day").selectOption("friday");
  await sw.getByLabel("Time (Lagos)").fill("17:00");
  await sw.getByLabel("Keep in my wallet (₦, optional)").fill("20000");
  await sw.getByRole("button", { name: "Save" }).click();
  await expect(sw.getByTestId("sweep-line")).toHaveText(
    "Every Friday at 17:00, what your wallet holds goes to your bank, keeping ₦20,000 in your wallet.",
  );
  await expect(sw).toContainText(
    "Nobody approves it: it only ever pays your saved bank account",
  );
  await sw.getByRole("button", { name: "Change" }).click();
  await sw.getByLabel("Off: I withdraw when I choose").check();
  await sw.getByRole("button", { name: "Save" }).click();
  await expect(sw.getByTestId("sweep-line")).toContainText(
    "Off: your money stays in your wallet",
  );

  // Money shows the number; Connections says Taskiem keeps the account.
  await page.goto("/money");
  await expect(page.getByTestId("receive-card-number")).toHaveText(
    "8800 000 001",
  );
  await page.goto("/connections");
  const conn = page
    .getByTestId("connection")
    .filter({ hasText: "Your wallet with Taskiem" });
  await expect(conn).toContainText("kept by Taskiem");
  await expect(conn.getByRole("button", { name: "Disconnect" })).toHaveCount(0);

  // Once on, Get started offers it no more.
  await page.goto("/start");
  await expect(page.getByTestId("onboarding-progress")).toBeVisible();
  await expect(page.getByTestId("receive-offer")).toHaveCount(0);
  expect(wallets).toBe(1);
});

// Saved accounts, S0 (docs/spec/saved-objects.md; build plan S0's "done
// when"): an owner with no payments account saves a supplier's account as
// @bayo, kept "Name not checked yet"; opens a Taskiem wallet from the
// Saved page's step to connect; back on Saved, the bank's name is checked
// through the wallet, and a new account shows the bank's name before it is
// saved. The number is never on the page in full. It shares this file's
// fake iswallet (one port).
test("an owner saves @bayo, opens a Taskiem wallet, and the bank's name is checked", async ({
  page,
}) => {
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 390, height: 844 });
  await signUp(page);

  await page.goto("/settings");
  await page.getByRole("link", { name: "Open saved accounts" }).click();
  await expect(page).toHaveURL(/\/settings\/saved$/);
  const connect = page.getByTestId("saved-connect");
  await expect(connect).toContainText("Check names with the bank");
  const saved = page.getByTestId("saved");
  await expect(saved).toContainText("No saved accounts yet.");
  await axeBoth(page, "/settings/saved (no payments account)");

  // Saved with no payments account: kept, not checked yet, as the form says.
  await saved.getByRole("button", { name: "Save an account" }).click();
  let form = page.getByTestId("saved-form");
  await expect(form.getByTestId("saved-form-connect")).toContainText("not checked until your business connects a payments account");
  await form.getByLabel("Name to call it up by").fill("@Bayo");
  await form.getByLabel("Title").fill("Bayo (rice)");
  await form.getByTestId("saved-bank").selectOption({ label: "Guaranty Trust Bank" });
  await form.getByLabel("Account number").fill("0123456789");
  await form.getByRole("button", { name: "Save account" }).click();
  let row = saved.getByTestId("saved-row").filter({ hasText: "Bayo (rice)" });
  await expect(row).toContainText("@bayo · GTBank ••6789");
  await expect(row.getByTestId("saved-check")).toHaveText("Name not checked yet");
  await expect(page.locator("body")).not.toContainText("0123456789");
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);

  // The step to connect leads to opening a Taskiem wallet.
  await connect.getByRole("link", { name: "Open a Taskiem wallet" }).click();
  await expect(page).toHaveURL(/\/settings\/payments$/);
  const off = page.getByTestId("receive-money-off");
  await off.getByLabel("Your business email").fill("ada@obi.test");
  await off.getByLabel("The director's BVN").fill("22212345678");
  await off.getByRole("button", { name: "Turn it on" }).click();
  await expect(page.getByTestId("receive-hero")).toBeVisible();

  // Back on Saved: no step to connect; @bayo is checked through the wallet.
  await page.goto("/settings/saved");
  await expect(saved).toContainText("Names are checked with the bank through your Taskiem wallet.");
  await expect(page.getByTestId("saved-connect")).toHaveCount(0);
  row = saved.getByTestId("saved-row").filter({ hasText: "Bayo (rice)" });
  await expect(row.getByTestId("saved-check")).toHaveText("Bank's name: ADA OBI STORES");
  await axeBoth(page, "/settings/saved (checked)");

  // A new account shows the bank's name before it is saved.
  await saved.getByRole("button", { name: "Save an account" }).click();
  form = page.getByTestId("saved-form");
  await expect(form.getByTestId("saved-form-connect")).toHaveCount(0);
  await form.getByLabel("Name to call it up by").fill("landlord");
  await form.getByLabel("Title").fill("Landlord");
  // The wallet lists many banks: a search narrows them.
  await form.getByTestId("saved-bank-search").fill("acc");
  await expect(form.getByTestId("saved-bank").locator("option")).toHaveText(["Choose the bank", "Access Bank"]);
  await form.getByTestId("saved-bank").selectOption({ label: "Access Bank" });
  await form.getByLabel("Account number").fill("1111 111 111");
  await expect(form.getByTestId("saved-bank-name")).toContainText("ADA OBI STORES");
  await form.getByRole("button", { name: "Save account" }).click();
  await expect(saved.getByTestId("saved-row").filter({ hasText: "Landlord" }).getByTestId("saved-check")).toHaveText("Bank's name: ADA OBI STORES");

  // Changing the account asks for a passkey, which this owner has not added yet.
  row = saved.getByTestId("saved-row").filter({ hasText: "Bayo (rice)" });
  await row.getByRole("button", { name: "Change @bayo" }).click();
  form = page.getByTestId("saved-form");
  await expect(form.getByTestId("saved-bank")).toHaveValue("c:000013");
  await form.getByLabel("Account number").fill("0123456780");
  await expect(form).toContainText("Changing the account asks for your passkey.");
  await form.getByRole("button", { name: "Save changes" }).click();
  await expect(form).toContainText("Add a passkey first");
  await form.getByRole("button", { name: "Cancel" }).click();
  await expect(row).toContainText("GTBank ••6789");
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});
