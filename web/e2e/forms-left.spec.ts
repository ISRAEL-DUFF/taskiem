import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { expect, test, type APIResponse, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Forms that sell L3 (docs/spec/forms-that-sell.md §6, §11): an owner turns
// on follow-ups for an order form; the buyer's page says so under the
// phone; a buyer who types their number and leaves is kept (only the
// number and the choice), followed up by text once the wait has passed,
// and shown to the owner masked under Left without ordering; the
// follow-up's link draws a page that uses nothing up until Finish my
// order opens the form with the choice kept. Every screen passes axe in
// both themes and fits 390 and 360 px.

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
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, `/${DB}$1`), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

async function check(page: Page, where: string) {
  const found: string[] = [];
  for (const scheme of ["dark", "light"] as const)
    for (const width of [360, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const vs = await axe(page);
      if (vs.length > 0) found.push(describe(`${where}, ${scheme}, ${width} px`, vs));
      expect(await page.evaluate(() => document.documentElement.scrollWidth), `${where}, ${scheme}, ${width} px`).toBeLessThanOrEqual(width);
      if (process.env.SCREENSHOT_DIR && width === 390)
        await page.screenshot({ path: `${process.env.SCREENSHOT_DIR}/${where.replace(/[^a-z0-9]+/gi, "-")}-${scheme}.png`, fullPage: true });
    }
  expect(found, found.join("\n")).toEqual([]);
}

async function ok(r: Promise<APIResponse>) {
  const res = await r;
  expect(res.ok(), await res.text()).toBeTruthy();
  return res.json() as Promise<Record<string, unknown>>;
}

/** A business with a published page whose order form sells the ear
 * cleaner in three packages, delivered to Lekki, paid at the door. */
async function seller(page: Page): Promise<{ slug: string; tenant: string; action: string; ref: string }> {
  const n = `${Date.now()}${Math.floor(Math.random() * 1e4)}`;
  const email = `left-${n}@left.test`;
  const slug = `left-${n}`;
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
  await ok(page.request.put("/v1/storefront", { headers: H, data: { slug, draft: { name: "Ear Shop" } } }));
  const opts = { delivery: true, pay_on_delivery: true, zones: [{ name: "Lekki", fee_kobo: 150000 }] };
  await ok(page.request.put("/v1/storefront/actions/order", { headers: H, data: { enabled: false, payment: "after_accept", options: opts } }));
  await publish(page, tenant);
  const fronts = (await ok(page.request.get("/v1/storefront/fronts", { headers: H }))).fronts as { id: string; slug: string }[];
  const home = fronts.find((f) => f.slug === "")?.id ?? "";
  expect(home).not.toBe("");
  const packs = [
    { qty: 1, price_kobo: 2500000 },
    { qty: 2, price_kobo: 4500000 },
    { qty: 3, price_kobo: 6500000 },
  ];
  const made = (await ok(
    page.request.post(`/v1/storefront/fronts/${home}/actions`, {
      headers: H,
      data: { kind: "order", label: "Order the ear cleaner", sells: { item: "ear", packages: packs } },
    }),
  )) as { action: { id: string; slug: string } };
  const action = made.action.id;
  const wf = ((await ok(page.request.post(`/v1/storefront/actions/${action}/setup`, { headers: H }))) as { workflow_id: string }).workflow_id;
  await ok(page.request.post(`/v1/workflows/${wf}/versions/1/publish`, { headers: H }));
  await ok(
    page.request.put(`/v1/storefront/actions/${action}`, {
      headers: H,
      data: { enabled: true, label: "Order the ear cleaner", payment: "after_accept", options: opts },
    }),
  );
  await publish(page, tenant);
  return { slug, tenant, action, ref: made.action.slug };
}

async function publish(page: Page, tenant: string) {
  await ok(page.request.post("/v1/storefront/publish", { headers: H }));
  sql(
    `INSERT INTO storefront_catalogue (storefront_id, table_name, row_id, tenant_id, position, item) SELECT id, '', 'ear', tenant_id, 0, ` +
      `'{"id":"ear","name":"Smart Ear Cleaner","price_kobo":2500000,"available":true}'::jsonb FROM storefronts WHERE tenant_id = '${tenant}' ON CONFLICT DO NOTHING`,
  );
}

test("a buyer who left is followed up, and the owner sees it masked", async ({ page }) => {
  test.setTimeout(120_000);
  const { slug, tenant, action, ref } = await seller(page);

  // The owner turns follow-ups on for the order form.
  await page.goto(`/storefront/left/${action}`);
  await expect(page.getByRole("heading", { name: "Left without ordering" })).toBeVisible();
  const on = page.getByRole("switch", { name: "Follow up people who leave without ordering" });
  await expect(on).not.toBeChecked();
  await check(page, "Left without ordering, off");
  await on.click();
  await expect(on).toBeChecked();
  await expect(page.getByText(/They get one text/)).toBeVisible();
  await expect(page.getByLabel("Wait before the message")).toHaveValue("30");
  await check(page, "Left without ordering, on");

  // The buyer's page says so under the phone; they type it and leave.
  await page.waitForTimeout(1500); // the public host's page cache
  const form = `http://${slug}.e2e.test:${PUBLIC_PORT}/a/${ref}`;
  await expect
    .poll(async () => {
      await page.goto(form);
      return page.getByText(/If you leave before you send this, we may send you one message/).count();
    }, { timeout: 90_000, intervals: [3000] })
    .toBe(1);
  await check(page, "an order form that follows up");
  await page.getByText("2 items", { exact: true }).click();
  await page.getByLabel("Your name").fill("Bola Ade");
  await page.getByLabel(/Delivery area/).selectOption("Lekki");
  await page.waitForTimeout(3500); // the keep token, as a person takes a few seconds
  await page.getByLabel("Phone number").fill("0803 555 1234");
  await page.getByLabel(/Delivery address/).click(); // the phone loses focus: a keep
  await expect
    .poll(() => sql(`SELECT count(*) || ' ' || max(choice::text) FROM form_left WHERE action_id = '${action}'`), { timeout: 20_000 })
    .toMatch(/^1 (?=.*"package": 2)(?=.*Lekki)/);
  expect(sql(`SELECT count(*) FROM form_left WHERE action_id = '${action}' AND (contact::text LIKE '%5551234%' OR choice::text LIKE '%Bola%')`)).toBe("0");

  // The wait passes: one text.
  sql(`UPDATE form_left SET last_keep_at = now() - interval '31 minutes' WHERE action_id = '${action}'`);
  await expect
    .poll(() => sql(`SELECT count(*) FROM platform_messages WHERE tenant_id = '${tenant}' AND channel = 'sms' AND dedup_key LIKE 'followup:%'`), {
      timeout: 40_000,
    })
    .toBe("1");

  // The owner sees it, masked.
  await page.goto(`/storefront/left/${action}`);
  const row = page.getByTestId("left-row");
  await expect(row).toHaveCount(1);
  await expect(row).toContainText("Smart Ear Cleaner, 2 items, to Lekki");
  await expect(row).toContainText("Phone … 1234 · Followed up by text");
  await expect(page.getByTestId("left-week")).toContainText("Followed up1");
  await check(page, "Left without ordering, one followed up");

  // A follow-up's link (its token is only in the text: one is written
  // here): its page uses nothing up; Finish my order keeps the choice.
  const token = "e2eRestoreTokenAbcdefghijklmnop0";
  sql(
    `INSERT INTO form_restores (token_hash, tenant_id, action_id, choice, expires_at, left_id) SELECT '\\x${createHash("sha256").update(token).digest("hex")}', tenant_id, action_id, choice, now() + interval '7 days', id FROM form_left WHERE action_id = '${action}'`,
  );
  const back = `http://${slug}.e2e.test:${PUBLIC_PORT}/back/${token}`;
  await page.goto(back);
  await page.goto(back);
  await expect(page.getByRole("button", { name: "Finish my order" })).toBeVisible();
  await check(page, "a follow-up's link");
  await page.getByRole("button", { name: "Finish my order" }).click();
  await expect(page).toHaveURL(new RegExp(`/a/${ref}\\?pkg=2&zone=Lekki$`));
  await expect(page.getByRole("radio", { name: /2 items/ })).toBeChecked();
  await expect(page.getByLabel(/Delivery area/)).toHaveValue("Lekki");
  await page.goto(back);
  await expect(page.getByRole("button", { name: "Finish my order" })).toHaveCount(0);
  await page.getByRole("button", { name: "Stop these messages" }).click();
  await expect(page.getByText(/no more messages like this from Ear Shop/)).toBeVisible();
  await check(page, "messages stopped");
});
