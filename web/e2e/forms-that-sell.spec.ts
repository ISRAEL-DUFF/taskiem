import { execFileSync } from "node:child_process";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Forms that sell L1 (docs/spec/forms-that-sell.md §5, §11): an owner adds
// an order form selling one product in three packages from Fronts, and
// turns a hosted form into one with Sell something with this form (offered
// to turn the Storefront area on when it is off); a buyer orders two on
// the public host, paying at the door. Every screen passes axe in both
// themes and fits 390 and 360 px. The public host answers *.e2e.test on
// its own port (serve.sh); Chromium maps those names to this machine.

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
    [
      base.replace(/\/[^/?]*(\?.*)?$/, `/${DB}$1`),
      "-v",
      "ON_ERROR_STOP=1",
      "-tAqc",
      text,
    ],
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
      if (vs.length > 0)
        found.push(describe(`${where}, ${scheme}, ${width} px`, vs));
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
        `${where}, ${scheme}, ${width} px`,
      ).toBeLessThanOrEqual(width);
      // SCREENSHOT_DIR=... keeps a picture of each, for review.
      if (process.env.SCREENSHOT_DIR && width === 390)
        await page.screenshot({
          path: `${process.env.SCREENSHOT_DIR}/${where.replace(/[^a-z0-9]+/gi, "-")}-${scheme}.png`,
          fullPage: true,
        });
    }
  expect(found, found.join("\n")).toEqual([]);
}

/** A business of its own with a published page selling one product, its
 * order form delivering to two areas and taking payment at the door. */
async function seller(page: Page): Promise<{ slug: string; tenant: string }> {
  const n = `${Date.now()}${Math.floor(Math.random() * 1e4)}`;
  const email = `sell-${n}@sell.test`;
  const slug = `ear-${n}`;
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Ear Shop ${n}`,
      email,
      name: "Owner",
      password: PASSWORD,
      accept_terms: true,
    },
  });
  expect(r.status(), await r.text()).toBe(201);
  sql(
    `UPDATE users SET email_verified_at = now() WHERE lower(email) = lower('${email}')`,
  );
  const tenant = (
    (await (await page.request.get("/v1/me", { headers: H })).json()) as {
      tenant_id: string;
    }
  ).tenant_id;
  sql(
    `INSERT INTO tenant_verifications (id, tenant_id, level, method, actor) VALUES (gen_random_uuid(), '${tenant}', 'phone', 'test', 'test'), (gen_random_uuid(), '${tenant}', 'business', 'test', 'test')`,
  );
  expect(
    (
      await page.request.put("/v1/storefront", {
        headers: H,
        data: { slug, draft: { name: "Ear Shop" } },
      })
    ).ok(),
  ).toBeTruthy();
  const opts = {
    delivery: true,
    pay_on_delivery: true,
    zones: [
      { name: "Lekki", fee_kobo: 150000 },
      { name: "Yaba", fee_kobo: 0 },
    ],
  };
  const a = await page.request.put("/v1/storefront/actions/order", {
    headers: H,
    data: { enabled: false, payment: "after_accept", options: opts },
  });
  expect(a.ok(), await a.text()).toBeTruthy();
  await publish(page, tenant);
  return { slug, tenant };
}

/** Publishes the page, then puts the product back in its copy: this test
 * server has no data tables to copy it from. */
async function publish(page: Page, tenant: string) {
  const p = await page.request.post("/v1/storefront/publish", { headers: H });
  expect(p.ok(), await p.text()).toBeTruthy();
  sql(
    `INSERT INTO storefront_catalogue (storefront_id, table_name, row_id, tenant_id, position, item) SELECT id, '', 'ear', tenant_id, 0, ` +
      `'{"id":"ear","name":"Smart Ear Cleaner","price_kobo":2500000,"available":true,"description":"A camera on a soft tip: see what you clean."}'::jsonb ` +
      `FROM storefronts WHERE tenant_id = '${tenant}' ON CONFLICT DO NOTHING`,
  );
}

test("an owner adds an order form in packages and a buyer orders two, paying at the door", async ({
  page,
}) => {
  const { slug, tenant } = await seller(page);
  await page.goto("/storefront/fronts");
  await page
    .getByTestId("front")
    .filter({ hasText: "Your home page" })
    .getByRole("link")
    .click();
  const forms = page.getByRole("region", { name: "Forms" });
  await forms.getByRole("button", { name: "Add an order form" }).click();
  const sheet = page.getByRole("dialog", { name: "Add an order form" });
  await expect(sheet.getByLabel("The product")).toHaveValue("ear");
  await expect(sheet.getByLabel("Price of package 2 (₦)")).toHaveValue("45000");
  await sheet.getByLabel("Price of package 3 (₦)").fill("65000");
  await sheet.getByLabel("Words for package 1").fill("1 item + free delivery");
  // A package dearer per item says so, and is not refused.
  await sheet.getByLabel("Price of package 2 (₦)").fill("52000");
  await expect(sheet.getByText(/₦26,000 an item/)).toBeVisible();
  await check(page, "Add an order form");
  await sheet.getByLabel("Price of package 2 (₦)").fill("45000");
  await sheet
    .getByRole("textbox", { name: /^Button/ })
    .fill("Order the ear cleaner");
  await sheet
    .getByLabel("Line above the button")
    .fill("Do not order if you are not ready to receive it in 2 to 4 days.");
  await sheet.getByRole("button", { name: "Add the order form" }).click();
  await expect(forms.getByText("Added, off")).toBeVisible();

  // Set up with the business's order workflow, then shown on the page.
  const card = forms.getByRole("article", { name: "Orders (another form)" });
  await card.getByRole("button", { name: /^Set up/ }).click();
  await expect(
    card.getByRole("link", { name: /Open its workflow/ }),
  ).toBeVisible();
  await card
    .getByRole("switch", { name: "Show this form on your page" })
    .check();
  await expect(card.getByLabel("Line above the button")).toHaveValue(
    "Do not order if you are not ready to receive it in 2 to 4 days.",
  );
  await card.getByRole("button", { name: /^Save/ }).click();
  await expect(card.getByText(/^Saved/)).toBeVisible();
  await publish(page, tenant);
  const action = sql(
    `SELECT slug FROM storefront_actions WHERE tenant_id = '${tenant}' AND NOT builtin AND kind = 'order'`,
  );
  expect(action).toBe("order-the-ear-cleaner");

  // The code for this one form, on the form's card.
  await page.reload();
  await page
    .getByRole("region", { name: "Forms" })
    .getByRole("button", { name: /Code for your website/ })
    .click();
  await expect(page.getByText(/storefront:height/)).toBeVisible();
  await check(page, "a front with an order form that sells");

  // The buyer, on the public host.
  await page.goto(`http://${slug}.e2e.test:${PUBLIC_PORT}/a/${action}`);
  await expect(
    page.getByRole("heading", { name: "Smart Ear Cleaner" }),
  ).toBeVisible();
  await expect(page.getByText("Not enough in stock")).toHaveCount(0);
  await check(page, "the form that sells");
  await page.getByText("2 items", { exact: true }).click();
  await page.getByLabel("Your name").fill("Bola Ade");
  await page.getByLabel("Phone number").fill("0803 555 1234");
  await page.getByLabel(/Delivery area/).selectOption("Lekki");
  await expect(
    page.getByText("Total: ₦46,500 with ₦1,500 delivery"),
  ).toBeVisible();
  await page.getByLabel(/Delivery address/).fill("12 Admiralty Way");
  await page.getByText("When it arrives", { exact: true }).click();
  await expect(
    page.getByText(
      "Do not order if you are not ready to receive it in 2 to 4 days.",
    ),
  ).toBeVisible();
  await page.waitForTimeout(3500); // a person takes a few seconds
  await page.getByRole("button", { name: "Send order" }).click();
  await expect(
    page.getByRole("heading", { name: /We have your order/ }),
  ).toBeVisible();
  // The status page refreshes itself, without a script, while the order
  // is taken in (as every storefront's does): axe's meta-refresh rule is
  // that page's own design, not this form's.
  await check(page, "the order's status page", ["meta-refresh"]);

  // It reaches Orders, the package as one line with the area's fee.
  await expect
    .poll(async () => {
      const o = (await (
        await page.request.get("/v1/storefront/orders", { headers: H })
      ).json()) as { orders: { total_kobo: number; payment: string }[] };
      return o.orders.map((x) => `${x.total_kobo} ${x.payment}`).join();
    })
    .toBe("4650000 on_delivery");
});

const HOSTED = {
  schema: "wd/v1",
  id: "wf_earform",
  version: 1,
  name: "Ear cleaner form",
  trigger: { type: "form", config: { title: "Ear cleaner", access: "public" } },
  inputs: {
    schema: {
      type: "object",
      required: ["full_name", "phone"],
      properties: {
        full_name: { type: "string", maxLength: 100 },
        phone: { type: "string", format: "phone-ng", "x-pii": "phone" },
        best_day: {
          type: "string",
          title: "Best day to deliver",
          maxLength: 40,
        },
      },
    },
  },
  steps: [
    {
      id: "note",
      type: "transform",
      config: {
        output: {
          who: "=trigger.body.full_name",
          day: "=trigger.body.best_day",
        },
      },
    },
  ],
};

test("an owner sells something with a form, turning the Storefront area on when asked", async ({
  page,
}) => {
  const { slug } = await seller(page);
  const made = await page.request.post("/v1/workflows", {
    headers: H,
    data: { name: "Ear cleaner form", definition: HOSTED },
  });
  expect(made.status(), await made.text()).toBe(201);
  const wf = ((await made.json()) as { id: string }).id;
  const pub = await page.request.post(
    `/v1/workflows/${wf}/versions/1/publish`,
    { headers: H },
  );
  expect(pub.ok(), await pub.text()).toBeTruthy();
  const off = await page.request.put("/v1/settings/areas", {
    headers: H,
    data: { areas: { storefront: false }, confirm: true },
  });
  expect(off.ok(), await off.text()).toBeTruthy();

  await page.goto("/forms");
  const card = page
    .getByRole("article", { name: "Ear cleaner" })
    .filter({ hasText: "prod" });
  await card
    .getByRole("button", { name: "Sell something with this form" })
    .click();
  const sheet = page.getByRole("dialog", {
    name: "Sell something with this form",
  });
  // Refused while the area is off, with the offer to turn it on.
  await expect(
    sheet.getByText("Storefront is turned off for this business."),
  ).toBeVisible();
  await expect(
    sheet.getByRole("button", { name: "Sell with this form" }),
  ).toBeDisabled();
  await check(page, "Sell something with this form, the area off");
  await sheet.getByRole("button", { name: "Turn on Storefront" }).click();
  await expect(
    sheet.getByRole("button", { name: "Turn on Storefront" }),
  ).toHaveCount(0);
  await expect(sheet.getByLabel("The product")).toHaveValue("ear");
  await sheet.getByLabel("Price of package 3 (₦)").fill("65000");
  await check(page, "Sell something with this form");
  await sheet.getByRole("button", { name: "Sell with this form" }).click();
  await expect(
    card.getByText("This form sells on your page now"),
  ).toBeVisible();
  await check(page, "a form that sells");

  // Its own question is asked beside the packages on the page.
  await page.goto(`http://${slug}.e2e.test:${PUBLIC_PORT}/a/ear-cleaner`);
  await expect(page.getByLabel(/Best day to deliver/)).toBeVisible();
  await expect(page.getByText("3 items", { exact: true })).toBeVisible();
});
