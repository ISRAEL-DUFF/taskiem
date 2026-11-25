import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// What a business buys, U3 (docs/spec/what-a-business-buys.md §5.3, §5.4,
// §9): Things we have under Money, grouped by label, with those no longer
// had in the history; a row's page, marked sold with a price (money in, for
// those who manage money only); Add something we have; an order's line
// leading to its rows; and spending by label in the money overview, Bought
// to keep apart and left out of profit. The server's side is tested in
// engine/stock, engine/moneyreport and api (U1); here the API is answered by
// a small stand-in, at a phone's widths, passing axe in both themes.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
const now = "2026-11-22T09:00:00Z";

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: "Ada Fashion House",
      email: `u3-${Date.now()}-${Math.random().toString(36).slice(2, 8)}@things.test`,
      name: "Ada",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function axeAt(page: Page, where: string) {
  for (const width of [390, 360]) {
    await page.setViewportSize({ width, height: 844 });
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const vs = await axe(page);
      expect(vs, describe(`${where} at ${width} (${scheme})`, vs)).toEqual([]);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
  }
  await page.emulateMedia({ colorScheme: "light" });
  await page.setViewportSize({ width: 390, height: 844 });
}

/** Without a permission: /v1/me as the server says it, less those. */
async function without(page: Page, ...perms: string[]) {
  await page.route("**/v1/me", async (r) => {
    const res = await r.fetch();
    const me = await res.json();
    me.permissions = (me.permissions as string[]).filter((p) => !perms.includes(p));
    await r.fulfill({ response: res, json: me });
  });
}

interface Thing {
  id: string;
  name: string;
  label_id?: string;
  label?: string;
  supplier_id?: string;
  supplier?: string;
  purchase_order_id?: string;
  order_reference?: string;
  purchase_line_id?: string;
  cost_kobo?: number;
  received_at?: string;
  serial?: string;
  warranty_until?: string;
  note?: string;
  status: string;
  status_at: string;
  sale_entry_id?: string;
  created_by: string;
  created_at: string;
}

const base = { status: "have", status_at: now, created_by: "user:ada", created_at: now };

/** Things we have as the server keeps them: listed, grouped by label (the
 * most cost first, Not labelled last), one read, added and changed. */
function thingsServer(page: Page, start: Thing[], money = true) {
  const things = start.map((t) => ({ ...t }));
  const sent: { method: string; path: string; body: unknown; key?: string }[] = [];
  const asked: string[] = [];
  void page.route("**/v1/things**", async (route: Route) => {
    const r = route.request();
    const url = new URL(r.url());
    const id = url.pathname.split("/")[3];
    const body = r.postData() ? r.postDataJSON() : undefined;
    if (r.method() !== "GET") sent.push({ method: r.method(), path: url.pathname, body, key: r.headers()["idempotency-key"] });
    if (r.method() === "GET" && !id) {
      asked.push(url.search);
      const all = url.searchParams.get("all") === "true";
      // ?line= filters on the server; the counts are the filtered rows'.
      const line = url.searchParams.get("line");
      const mine = things.filter((t) => !line || t.purchase_line_id === line);
      const list = mine.filter((t) => all || t.status === "have");
      const had = mine.filter((t) => t.status === "have");
      // As GroupThings: by the label's name, any case (a merged label's is
      // its kept label's), the first row's label_id.
      const key = (t: { label_id?: string | null; label?: string }) => (t.label_id ? (t.label ?? "").toLowerCase() : "");
      const groups: { label_id: string | null; label: string; count: number; total_kobo: number }[] = [];
      for (const t of had) {
        let g = groups.find((x) => key(x) === key(t));
        if (!g) groups.push((g = { label_id: t.label_id ?? null, label: t.label ?? "", count: 0, total_kobo: 0 }));
        g.count++;
        g.total_kobo += t.cost_kobo ?? 0;
      }
      groups.sort((a, b) => (a.label_id === null ? 1 : b.label_id === null ? -1 : b.total_kobo - a.total_kobo));
      return route.fulfill(json({ things: list, count: had.length, total_kobo: had.reduce((n, t) => n + (t.cost_kobo ?? 0), 0), groups }));
    }
    if (r.method() === "POST") {
      const b = body as Record<string, unknown>;
      const t: Thing = { ...base, id: `t${things.length + 1}`, name: b.name as string, cost_kobo: b.cost_kobo as number | undefined, label_id: b.label_id as string | undefined, label: b.label_id ? "Furniture" : undefined };
      things.unshift(t);
      return route.fulfill(json({ thing: t }, 201));
    }
    const t = things.find((x) => x.id === id);
    if (!t) return route.fulfill(json({ error: "not found" }, 404));
    if (r.method() === "PATCH") {
      const b = body as Record<string, unknown>;
      if (b.sale_price_kobo && !money) return route.fulfill(json({ error: "requires invoice.manage" }, 403));
      if (b.status) {
        t.status = b.status as string;
        t.status_at = now;
      }
      if (b.sale_price_kobo) t.sale_entry_id = "e1";
      if (b.supplier_id) {
        t.supplier_id = b.supplier_id as string;
        t.supplier = b.supplier_id === "s2" ? "Lister Power" : "Singer Ltd";
      }
      if (b.clear_supplier) {
        delete t.supplier_id;
        delete t.supplier;
      }
    }
    return route.fulfill(json({ thing: t }));
  });
  return { things, sent, asked };
}

function labels(page: Page, suggest: Record<string, string> = {}) {
  const made: string[] = [];
  const list = [{ id: "lab-m", name: "Machines", uses: 4, created_by: "user:ada", created_at: now }];
  void page.route("**/v1/money/labels**", async (route) => {
    if (route.request().method() === "POST") {
      const name = (route.request().postDataJSON() as { name: string }).name;
      made.push(name);
      return route.fulfill(json({ id: "lab-f", name, uses: 0, created_by: "user:ada", created_at: now }, 201));
    }
    // A suggestion by the item's name, as the server makes one (not yet a
    // label: no label_id).
    const item = new URL(route.request().url()).searchParams.get("item") ?? "";
    const s = suggest[item.toLowerCase()];
    return route.fulfill(json({ labels: list, suggestions: s ? [{ name: s }] : [] }));
  });
  return made;
}

const generator: Thing = { ...base, id: "t1", name: "Generator", label_id: "lab-m", label: "Machines", supplier_id: "s1", supplier: "Singer Ltd", purchase_order_id: "p1", order_reference: "PO-0100", purchase_line_id: "c", cost_kobo: 45_000_000, received_at: "2026-11-02T10:00:00Z", serial: "G-1", warranty_until: "2027-11-02" };
const overlock: Thing = { ...base, id: "t2", name: "Overlock machine", label_id: "lab-m", label: "Machines", supplier: "Singer Ltd", cost_kobo: 14_000_000, received_at: "2026-11-03T10:00:00Z" };
const fan: Thing = { ...base, id: "t3", name: "Standing fan", cost_kobo: 2_500_000, received_at: "2026-10-01T10:00:00Z" };
const oldIron: Thing = { ...base, id: "t4", name: "Old pressing iron", label_id: "lab-m", label: "Machines", cost_kobo: 900_000, status: "broken", received_at: "2025-01-01T10:00:00Z" };

test("Things we have, grouped by label, with those no longer had kept in the history", async ({ page }) => {
  await signUp(page);
  labels(page);
  thingsServer(page, [generator, overlock, fan, oldIron]);
  await page.goto("/money/things");
  await expect(page.getByRole("heading", { level: 1, name: "Things we have" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Money" }).getByRole("link", { name: "Things we have" })).toHaveAttribute("aria-current", "page");
  const hero = page.getByTestId("things-hero");
  await expect(hero).toContainText("3 things");
  await expect(hero).toContainText("₦615,000");
  const groups = page.getByTestId("things-group");
  await expect(groups).toHaveCount(2);
  await expect(groups.nth(0).getByRole("heading")).toContainText("Machines 2 things, ₦590,000");
  await expect(groups.nth(1).getByRole("heading")).toContainText("Not labelled");
  await expect(groups.nth(0).getByTestId("thing-row")).toHaveCount(2);
  await expect(groups.nth(0).getByTestId("thing-row").first()).toContainText("From Singer Ltd · came 2 Nov 2026");
  await expect(page.getByText("Old pressing iron")).toHaveCount(0);
  await axeAt(page, "Things we have");
  // The box follows the page's address, which changes a moment after the
  // click: click it, then wait for the address and the box.
  const noLonger = page.getByLabel("Show those no longer had");
  await noLonger.click();
  await expect(page).toHaveURL(/all=true/);
  await expect(noLonger).toBeChecked();
  const iron = page.getByTestId("thing-row").filter({ hasText: "Old pressing iron" });
  await expect(iron).toContainText("Broken");
  // Out of the totals still.
  await expect(hero).toContainText("₦615,000");
  await axeAt(page, "Things we have, with those no longer had");
});

test("a row marked sold with a price is money in", async ({ page }) => {
  await signUp(page);
  labels(page);
  const { sent } = thingsServer(page, [generator]);
  await page.goto("/money/things");
  await page.getByTestId("thing-row").filter({ hasText: "Generator" }).getByRole("link").click();
  await expect(page).toHaveURL(/\/money\/things\/t1$/);
  await expect(page.getByTestId("thing-hero")).toContainText("₦450,000");
  const facts = page.getByTestId("thing-facts");
  await expect(facts).toContainText("Machines");
  await expect(facts).toContainText("G-1");
  await expect(facts).toContainText("2 Nov 2027");
  await expect(facts.getByRole("link", { name: "PO-0100" })).toHaveAttribute("href", "/purchases/p1");
  const change = page.getByTestId("thing-change");
  await change.getByRole("radio", { name: "Sold" }).check();
  await change.getByLabel("What it sold for (optional)").fill("300,000");
  await change.getByLabel("Paid").selectOption("transfer");
  await axeAt(page, "a row's page, sold with a price");
  await change.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("thing-status")).toHaveText("Sold");
  expect(sent).toHaveLength(1);
  expect(sent[0]?.body).toEqual({ status: "sold", sale_price_kobo: 30_000_000, sale_paid_how: "transfer" });
  expect(sent[0]?.key).toBeTruthy();
  await expect(page.getByTestId("thing-hero")).toContainText("Kept in the history, out of the totals");
});

test("a cashier who does not manage money marks it sold without a price", async ({ page }) => {
  await signUp(page);
  await without(page, "invoice.manage");
  labels(page);
  const { sent } = thingsServer(page, [generator], false);
  await page.goto("/money/things/t1");
  const change = page.getByTestId("thing-change");
  await change.getByRole("radio", { name: "Sold" }).check();
  await expect(change.getByLabel("What it sold for (optional)")).toBeDisabled();
  await expect(change.getByTestId("sale-price-why")).toContainText("Only someone who manages money can record what it sold for");
  await axeAt(page, "a row's page, sold by a cashier");
  await change.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("thing-status")).toHaveText("Sold");
  expect(sent.map((s) => s.body)).toEqual([{ status: "sold" }]);
});

test("someone who may only look sees no Add and no changes", async ({ page }) => {
  await signUp(page);
  await without(page, "table.edit");
  labels(page);
  thingsServer(page, [generator]);
  await page.goto("/money/things");
  await expect(page.getByTestId("thing-row")).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Add something we have" })).toHaveCount(0);
  await page.goto("/money/things/t1");
  await expect(page.getByText("Someone who may buy for the business can change it.")).toBeVisible();
  await expect(page.getByTestId("thing-change")).toHaveCount(0);
});

test("Add something we have, by hand, with a new label", async ({ page }) => {
  await signUp(page);
  const made = labels(page);
  const { sent } = thingsServer(page, [generator]);
  await page.route("**/v1/stock/suppliers", (route) =>
    route.fulfill(json({ suppliers: [{ id: "s1", name: "Singer Ltd", phone: "", reached: "sms", owed_kobo: 0, open_orders: 0 }], settings: {} })),
  );
  await page.goto("/money/things");
  await page.getByRole("button", { name: "Add something we have" }).click();
  const form = page.getByTestId("add-thing");
  await form.getByRole("button", { name: "Add it" }).click();
  await expect(form.getByRole("alert")).toHaveText("Say what it is.");
  await form.getByLabel("What is it?").fill("Office desk");
  await form.getByLabel("Label", { exact: true }).fill("Furniture");
  await form.getByLabel(/From whom/).selectOption("s1");
  await form.getByLabel(/What it cost/).fill("85,000");
  await form.getByLabel(/When it came/).fill("2026-03-15");
  await form.getByLabel(/Serial number/).fill("D-7");
  await form.getByLabel(/Warranty ends/).fill("2028-03-15");
  await form.getByLabel(/Note/).fill("Bought before Taskiem");
  await axeAt(page, "Add something we have");
  await form.getByRole("button", { name: "Add it" }).click();
  await expect(form).toHaveCount(0);
  expect(made).toEqual(["Furniture"]);
  expect(sent[0]).toMatchObject({
    method: "POST",
    path: "/v1/things",
    body: { name: "Office desk", label_id: "lab-f", supplier_id: "s1", cost_kobo: 8_500_000, received_at: "2026-03-15T12:00:00+01:00", serial: "D-7", warranty_until: "2028-03-15", note: "Bought before Taskiem" },
  });
  await expect(page.getByTestId("things-group").filter({ hasText: "Furniture" })).toContainText("Office desk");
});

test("a row sold without a price gets its price later from someone who manages money", async ({ page }) => {
  await signUp(page);
  labels(page);
  const { sent } = thingsServer(page, [{ ...generator, status: "sold" }]);
  await page.goto("/money/things/t1");
  await expect(page.getByTestId("thing-status")).toHaveText("Sold");
  const change = page.getByTestId("thing-change");
  await expect(change.getByRole("radio", { name: "Sold" })).toBeChecked();
  const save = change.getByRole("button", { name: "Save" });
  await expect(save).toBeDisabled();
  await change.getByLabel("What it sold for (optional)").fill("300,000");
  await expect(save).toBeEnabled();
  await save.click();
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0]?.body).toEqual({ status: "sold", sale_price_kobo: 30_000_000, sale_paid_how: "cash" });
  // Its money recorded: it stays sold.
  await expect(change.getByTestId("sold-kept")).toHaveText("What it sold for is recorded, as money in, so it stays sold.");
  await expect(change.getByRole("radio", { name: "Have" })).toBeDisabled();
  await expect(change.getByRole("button", { name: "See money in" })).toBeVisible();
});

const purchaseWithLine = {
  id: "p1", reference: "PO-0100", supplier_id: "s1", supplier: "Singer Ltd", total_kobo: 45_000_000, status: "received",
  drafted_by: "user:ada", approved_by: "user:ada", sent_at: now, confirmed_at: now, confirmed_how: "owner", counter: null, counter_at: null,
  received_at: now, cancelled_at: null, received_kobo: 45_000_000, paid_kobo: 0, owed_kobo: 45_000_000, created_at: now,
  lines: [{ id: "c", position: 0, item_id: "c", item: "Generator", unit: "", quantity: 1, cost_kobo: 45_000_000, received: 1, uses: 1, treatment: "track", label_id: "lab-m" }],
};

test("an order line's rows: its own counts and its own sum, asked of the server by line", async ({ page }) => {
  await signUp(page);
  labels(page);
  const soldToo: Thing = { ...generator, id: "t5", name: "Generator (second)", status: "sold" };
  const { asked } = thingsServer(page, [generator, soldToo, overlock, fan]);
  await page.goto("/money/things?line=c");
  await expect.poll(() => asked.some((q) => new URLSearchParams(q).get("line") === "c")).toBe(true);
  await expect(page.getByTestId("things-of-line")).toBeVisible();
  const hero = page.getByTestId("things-hero");
  await expect(hero).toContainText("from this order line");
  await expect(hero).toContainText("1 thing");
  await expect(hero).toContainText("₦450,000");
  const groups = page.getByTestId("things-group");
  await expect(groups).toHaveCount(1);
  await expect(groups.first().getByRole("heading")).toContainText("Machines 1 thing, ₦450,000");
  await expect(page.getByTestId("thing-row")).toHaveCount(2);
  await axeAt(page, "an order line's rows");
});

test("a label merged into another shows once, its rows together", async ({ page }) => {
  await signUp(page);
  labels(page);
  // Equipment merged into Machines: the overlock keeps its own label_id.
  thingsServer(page, [generator, { ...overlock, label_id: "lab-e", label: "Machines" }]);
  await page.goto("/money/things");
  const groups = page.getByTestId("things-group");
  await expect(groups).toHaveCount(1);
  await expect(groups.first().getByRole("heading")).toContainText("Machines 2 things, ₦590,000");
  await expect(groups.first().getByTestId("thing-row")).toHaveCount(2);
});

test("without table.read, On Things we have is words, not a link", async ({ page }) => {
  await signUp(page);
  await without(page, "table.read");
  labels(page);
  await page.route("**/v1/stock/purchases/p1", (route) => route.fulfill(json({ purchase: purchaseWithLine })));
  await page.goto("/purchases/p1");
  const mark = page.getByTestId("line-mark");
  await expect(mark).toContainText("On Things we have");
  await expect(mark.getByRole("link")).toHaveCount(0);
});

test("an order line kept track of leads to its rows", async ({ page }) => {
  await signUp(page);
  labels(page);
  thingsServer(page, [generator, overlock]);
  await page.route("**/v1/stock/purchases/p1", (route) => route.fulfill(json({ purchase: purchaseWithLine })));
  await page.goto("/purchases/p1");
  await page.getByTestId("line-mark").getByRole("link", { name: "On Things we have" }).click();
  await expect(page).toHaveURL(/\/money\/things\?line=c$/);
  await expect(page.getByTestId("things-of-line")).toBeVisible();
  await expect(page.getByTestId("thing-row")).toHaveCount(1);
  await expect(page.getByTestId("thing-row")).toContainText("Generator");
});

test("spending by label in the money overview: Bought to keep apart, out of profit", async ({ page }) => {
  await signUp(page);
  let asked = "";
  await page.route("**/v1/money/spending?**", (route) => {
    asked = new URL(route.request().url()).search;
    return route.fulfill(
      json({
        from: "2026-11-01T00:00:00+01:00",
        to: "2026-11-23T00:00:00+01:00",
        total_kobo: 52_000_000,
        bought_to_sell_kobo: 6_000_000,
        bought_to_keep_kobo: 45_000_000,
        running_costs_kobo: 1_000_000,
        labels: [
          { label_id: "lab-m", name: "Machines", kobo: 45_000_000, bought_to_sell_kobo: 0, bought_to_keep_kobo: 45_000_000, running_costs_kobo: 0, entries: 1 },
          { label_id: "lab-s", name: "Stock bought", kobo: 6_000_000, bought_to_sell_kobo: 6_000_000, bought_to_keep_kobo: 0, running_costs_kobo: 0, entries: 2 },
          { label_id: null, name: "", kobo: 1_000_000, bought_to_sell_kobo: 0, bought_to_keep_kobo: 0, running_costs_kobo: 1_000_000, entries: 1 },
        ],
        profit: { in_kobo: 20_000_000, fees_kobo: 300_000, costs_kobo: 7_000_000, profit_kobo: 12_700_000 },
      }),
    );
  });
  await page.goto("/money?period=custom&from=2026-11-01&to=2026-11-22");
  const card = page.getByTestId("spending");
  await expect(card.getByRole("heading", { name: "Spending by label" })).toBeVisible();
  await expect.poll(() => asked).toBe("?from=2026-11-01&to=2026-11-22");
  await expect(card.getByTestId("spending-keep")).toContainText("₦450,000");
  await expect(card.getByTestId("spending-keep")).toContainText("left out of profit");
  await expect(card.getByTestId("spending-sell")).toContainText("₦60,000");
  await expect(card.getByTestId("spending-running")).toContainText("₦10,000");
  const rows = card.getByTestId("spending-label");
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText("Machines");
  await expect(rows.nth(0)).toContainText("₦450,000 bought to keep");
  await expect(rows.nth(1)).toContainText("bought to sell or make with");
  await expect(rows.nth(2)).toContainText("Not labelled");
  await expect(card.getByTestId("spending-profit")).toContainText("₦127,000 profit");
  await expect(card.getByTestId("spending-profit")).toContainText("Bought to keep is left out");
  await axeAt(page, "spending by label");
});

test("a sale refused and changed goes with a new Idempotency-Key; the same one tried again keeps its key", async ({ page }) => {
  await signUp(page);
  labels(page);
  const { sent } = thingsServer(page, [generator]);
  // The first two tries are refused.
  let refusals = 2;
  await page.route("**/v1/things/t1", async (route) => {
    const r = route.request();
    if (r.method() === "PATCH" && refusals > 0) {
      refusals--;
      sent.push({ method: "PATCH", path: "/v1/things/t1", body: r.postDataJSON(), key: r.headers()["idempotency-key"] });
      return route.fulfill(json({ error: "try again in a moment" }, 409));
    }
    return route.fallback();
  });
  await page.goto("/money/things/t1");
  const change = page.getByTestId("thing-change");
  await change.getByRole("radio", { name: "Sold" }).check();
  await change.getByLabel("What it sold for (optional)").fill("300,000");
  const save = change.getByRole("button", { name: "Save" });
  await save.click();
  await expect.poll(() => sent.length).toBe(1);
  await expect(change.getByRole("alert")).toContainText("try again in a moment");
  // The same sale again: the same key.
  await save.click();
  await expect.poll(() => sent.length).toBe(2);
  // A different price: a new key, and it is saved.
  await change.getByLabel("What it sold for (optional)").fill("250,000");
  await save.click();
  await expect.poll(() => sent.length).toBe(3);
  await expect(page.getByTestId("thing-status")).toHaveText("Sold");
  expect(sent.map((s) => (s.body as { sale_price_kobo: number }).sale_price_kobo)).toEqual([30_000_000, 30_000_000, 25_000_000]);
  expect(sent[0]?.key).toBeTruthy();
  expect(sent[1]?.key).toBe(sent[0]?.key);
  expect(sent[2]?.key).toBeTruthy();
  expect(sent[2]?.key).not.toBe(sent[0]?.key);
});

test("a row sold with its money recorded stays sold", async ({ page }) => {
  await signUp(page);
  labels(page);
  const { sent } = thingsServer(page, [{ ...generator, status: "sold", sale_entry_id: "e1" }]);
  await page.goto("/money/things/t1");
  await expect(page.getByTestId("thing-status")).toHaveText("Sold");
  const change = page.getByTestId("thing-change");
  await expect(change.getByTestId("sold-kept")).toHaveText("What it sold for is recorded, as money in, so it stays sold.");
  await expect(change.getByRole("radio", { name: "Sold" })).toBeChecked();
  for (const other of ["Have", "Broken", "Given away", "Gone"])
    await expect(change.getByRole("radio", { name: other, exact: true })).toBeDisabled();
  await expect(change.getByRole("radiogroup")).toHaveAccessibleDescription("What it sold for is recorded, as money in, so it stays sold.");
  await expect(change.getByRole("button", { name: "Save" })).toBeDisabled();
  await axeAt(page, "a row sold with its money recorded");
  expect(sent).toEqual([]);
});

test("without table.read, Things we have is not asked for", async ({ page }) => {
  await signUp(page);
  await without(page, "table.read");
  labels(page);
  thingsServer(page, [generator]);
  const asked: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).pathname.startsWith("/v1/things")) asked.push(r.url());
  });
  // Anything the page asked before a request of our own is seen before it.
  const drawnAndAsked = async () => {
    await Promise.all([
      page.waitForRequest((r) => r.url().includes("after-the-page")),
      page.evaluate(() => fetch("/favicon.ico?after-the-page").catch(() => undefined)),
    ]);
  };
  const words = page.getByText("Someone who may see your stock can see Things we have.");
  await page.goto("/money/things");
  await expect(words).toBeVisible();
  await drawnAndAsked();
  await page.goto("/money/things/t1");
  await expect(words).toBeVisible();
  await expect(page.getByTestId("thing-hero")).toHaveCount(0);
  await drawnAndAsked();
  expect(asked).toEqual([]);
});

test("the totals of the last filter are not shown while the next loads", async ({ page }) => {
  await signUp(page);
  labels(page);
  thingsServer(page, [generator, overlock, fan]);
  // All of them held back until let go.
  let letGo: () => void = () => undefined;
  const held = new Promise<void>((r) => (letGo = r));
  let holding = 0;
  await page.route("**/v1/things?**", async (route) => {
    if (!new URL(route.request().url()).searchParams.get("line")) {
      holding++;
      await held;
    }
    return route.fallback();
  });
  await page.goto("/money/things?line=c");
  const hero = page.getByTestId("things-hero");
  await expect(hero).toContainText("1 thing");
  await expect(hero).toContainText("₦450,000");
  await page.getByRole("link", { name: "See them all" }).click();
  await expect(page).toHaveURL(/\/money\/things$/);
  await expect.poll(() => holding).toBe(1);
  // Loading, not the order line's totals.
  await expect(page.locator(".skeleton")).toBeVisible();
  await expect(hero).toHaveCount(0);
  letGo();
  await expect(hero).toContainText("3 things");
  await expect(hero).toContainText("₦615,000");
});

// H2 g-things-menu: someone who may see stock (table.read) but not
// invoices opens Things we have from the menu, and Money is only that.
test("a staff member with table.read and no invoice rights opens Things we have from the menu", async ({ page }) => {
  await signUp(page);
  await without(page, "invoice.read", "invoice.manage");
  labels(page);
  thingsServer(page, [generator, overlock]);
  await page.goto("/home");
  await page.getByRole("button", { name: "Open menu" }).click();
  const nav = page.getByRole("navigation", { name: "Main" });
  await expect(nav.getByRole("link", { name: "Money", exact: true })).toHaveCount(0);
  await nav.getByRole("link", { name: "Things we have" }).click();
  await expect(page).toHaveURL(/\/money\/things$/);
  await expect(page.getByRole("heading", { level: 1, name: "Things we have" })).toBeVisible();
  await expect(page.getByTestId("thing-row")).toHaveCount(2);
  // Money's tabs are Things we have alone.
  const tabs = page.getByRole("navigation", { name: "Money" }).getByRole("link");
  await expect(tabs).toHaveCount(1);
  await expect(tabs.first()).toHaveText("Things we have");
  await axeAt(page, "Things we have without invoice rights");
  // Money itself leads there.
  await page.goto("/money");
  await expect(page).toHaveURL(/\/money\/things$/);
});

// H2 g-thing-extra: search on Things we have.
test("Things we have searched by name, supplier and serial", async ({ page }) => {
  await signUp(page);
  labels(page);
  thingsServer(page, [generator, overlock, fan]);
  await page.goto("/money/things");
  const search = page.getByTestId("things-search");
  await search.fill("singer");
  await expect(page.getByTestId("thing-row")).toHaveCount(2);
  await search.fill("G-1");
  await expect(page.getByTestId("thing-row")).toHaveCount(1);
  await expect(page.getByTestId("thing-row")).toContainText("Generator");
  await expect(page).toHaveURL(/q=G-1/);
  // The hero is still what the business has.
  await expect(page.getByTestId("things-hero")).toContainText("3 things");
  await axeAt(page, "Things we have searched");
  await search.fill("piano");
  await expect(page.getByTestId("thing-row")).toHaveCount(0);
  await expect(page.getByText("Nothing on Things we have matches “piano”.")).toBeVisible();
});

// H2 g-thing-extra: a row's supplier changed, and taken off.
test("a row's supplier changed and taken off", async ({ page }) => {
  await signUp(page);
  labels(page);
  const { sent } = thingsServer(page, [generator]);
  await page.route("**/v1/stock/suppliers", (route) =>
    route.fulfill(
      json({
        suppliers: [
          { id: "s1", name: "Singer Ltd", phone: "", reached: "sms", owed_kobo: 0, open_orders: 0 },
          { id: "s2", name: "Lister Power", phone: "", reached: "sms", owed_kobo: 0, open_orders: 0 },
        ],
        settings: {},
      }),
    ),
  );
  await page.goto("/money/things/t1");
  await page.getByRole("button", { name: "Change the details" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("From whom")).toHaveValue("s1");
  await dialog.getByLabel("From whom").selectOption("s2");
  await axeAt(page, "a row's details with its supplier");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByTestId("thing-facts")).toContainText("Lister Power");
  await page.getByRole("button", { name: "Change the details" }).click();
  await dialog.getByLabel("From whom").selectOption("");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByTestId("thing-facts")).toContainText("Not said");
  expect(sent.map((s) => s.body)).toEqual([{ supplier_id: "s2" }, { clear_supplier: true }]);
});

// H2 g-thing-suggest: Add something we have pre-selects the label
// suggested by the name, made when the row is added.
test("Add something we have pre-selects a label suggested by its name", async ({ page }) => {
  await signUp(page);
  const made = labels(page, { "office desk": "Furniture" });
  const { sent } = thingsServer(page, []);
  await page.route("**/v1/stock/suppliers", (route) => route.fulfill(json({ suppliers: [], settings: {} })));
  await page.goto("/money/things");
  await page.getByRole("button", { name: "Add something we have" }).click();
  const form = page.getByTestId("add-thing");
  await form.getByLabel("What is it?").fill("Office desk");
  await expect(form.getByLabel("Label", { exact: true })).toHaveValue("Furniture");
  await expect(form.getByRole("button", { name: "Furniture" })).toHaveAttribute("aria-pressed", "true");
  await axeAt(page, "Add something we have with a suggested label");
  await form.getByRole("button", { name: "Add it" }).click();
  await expect(form).toHaveCount(0);
  expect(made).toEqual(["Furniture"]);
  expect(sent[0]?.body).toMatchObject({ name: "Office desk", label_id: "lab-f" });
});

// H2 g-thing-extra: Things we have reached from the Stock page.
test("the Stock page leads to Things we have", async ({ page }) => {
  await signUp(page);
  labels(page);
  thingsServer(page, [generator, overlock]);
  // Stock kept, with nothing counted yet (no Xenibase here).
  await page.route("**/v1/stock", (route) => route.fulfill(json({ keeps_stock: true, catalogue: "products", items: [] })));
  await page.route("**/v1/stock/stock-items?**", (route) => route.fulfill(json({ keeps_stock: true, value_kobo: 0, low: 0, items: [] })));
  await page.route("**/v1/stock/movements?**", (route) => route.fulfill(json({ movements: [] })));
  await page.goto("/storefront/stock");
  const card = page.getByTestId("stock-things");
  await expect(card).toContainText("2 things you keep track of, one row each, that cost ₦590,000.");
  await axeAt(page, "the Stock page with Things we have");
  await card.getByRole("link", { name: "See Things we have" }).click();
  await expect(page).toHaveURL(/\/money\/things$/);
  await expect(page.getByTestId("thing-row")).toHaveCount(2);
});
