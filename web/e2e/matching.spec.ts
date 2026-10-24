import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Matching money to invoices (decision 0045, T5; docs/invoices.md
// "Matching"): the To match view, the way a payment says how it was
// matched, and the automatic-matching setting. The money and the
// suggestions are written straight into the e2e database (as the matcher
// would write them; serve.sh makes the database), so the test does not
// depend on a payments provider or on the matcher's scoring.
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

interface Made {
  id: string;
  number: string;
}

/** An invoice for ₦25,000, made in the API and marked sent in the database:
 * the e2e server sends no messages to customers, and sending is not what is tested. */
async function sentInvoice(page: Page, name: string): Promise<Made> {
  const r = await page.request.post("/v1/invoices", {
    headers: { ...H, "Idempotency-Key": `e2e:${randomUUID()}` },
    data: { customer: { name, email: `${name.toLowerCase().replaceAll(" ", ".")}@example.test` }, amount_kobo: 2500000, description: "Catering" },
  });
  expect(r.ok(), await r.text()).toBeTruthy();
  const x = (await r.json()) as Made;
  sql(`UPDATE customer_invoices SET sent_at = now(), updated_at = now() WHERE id = '${x.id}'`);
  return x;
}

/** Money in, and what the matcher suggested for it (its state says it has been looked at, so the running matcher leaves it). */
function seedPayment(tenant: string, reference: string, suggestions: { invoice: Made; score: number; reasons: string[] }[]): string[] {
  const entry = randomUUID();
  const ids = suggestions.map(() => randomUUID());
  const rows = suggestions
    .map((s, i) => `('${ids[i]}', '${tenant}', '${entry}', '${s.invoice.id}', ${s.score}, '${JSON.stringify(s.reasons).replaceAll("'", "''")}'::jsonb)`)
    .join(", ");
  sql(
    `INSERT INTO money_entries (id, tenant_id, entry_key, occurred_at, direction, kind, amount_kobo, status, channel, reference, source, mirrored_at)
       VALUES ('${entry}', '${tenant}', 'paystack/main|e2e|${reference}', now(), 'in', 'account_credit', 2500000, 'settled', 'paystack/main', '${reference}', 'event', now());
     INSERT INTO money_match_state (entry_id, tenant_id, state) VALUES ('${entry}', '${tenant}', 'suggested');
     INSERT INTO money_suggestions (id, tenant_id, entry_id, invoice_id, score, reasons) VALUES ${rows};`,
  );
  return ids;
}

test("money is matched to invoices from To match, and each payment says how", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page);
  const tenant = sql("SELECT id FROM tenants WHERE name = 'E2E'");
  const [a, b, c, d, e] = [
    await sentInvoice(page, "Ada Obi"),
    await sentInvoice(page, "Bola Ade"),
    await sentInvoice(page, "Chidi Eze"),
    await sentInvoice(page, "Dayo Bello"),
    await sentInvoice(page, "Efe Okon"),
  ] as [Made, Made, Made, Made, Made];
  seedPayment(tenant, "E2E-MATCH-1", [
    { invoice: a, score: 92, reasons: ["same amount", "payer ADA OBI matches the customer"] },
    { invoice: b, score: 55, reasons: ["same amount"] },
  ]);
  seedPayment(tenant, "E2E-MATCH-2", [
    { invoice: b, score: 65, reasons: ["same amount", "two days after the invoice"] },
    { invoice: c, score: 45, reasons: ["same amount"] },
  ]);
  const [stale] = seedPayment(tenant, "E2E-MATCH-3", [{ invoice: e, score: 70, reasons: ["same amount"] }]);

  // The Invoices page links to To match, with the number of payments waiting.
  await page.goto("/invoices");
  const tabs = page.getByRole("navigation", { name: "Invoices" });
  await expect(tabs.getByTestId("to-match-count")).toHaveText(/^3/);
  await tabs.getByRole("link", { name: /To match/ }).click();
  await expect(page.getByRole("heading", { level: 1, name: "To match" })).toBeVisible();
  const cards = page.getByTestId("waiting-payment");
  await expect(cards).toHaveCount(3);

  // Accessible in both themes, with payments waiting.
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`/invoices/match (${scheme})`, vs)).toEqual([]);
  }

  // One card per payment: what came in, and its candidates strongest first, scores in words, reasons listed.
  const first = cards.filter({ hasText: "E2E-MATCH-1" });
  await expect(first).toContainText("Transfer into an account");
  await expect(first).toContainText("Paystack (main)");
  await expect(first).toContainText("₦25,000");
  const candidates = first.getByTestId("candidate");
  await expect(candidates.nth(0)).toContainText(a.number);
  await expect(candidates.nth(0)).toContainText("Strong");
  await expect(candidates.nth(0).getByRole("list", { name: `Why ${a.number}` })).toContainText("payer ADA OBI matches the customer");
  await expect(candidates.nth(1)).toContainText(b.number);
  await expect(candidates.nth(1)).toContainText("Possible");

  // Match: the payment counts towards the invoice and leaves the list.
  await first.getByRole("button", { name: `Match to ${a.number}` }).click();
  await expect(page.getByTestId("match-status")).toContainText(`₦25,000 matched to ${a.number}`);
  await expect(cards).toHaveCount(2);

  // Not this one: the suggestion goes; the payment's others stay.
  const second = cards.filter({ hasText: "E2E-MATCH-2" });
  await expect(second.getByTestId("candidate").first()).toContainText("Likely");
  await second.getByRole("button", { name: `Not this one: ${c.number}` }).click();
  await expect(page.getByTestId("match-status")).toContainText(`${c.number} will not be suggested for this payment again.`);
  await expect(second.getByTestId("candidate")).toHaveCount(1);

  // Match to another invoice, by hand.
  await second.getByText("Match to another invoice…").click();
  await second.getByLabel("Invoice").selectOption(d.id);
  await second.getByRole("button", { name: "Match to this invoice" }).click();
  await expect(page.getByTestId("match-status")).toContainText(`matched to ${d.number}`);
  await expect(cards).toHaveCount(1);

  // The server's refusal is shown as it says it: this suggestion was decided elsewhere.
  const r = await page.request.post(`/v1/money/suggestions/${stale}/confirm`, { headers: H });
  expect(r.ok(), await r.text()).toBeTruthy();
  const third = cards.filter({ hasText: "E2E-MATCH-3" });
  await third.getByRole("button", { name: `Match to ${e.number}` }).click();
  await expect(third.getByRole("alert")).toContainText(/confirmed/i);
  await page.reload();
  await expect(page.getByText(/^Nothing to match/)).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Invoices" }).getByTestId("to-match-count")).toHaveCount(0);

  // The invoice says how each payment was matched, with the reasons, and the match can be undone.
  await page.goto(`/invoices/${a.number}`);
  const pay = page.getByTestId("payment");
  await expect(pay.getByTestId("how-matched")).toHaveText(/^Suggested, confirmed by .+/);
  await expect(pay.getByRole("list", { name: "Why it was matched" })).toContainText("payer ADA OBI matches the customer");
  await pay.getByRole("button", { name: /^Undo the payment/ }).click();
  await page.getByRole("button", { name: "Undo payment" }).click();
  await expect(pay).toContainText("Undone");
  await page.goto(`/invoices/${d.number}`);
  await expect(page.getByTestId("payment").getByTestId("how-matched")).toHaveText("Matched by hand");
  await page.goto(`/invoices/${a.number}`);
  await expect(page.getByTestId("payment")).toContainText("Undone");
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`/invoices/${a.number} (${scheme})`, vs)).toEqual([]);
  }
});

test("automatic matching is a setting, saved with the others", async ({ page }) => {
  await signIn(page);
  await page.goto("/invoices/settings");
  const auto = page.getByLabel(/Match strong suggestions automatically/);
  await expect(auto).not.toBeChecked();
  const before = await (await page.request.get("/v1/invoice-settings")).json();
  await auto.check();
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect(page.locator(".toast").filter({ hasText: "Settings saved" })).toBeVisible();
  const after = await (await page.request.get("/v1/invoice-settings")).json();
  expect(after).toEqual({ ...before, auto_match: true });
  await page.reload();
  await expect(page.getByLabel(/Match strong suggestions automatically/)).toBeChecked();
  await page.getByLabel(/Match strong suggestions automatically/).uncheck();
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect(page.locator(".toast").filter({ hasText: "Settings saved" }).last()).toBeVisible();
  expect(await (await page.request.get("/v1/invoice-settings")).json()).toEqual({ ...before, auto_match: false });
});

/** A storefront order of ₦25,000 taken without online payment, waiting for its money (the business's storefront made if it has none).
 * Its customer is sealed under a key the database does not hold, so it reads as erased: who it is from is not what is tested. */
function storefrontOrder(tenant: string): string {
  const reference = `AK-E2E${randomUUID().slice(0, 4).toUpperCase()}`;
  sql(
    `INSERT INTO storefronts (id, tenant_id, slug, draft, created_by) VALUES (gen_random_uuid(), '${tenant}', 'e2e-matching', '{}', 'user:e2e') ON CONFLICT DO NOTHING;
     INSERT INTO storefront_orders (id, tenant_id, storefront_id, kind, reference, status_hash, status, customer, subject_id, total_kobo, payment)
       SELECT gen_random_uuid(), '${tenant}', s.id, 'order', '${reference}', '\\x00', 'received', '{"subject": "e2e-none", "ct": "AAAA"}', 'e2e-none', 2500000, 'none'
       FROM storefronts s WHERE s.tenant_id = '${tenant}';`,
  );
  return reference;
}

/** Money in for an order, with the matcher's suggestion for it, or nothing suggested (left for a person). */
function seedOrderPayment(tenant: string, reference: string, order?: string): void {
  const entry = randomUUID();
  const reasons = JSON.stringify(order ? [`the reference mentions ${order}`, "same amount as what is due"] : []);
  sql(
    `INSERT INTO money_entries (id, tenant_id, entry_key, occurred_at, direction, kind, amount_kobo, status, channel, reference, source, mirrored_at)
       VALUES ('${entry}', '${tenant}', 'paystack/main|e2e|${reference}', now(), 'in', 'account_credit', 2500000, 'settled', 'paystack/main', '${reference}', 'event', now());
     INSERT INTO money_match_state (entry_id, tenant_id, state) VALUES ('${entry}', '${tenant}', '${order ? "suggested" : "unmatched"}');` +
      (order
        ? `INSERT INTO money_suggestions (id, tenant_id, entry_id, order_id, score, reasons)
             SELECT gen_random_uuid(), '${tenant}', '${entry}', o.id, 90, '${reasons}'::jsonb FROM storefront_orders o WHERE o.reference = '${order}';`
        : ""),
  );
}

test("money is matched to storefront orders from To match, and undone on the order", async ({ page }) => {
  test.setTimeout(120_000);
  // A business of its own: the E2E business's storefront belongs to storefront.spec.ts, which expects it without orders.
  const name = `Ada Shop ${Date.now()}`;
  await page.goto("/login");
  await page.getByRole("link", { name: "Create an account" }).click();
  await page.getByLabel("Business or organisation name").fill(name);
  await page.getByLabel("Your name").fill("Ada Obi");
  await page.getByLabel("Email").fill(`shop-${Date.now()}@stores.test`);
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page.getByRole("heading", { name: "Get started" })).toBeVisible();
  const tenant = sql(`SELECT id FROM tenants WHERE name = '${name}'`);
  const [suggested, byHand] = [storefrontOrder(tenant), storefrontOrder(tenant)];
  seedOrderPayment(tenant, `TRF ${suggested}`, suggested);
  seedOrderPayment(tenant, "E2E-ORDER-LEFT");

  await page.goto("/invoices/match");
  const cards = page.getByTestId("waiting-payment");
  const first = cards.filter({ hasText: `TRF ${suggested}` });
  const candidate = first.getByTestId("candidate");
  await expect(candidate).toHaveCount(1);
  await expect(candidate.getByRole("link", { name: `Order ${suggested}` })).toHaveAttribute("href", `/storefront/orders/${suggested}`);
  await expect(candidate).toContainText("₦25,000 to pay");
  await expect(candidate.getByRole("list", { name: `Why Order ${suggested}` })).toContainText(`the reference mentions ${suggested}`);
  // The order waiting for money can be chosen by hand for the money left over.
  const left = cards.filter({ hasText: "E2E-ORDER-LEFT" });
  await left.getByText("Match to another invoice or order…").click();
  await expect(left.getByLabel("Invoice or order").locator(`option[value="order:${byHand}"]`)).toHaveText(`Order ${byHand} · ₦25,000 to pay`);
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`/invoices/match with orders (${scheme})`, vs)).toEqual([]);
  }

  // Match the suggestion: the order is paid.
  await first.getByRole("button", { name: `Match to Order ${suggested}` }).click();
  await expect(page.getByTestId("match-status")).toContainText(`₦25,000 matched to order ${suggested}. If that is wrong, undo it on the order.`);
  await expect(first).toHaveCount(0);

  // By hand, to the other order.
  await left.getByLabel("Invoice or order").selectOption(`order:${byHand}`);
  await left.getByRole("button", { name: "Match to this one" }).click();
  await expect(page.getByTestId("match-status")).toContainText(`matched to order ${byHand}`);
  await expect(left).toHaveCount(0);

  // The order lists the money matched, and the match is undone there.
  await page.goto(`/storefront/orders/${suggested}`);
  const pay = page.getByTestId("order-payment");
  await expect(pay.getByTestId("how-matched")).toHaveText(/^Suggested, confirmed by .+/);
  await expect(pay.getByRole("list", { name: "Why it was matched" })).toContainText(`the reference mentions ${suggested}`);
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`/storefront/orders/${suggested} (${scheme})`, vs)).toEqual([]);
  }
  page.once("dialog", (d) => void d.accept());
  await pay.getByRole("button", { name: "Undo the match" }).click();
  await expect(pay).toContainText("Undone");
  await expect(pay.getByRole("button", { name: "Undo the match" })).toHaveCount(0);
  expect(sql(`SELECT paid_at IS NULL FROM storefront_orders WHERE reference = '${suggested}'`)).toBe("t");

  // The money is back to be matched.
  await page.goto("/invoices/match");
  await expect(cards.filter({ hasText: `TRF ${suggested}` })).toHaveCount(1);
});

test("a payment is split across a customer's invoices, held money says why, and a remembered payer is forgotten", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page);
  const tenant = sql("SELECT id FROM tenants WHERE name = 'E2E'");
  // Two invoices of one customer (the same email), ₦25,000 each, oldest first.
  const a = await sentInvoice(page, "Gozie Nwosu");
  sql(`UPDATE customer_invoices SET sent_at = now() - interval '2 days' WHERE id = '${a.id}'`);
  const b = await sentInvoice(page, "Gozie Nwosu");
  // ₦60,000 in, suggested for the newer one; and a credit held for a person.
  const entry = randomUUID();
  sql(
    `INSERT INTO money_entries (id, tenant_id, entry_key, occurred_at, direction, kind, amount_kobo, status, channel, reference, source, mirrored_at)
       VALUES ('${entry}', '${tenant}', 'paystack/main|e2e|E2E-SPLIT-1', now(), 'in', 'account_credit', 6000000, 'settled', 'paystack/main', 'E2E-SPLIT-1', 'event', now());
     INSERT INTO money_match_state (entry_id, tenant_id, state) VALUES ('${entry}', '${tenant}', 'suggested');
     INSERT INTO money_suggestions (id, tenant_id, entry_id, invoice_id, score, reasons) VALUES (gen_random_uuid(), '${tenant}', '${entry}', '${b.id}', 60, '["the payer shares a name with the customer"]'::jsonb);
     INSERT INTO money_entries (id, tenant_id, entry_key, occurred_at, direction, kind, amount_kobo, status, channel, reference, source, mirrored_at, account_number)
       VALUES (gen_random_uuid(), '${tenant}', 'paystack/main|e2e|E2E-HELD-1', now(), 'in', 'account_credit', 1000000, 'settled', 'paystack/main', 'E2E-HELD-1', 'event', now(), '9900000999');
     INSERT INTO money_match_state (entry_id, tenant_id, state, held_reason)
       SELECT id, '${tenant}', 'unmatched', 'the reference names a customer''s account, but the money was paid into another account number' FROM money_entries WHERE reference = 'E2E-HELD-1';`,
  );

  await page.goto("/invoices/match");
  const cards = page.getByTestId("waiting-payment");
  const held = cards.filter({ hasText: "E2E-HELD-1" });
  await expect(held.getByTestId("held-reason")).toContainText("paid into another account number");
  const card = cards.filter({ hasText: "E2E-SPLIT-1" });
  await card.getByText("Split across the customer's invoices…").click();
  const split = card.getByTestId("split");
  await expect(split.getByLabel(`${a.number} (₦25,000 due)`)).toHaveValue("25000");
  await expect(split.getByLabel(`${b.number} (₦25,000 due)`)).toHaveValue("25000");
  await expect(split.getByTestId("split-rest")).toHaveText("₦10,000 not counted towards these invoices.");
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`/invoices/match with a split (${scheme})`, vs)).toEqual([]);
  }
  // Too much is refused before it is sent; the amounts can be changed.
  await split.getByLabel(`${a.number} (₦25,000 due)`).fill("45000");
  await split.getByRole("button", { name: "Match in parts" }).click();
  await expect(split.getByRole("alert")).toHaveText("The parts add up to ₦70,000, more than the ₦60,000 received.");
  await split.getByLabel(`${a.number} (₦25,000 due)`).fill("25000");
  await split.getByLabel(`${b.number} (₦25,000 due)`).fill("20000");
  await expect(split.getByLabel(/Keep the rest as the customer's credit/)).toBeChecked();
  await split.getByRole("button", { name: "Match in parts" }).click();
  await expect(page.getByTestId("match-status")).toContainText(
    `Matched in parts: ${a.number} ₦25,000, ${b.number} ₦20,000. ₦15,000 is the customer's credit, counted towards their next invoice.`,
  );
  await expect(card).toHaveCount(0);

  // The invoice shows the customer's credit, and a payer remembered for them can be forgotten.
  const customer = sql(`SELECT customer_id FROM customer_invoices WHERE id = '${a.id}'`);
  sql(
    `INSERT INTO money_payers (id, tenant_id, kind, payer_key, payer, customer_id, first_by)
       VALUES (gen_random_uuid(), '${tenant}', 'name', 'payer_name:e2e', '{"subject": "e2e-none", "ct": "AAAA"}', '${customer}', 'user:e2e')`,
  );
  await page.goto(`/invoices/${a.number}`);
  const money = page.getByTestId("customer-money");
  await expect(money.getByTestId("customer-credit")).toContainText("₦15,000: paid and not yet counted towards an invoice.");
  await expect(money.getByTestId("payer")).toHaveCount(1);
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`/invoices/${a.number} with the customer's money (${scheme})`, vs)).toEqual([]);
  }
  await money.getByRole("button", { name: /^Forget this payer/ }).click();
  await expect(page.locator(".toast").filter({ hasText: "Payer forgotten" })).toBeVisible();
  await expect(money.getByTestId("payer")).toHaveCount(0);
  expect(sql(`SELECT count(*) FROM money_payers WHERE customer_id = '${customer}'`)).toBe("0");
});
