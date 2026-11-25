import { execFileSync } from "node:child_process";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Forms that sell L2 (docs/spec/forms-that-sell.md §5.4, §11): an owner
// adds a One product front from a product of the catalogue, with its
// packages; sets its promise line and a feature chip; turns its order
// form and the front on; and the page in the video is on the public host:
// the promise, the headline, the chips, the form with its packages, and
// the price bar. A buyer orders two from it. Every screen passes axe in
// both themes and fits 390 and 360 px. The public host answers *.e2e.test
// on its own port (serve.sh); Chromium maps those names to this machine.

const H = { "X-Taskiem-Request": "1" };
const PASSWORD = "correct horse battery";
const PUBLIC_PORT = process.env.E2E_PUBLIC_PORT ?? "18082";
const DB = process.env.E2E_DB_NAME ?? "taskiem_e2e";

test.use({
  bypassCSP: true,
  launchOptions: { args: ["--host-resolver-rules=MAP *.e2e.test 127.0.0.1"] },
});

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync(
    "psql",
    [base.replace(/\/[^/?]*(\?.*)?$/, `/${DB}$1`), "-v", "ON_ERROR_STOP=1", "-tAqc", text],
    { encoding: "utf8" },
  ).trim();
}

/** Checks the page in both themes at both widths, leaving it light at 390. */
async function check(page: Page, where: string, allow: string[] = []) {
  const found: string[] = [];
  for (const scheme of ["dark", "light"] as const)
    for (const width of [360, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const vs = (await axe(page)).filter((v) => !allow.includes(v.id));
      if (vs.length > 0) found.push(describe(`${where}, ${scheme}, ${width} px`, vs));
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
        `${where}, ${scheme}, ${width} px`,
      ).toBeLessThanOrEqual(width);
      // SCREENSHOT_DIR=... keeps a picture of each, for review.
      if (process.env.SCREENSHOT_DIR)
        await page.screenshot({
          path: `${process.env.SCREENSHOT_DIR}/${where.replace(/[^a-z0-9]+/gi, "-")}-${scheme}-${width}.png`,
          fullPage: true,
        });
    }
  expect(found, found.join("\n")).toEqual([]);
}

const TABLE = "0192e2e0-0000-7000-8000-0000000000a2";

/** A business with a published page whose catalogue is its own products
 * table, holding the Smart Ear Cleaner; its order form delivers to two
 * areas and takes payment at the door. */
async function seller(page: Page): Promise<{ slug: string; tenant: string; table: string }> {
  const n = `${Date.now()}${Math.floor(Math.random() * 1e4)}`;
  const email = `one-${n}@one.test`;
  const slug = `ear-${n}`;
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: `Ear Shop ${n}`, email, name: "Owner", password: PASSWORD, accept_terms: true },
  });
  expect(r.status(), await r.text()).toBe(201);
  sql(`UPDATE users SET email_verified_at = now() WHERE lower(email) = lower('${email}')`);
  const tenant = ((await (await page.request.get("/v1/me", { headers: H })).json()) as { tenant_id: string }).tenant_id;
  sql(
    `INSERT INTO tenant_verifications (id, tenant_id, level, method, actor) VALUES (gen_random_uuid(), '${tenant}', 'phone', 'test', 'test'), (gen_random_uuid(), '${tenant}', 'business', 'test', 'test')`,
  );
  expect(
    (await page.request.put("/v1/storefront", { headers: H, data: { slug, draft: { name: "Ear Shop" } } })).ok(),
  ).toBeTruthy();
  const a = await page.request.put("/v1/storefront/actions/order", {
    headers: H,
    data: {
      enabled: false,
      payment: "after_accept",
      options: { delivery: true, pay_on_delivery: true, zones: [{ name: "Lekki", fee_kobo: 150000 }, { name: "Yaba", fee_kobo: 0 }] },
    },
  });
  expect(a.ok(), await a.text()).toBeTruthy();
  await publish(page);
  // The catalogue's own table: its record only (no Xenibase serves this
  // test server's rows, and a publish would read them), with the page's
  // copy of it written here; turning the front on publishes again without
  // reading it.
  const table = TABLE.slice(0, -4) + n.slice(-4).padStart(4, "0");
  sql(
    `INSERT INTO data_tables (id, tenant_id, name, title, created_by) VALUES ('${table}', '${tenant}', 'products', 'Products', 'user:test')`,
  );
  sql(
    `UPDATE storefronts SET catalogue_table = '${table}', catalogue_columns = '{"name":"name","price":"price","price_unit":"naira","photo":"photo","available":"available","description":"description"}' WHERE tenant_id = '${tenant}'`,
  );
  sql(
    `INSERT INTO storefront_catalogue (storefront_id, table_name, row_id, tenant_id, position, item) SELECT id, 'products', 'ear', tenant_id, 0, ` +
      `'{"id":"ear","name":"Smart Ear Cleaner","price_kobo":2500000,"available":true,"description":"A camera on a soft tip: see what you clean."}'::jsonb ` +
      `FROM storefronts WHERE tenant_id = '${tenant}' ON CONFLICT DO NOTHING`,
  );
  return { slug, tenant, table };
}

/** Publishes the page, its catalogue read from its products. */
async function publish(page: Page) {
  const p = await page.request.post("/v1/storefront/publish", { headers: H });
  expect(p.ok(), await p.text()).toBeTruthy();
}

test("an owner makes a One product page from a product and a buyer orders two from it", async ({ page }) => {
  const { slug, tenant } = await seller(page);
  await page.goto("/storefront/fronts");
  await page.getByRole("button", { name: "Add a front" }).click();
  const sheet = page.getByRole("dialog", { name: "Add a front" });
  await sheet.getByRole("radio", { name: /One product/ }).check();
  await expect(sheet.getByLabel("The product")).toHaveValue("ear");
  await expect(sheet.getByLabel("Name")).toHaveValue("Smart Ear Cleaner");
  await sheet.getByLabel("Price of package 1 (₦)").fill("25000");
  await sheet.getByLabel("Price of package 2 (₦)").fill("45000");
  await sheet.getByLabel("Price of package 3 (₦)").fill("65000");
  await check(page, "Add a One product front");
  await sheet.getByRole("button", { name: "Add the front" }).click();

  // Its page: what it sells, its promise line and its chips.
  await expect(page.getByRole("heading", { name: "Smart Ear Cleaner", level: 1 })).toBeVisible();
  const card = page.getByRole("region", { name: "The page" });
  await expect(card.getByText("Smart Ear Cleaner, in 3 packages")).toBeVisible();
  await expect(card.getByLabel("Promise line")).toHaveValue("Pay on delivery nationwide");
  await card.getByLabel("Promise line").fill("Pay on delivery nationwide + free delivery today");
  await card.getByRole("button", { name: "Add a chip" }).click();
  await card.getByLabel("Icon of chip 3").selectOption("shield");
  await card.getByLabel("Words of chip 3").fill("30-day warranty");
  await card.getByRole("button", { name: "Save the page" }).click();
  await expect(page.getByText(/^Saved/)).toBeVisible();
  await check(page, "a One product front");

  // Its order form on, then the front, then the page published.
  await page.getByRole("button", { name: "Turn on orders" }).click();
  await expect(page.getByTestId("orders-banner")).toHaveCount(0);
  await page.getByRole("button", { name: "Turn on", exact: true }).click();
  await expect(page.getByRole("button", { name: "Turn off", exact: true })).toBeVisible();
  const front = sql(`SELECT slug FROM storefront_fronts WHERE tenant_id = '${tenant}' AND kind = 'one-product'`);
  expect(front).toBe("smart-ear-cleaner");

  // The page in the video, on the public host.
  await page.goto(`http://${slug}.e2e.test:${PUBLIC_PORT}/${front}`);
  await expect(page.getByText("Pay on delivery nationwide + free delivery today")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Smart Ear Cleaner", level: 1 })).toBeVisible();
  await expect(page.getByText("30-day warranty")).toBeVisible();
  await expect(page.getByText("From ₦25,000")).toBeVisible();
  await check(page, "the One product page");
  await page.getByRole("link", { name: "Order now" }).click();
  await page.getByText("2 items", { exact: true }).click();
  await page.getByLabel("Your name").fill("Bola Ade");
  await page.getByLabel("Phone number").fill("0803 555 1234");
  await page.getByLabel(/Delivery area/).selectOption("Lekki");
  await expect(page.getByText("Total: ₦46,500 with ₦1,500 delivery")).toBeVisible();
  await page.getByLabel(/Delivery address/).fill("12 Admiralty Way");
  await page.getByText("When it arrives", { exact: true }).click();
  await page.waitForTimeout(3500); // a person takes a few seconds
  await page.getByRole("button", { name: "Send order" }).click();
  await expect(page.getByRole("heading", { name: /We have your order/ })).toBeVisible();
  // Sent as an order of the form under the page. It waits in the inbox for
  // its price check, which reads the products table this test server has
  // no rows service for (api/oneproduct_test.go follows it into Orders).
  expect(
    sql(
      `SELECT count(*) FROM storefront_inbox i JOIN storefront_actions a ON a.id = i.action_id WHERE i.tenant_id = '${tenant}' AND a.slug = 'order-now'` +
        ` UNION ALL SELECT count(*) FROM storefront_orders o JOIN storefront_actions a ON a.id = o.action_id WHERE o.tenant_id = '${tenant}' AND a.slug = 'order-now'`,
    )
      .split("\n")
      .reduce((n, x) => n + Number(x), 0),
  ).toBe(1);
});
