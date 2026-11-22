import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// What a business buys, U2a (docs/spec/what-a-business-buys.md §5, §6, §9):
// the question on each line of an order sheet ("<item> from <supplier>: How
// should we treat it?") and its memory, many to keep track of asked first,
// an order's lines answered on its page, what is remembered changed on the
// Suppliers page, and labels renamed and merged in Money. The server's side
// is tested in engine/stock and api (U1); here the API is answered by a
// small stand-in that remembers as the server does, at a phone's widths,
// passing axe in both themes.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
const now = "2026-11-22T09:00:00Z";

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: "Ada Fashion House",
      email: `u2a-${Date.now()}-${Math.random().toString(36).slice(2, 8)}@buying.test`,
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

interface Label {
  id: string;
  name: string;
  uses: number;
  merged_into?: string;
  created_by: string;
  created_at: string;
}

/** The business's labels, found or made by name, renamed and merged. */
function labelsServer(page: Page, start: Label[] = []) {
  const labels = [...start];
  const sent: { method: string; path: string; body: unknown }[] = [];
  const kept = () => labels.filter((l) => !l.merged_into).sort((a, b) => b.uses - a.uses);
  void page.route("**/v1/money/labels**", async (route: Route) => {
    const r = route.request();
    const path = new URL(r.url()).pathname;
    const body = r.postData() ? r.postDataJSON() : undefined;
    if (r.method() !== "GET") sent.push({ method: r.method(), path, body });
    if (r.method() === "GET") {
      const item = new URL(r.url()).searchParams.get("item") ?? "";
      const suggestions = /machine/i.test(item) && !labels.some((l) => l.name === "Machines") ? [{ name: "Machines" }] : [];
      return route.fulfill(json({ labels: kept(), suggestions }));
    }
    if (r.method() === "POST" && path === "/v1/money/labels") {
      const name = (body as { name: string }).name;
      let l = labels.find((x) => x.name.toLowerCase() === name.toLowerCase());
      if (!l) labels.push((l = { id: `lab-${labels.length + 1}`, name, uses: 0, created_by: "user:ada", created_at: now }));
      return route.fulfill(json(l, 201));
    }
    const id = path.split("/")[4];
    const l = labels.find((x) => x.id === id);
    if (!l) return route.fulfill(json({ error: "not found" }, 404));
    if (r.method() === "PATCH") l.name = (body as { name: string }).name;
    if (path.endsWith("/merge")) {
      const into = labels.find((x) => x.id === (body as { into: string }).into);
      l.merged_into = into?.id;
      if (into) into.uses += l.uses;
    }
    return route.fulfill(json(l));
  });
  return { labels, sent };
}

/** Stock items, and Things we have: none tracked before. */
async function stockAndThings(page: Page) {
  await page.route("**/v1/stock/stock-items", (route) =>
    route.fulfill(
      json({
        keeps_stock: true,
        value_kobo: 0,
        low: 0,
        items: [
          { id: "si-fabric", name: "Ankara fabric", unit: "yard", category: "", count: 40, cost_kobo: 150_000, low_level: 10, target_level: 60, supplier_id: null, supplier: "", group_of: "", group_name: "", low: false, suggest: null, value_kobo: 6_000_000, retired: false, sold_as: [] },
        ],
      }),
    ),
  );
  await page.route("**/v1/things**", (route) => route.fulfill(json({ things: [] })));
}

const machine = { item_id: "sm", name: "Singer heavy-duty sewing machine", unit: "", category: "Machines", price_kobo: 18_000_000, min_quantity: 1, available: true };
const bags = { item_id: "nb", name: "Nylon bags", unit: "", category: "Packaging", price_kobo: 2_000, min_quantity: 1, available: true };

test("a sewing machine from a price list: Keep track of it, Machines; the next order asks nothing", async ({ page }) => {
  await signUp(page);
  const { sent: labelWrites } = labelsServer(page);
  await stockAndThings(page);
  const memory: Record<string, unknown>[] = [];
  const orders: unknown[] = [];
  await page.route("**/v1/supplier-prices?**", (route) =>
    route.fulfill(json({ suppliers: [{ link: "l1", business: "Singer Ltd", name: "Trade prices", updated_at: now, items: [machine, bags] }] })),
  );
  await page.route("**/v1/item-links/l1", (route) => route.fulfill(json({ item_links: memory })));
  await page.route("**/v1/supplier-prices/l1/order", async (route) => {
    const body = route.request().postDataJSON() as { lines: Record<string, unknown>[] };
    orders.push(body);
    // Said the first time, it is remembered (as the server does).
    for (const l of body.lines)
      if (l.treatment && !memory.some((m) => m.seller_item_id === l.seller_item_id))
        memory.push({ link_id: "l1", seller_item_id: l.seller_item_id, name: machine.name, treatment: l.treatment, uses: 1, label_id: l.label_id, label: "Machines", updated_by: "user:ada", updated_at: now });
    return route.fulfill(json({ purchase: { id: `p${orders.length}`, lines: [] } }, 201));
  });
  await page.route("**/v1/stock/purchases/p*", (route) => route.fulfill(json({ error: "not found" }, 404)));

  await page.goto("/storefront/suppliers/l1/prices");
  await page.getByLabel(`How many ${machine.name}`).fill("1");
  const q = page.getByTestId("treat");
  await expect(q).toContainText(`${machine.name} from Singer Ltd`);
  await expect(q).toContainText("How should we treat it?");
  // Nothing tracked before and no stock item of its name: Just record it,
  // with the suggested label.
  await expect(q.getByRole("radio", { name: /Just record it/ })).toBeChecked();
  await expect(q.getByLabel("Label", { exact: true })).toHaveValue("Machines");
  await q.getByRole("radio", { name: /Keep track of it/ }).check();
  await q.getByLabel("Label", { exact: true }).fill("");
  await q.getByRole("button", { name: "Machines" }).click();
  await axeAt(page, "the question on a price list's line");
  await page.getByRole("button", { name: "Draft the order" }).click();
  await expect(page).toHaveURL(/\/purchases\/p1$/);
  expect(labelWrites).toEqual([{ method: "POST", path: "/v1/money/labels", body: { name: "Machines" } }]);
  expect(orders[0]).toEqual({ lines: [{ seller_item_id: "sm", quantity: 1, treatment: "track", label_id: "lab-1" }] });

  // The next order of the same machine asks nothing: it shows what is
  // remembered, to change for this order only.
  await page.goto("/storefront/suppliers/l1/prices");
  await page.getByLabel(`How many ${machine.name}`).fill("2");
  await expect(q).toContainText("Keep track of it · Machines");
  await expect(q.getByRole("radio")).toHaveCount(0);
  await expect(q.getByRole("button", { name: /Change for this order/ })).toBeVisible();
  await axeAt(page, "a remembered line");
  await page.getByRole("button", { name: "Draft the order" }).click();
  await expect(page).toHaveURL(/\/purchases\/p2$/);
  expect(orders[1]).toEqual({ lines: [{ seller_item_id: "sm", quantity: 2 }] });
});

test("60 nylon bags kept track of: asked first, with Just record them", async ({ page }) => {
  await signUp(page);
  labelsServer(page, [{ id: "lab-p", name: "Packaging", uses: 4, created_by: "user:ada", created_at: now }]);
  await stockAndThings(page);
  const orders: { lines: Record<string, unknown>[] }[] = [];
  await page.route("**/v1/supplier-prices?**", (route) =>
    route.fulfill(json({ suppliers: [{ link: "l1", business: "Singer Ltd", name: "Trade prices", updated_at: now, items: [machine, bags] }] })),
  );
  await page.route("**/v1/item-links/l1", (route) => route.fulfill(json({ item_links: [] })));
  await page.route("**/v1/supplier-prices/l1/order", async (route) => {
    const body = route.request().postDataJSON() as { lines: Record<string, unknown>[] };
    orders.push(body);
    const l = body.lines[0];
    if (l?.treatment === "track" && !l.track_many)
      return route.fulfill(
        json({ error: "put 60 Nylon bags on Things we have, one row each? Say so, or just record them", code: "track_ask", line: 0, item: "Nylon bags", quantity: 60, ask_over: 50, most: 200 }, 409),
      );
    return route.fulfill(json({ purchase: { id: "p9", lines: [] } }, 201));
  });
  await page.route("**/v1/stock/purchases/p9", (route) => route.fulfill(json({ error: "not found" }, 404)));

  await page.goto("/storefront/suppliers/l1/prices");
  await page.getByLabel("How many Nylon bags").fill("60");
  const q = page.getByTestId("treat");
  await q.getByRole("radio", { name: /Keep track of it/ }).check();
  await q.getByRole("button", { name: "Packaging" }).click();
  await page.getByRole("button", { name: "Draft the order" }).click();
  const ask = page.getByTestId("track-ask");
  await expect(ask).toContainText("Put 60 Nylon bags on Things we have, one row each?");
  await expect(ask.getByRole("button", { name: "Yes, one row each" })).toBeVisible();
  await axeAt(page, "many to keep track of, asked");
  await ask.getByRole("button", { name: "Just record them" }).click();
  await expect(page).toHaveURL(/\/purchases\/p9$/);
  expect(orders.map((o) => o.lines[0])).toEqual([
    { seller_item_id: "nb", quantity: 60, treatment: "track", label_id: "lab-p" },
    { seller_item_id: "nb", quantity: 60, treatment: "record", label_id: "lab-p" },
  ]);
});

test("an order's lines on its page: one asked, one changed for this order, one locked", async ({ page }) => {
  await signUp(page);
  labelsServer(page, [
    { id: "lab-m", name: "Machines", uses: 3, created_by: "user:ada", created_at: now },
    { id: "lab-s", name: "Stock bought", uses: 9, created_by: "user:ada", created_at: now },
  ]);
  await stockAndThings(page);
  const patches: { path: string; body: unknown }[] = [];
  const line = { unit: "", received: 0, uses: 1 };
  const purchase = {
    id: "p1", reference: "PO-0100", supplier_id: "s1", supplier: "Mama Nkechi Fabrics", total_kobo: 21_000_000, status: "sent",
    drafted_by: "user:ada", approved_by: "user:ada", sent_at: now, confirmed_at: null, confirmed_how: null, counter: null, counter_at: null,
    received_at: null, cancelled_at: null, received_kobo: 0, paid_kobo: 0, owed_kobo: 0, created_at: now,
    lines: [
      { ...line, id: "a", position: 0, item_id: "a", item: "Thread", quantity: 20, cost_kobo: 50_000, treatment: "record", unconfirmed: true },
      { ...line, id: "b", position: 1, item_id: "si-fabric", item: "Ankara fabric", quantity: 40, cost_kobo: 150_000, treatment: "stock", stock_item_id: "si-fabric", label_id: "lab-s" },
      { ...line, id: "c", position: 2, item_id: "c", item: "Overlock machine", quantity: 1, cost_kobo: 14_000_000, treatment: "track", label_id: "lab-m", received: 1 },
    ],
  };
  await page.route("**/v1/stock/purchases/p1", (route) => route.fulfill(json({ purchase })));
  await page.route("**/v1/stock/purchases/p1/lines/*", async (route) => {
    patches.push({ path: new URL(route.request().url()).pathname, body: route.request().postDataJSON() });
    return route.fulfill(json({ purchase }));
  });

  await page.goto("/purchases/p1");
  // The order card: each line's label, and where it went.
  const marks = page.getByTestId("line-mark");
  await expect(marks.nth(1)).toContainText("Stock bought");
  await expect(marks.nth(1)).toContainText("To stock");
  await expect(marks.nth(2)).toContainText("Machines");
  await expect(marks.nth(2)).toContainText("On Things we have");
  const card = page.getByTestId("purchase-treat");
  const qs = card.getByTestId("treat");
  await expect(qs).toHaveCount(3);
  await expect(qs.nth(0)).toContainText("How should we treat it?");
  await expect(qs.nth(1)).toContainText("Add to stock: Ankara fabric · Stock bought");
  await expect(qs.nth(2)).toContainText("It came, so it stays as it is.");
  await expect(qs.nth(2).getByRole("button")).toHaveCount(0);
  // Thread: Add to stock, a new stock item we do not sell.
  await qs.nth(0).getByRole("radio", { name: /Add to stock/ }).check();
  await expect(qs.nth(0).getByLabel("Which stock item?")).toHaveValue("new");
  await expect(card.getByRole("button", { name: "Save" })).toBeDisabled();
  await qs.nth(0).getByRole("radio", { name: /No, we use it/ }).check();
  await page.route("**/v1/stock/items", (route) => route.fulfill(json({ item: { id: "si-thread", name: "Thread", material: true } }, 201)));
  // The fabric: this order only, kept for next time.
  await qs.nth(1).getByRole("button", { name: /Change for this order/ }).click();
  await qs.nth(1).getByRole("radio", { name: /Just record it/ }).check();
  await qs.nth(1).getByLabel("Remember for next time").check();
  await axeAt(page, "an order's lines asked on its page");
  await card.getByRole("button", { name: "Save" }).click();
  await expect.poll(() => patches.length).toBe(2);
  expect(patches).toEqual([
    { path: "/v1/stock/purchases/p1/lines/0", body: { treatment: "stock", stock_item_id: "si-thread" } },
    { path: "/v1/stock/purchases/p1/lines/1", body: { treatment: "record", label_id: "lab-s", remember: true } },
  ]);
});

test("what is remembered, changed and forgotten on the Suppliers page", async ({ page }) => {
  await signUp(page);
  labelsServer(page, [{ id: "lab-m", name: "Machines", uses: 3, created_by: "user:ada", created_at: now }]);
  await stockAndThings(page);
  const writes: { method: string; path: string; body: unknown }[] = [];
  const supplier = (id: string, name: string, link?: string) => ({
    id, name, phone: "0803 123 4567", reached: link ? "link" : "sms", link_id: link ?? null, owed_kobo: 0, open: 0, invited_at: null, created_at: now, retired_at: null,
  });
  await page.route("**/v1/stock/suppliers", (route) =>
    route.fulfill(json({ suppliers: [supplier("s1", "Singer Ltd", "l1"), supplier("s2", "Total filling station")], settings: { purchase_limit_kobo: null } })),
  );
  await page.route("**/v1/bills", (route) => route.fulfill(json({ bills: [] })));
  await page.route("**/v1/stock/deliver-to", (route) => route.fulfill(json({ address: "", phone: "" })));
  const links = [{ link_id: "l1", seller_item_id: "sm", name: machine.name, treatment: "track", uses: 1, label_id: "lab-m", label: "Machines", updated_by: "user:ada", updated_at: now }];
  const items = [{ supplier_id: "s2", name_key: "diesel", name: "Diesel", treatment: "record", uses: 1, updated_by: "user:ada", updated_at: now }];
  await page.route("**/v1/item-links/l1**", async (route) => {
    const r = route.request();
    if (r.method() !== "GET") writes.push({ method: r.method(), path: new URL(r.url()).pathname, body: r.postData() ? r.postDataJSON() : undefined });
    return route.fulfill(json({ item_links: links }));
  });
  await page.route("**/v1/stock/suppliers/s2/items**", async (route) => {
    const r = route.request();
    const u = new URL(r.url());
    if (r.method() !== "GET") writes.push({ method: r.method(), path: u.pathname + u.search, body: r.postData() ? r.postDataJSON() : undefined });
    if (r.method() === "DELETE") {
      items.length = 0;
      return route.fulfill({ status: 204 });
    }
    return route.fulfill(json({ items }));
  });

  await page.goto("/storefront/suppliers");
  await page.getByRole("button", { name: "What you buy from Singer Ltd" }).click();
  const dialog = page.getByRole("dialog");
  const row = dialog.getByTestId("supplier-item");
  await expect(row).toContainText(machine.name);
  await expect(row).toContainText("Keep track of it · Machines");
  await axeAt(page, "a linked supplier's items");
  await row.getByRole("button", { name: `Change ${machine.name}` }).click();
  const q = dialog.getByTestId("treat");
  await q.getByRole("radio", { name: /Add to stock/ }).check();
  await q.getByLabel("Which stock item?").selectOption("si-fabric");
  await axeAt(page, "a supplier's item changed");
  await dialog.getByRole("button", { name: `Save ${machine.name}` }).click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toEqual({
    method: "PUT",
    path: "/v1/item-links/l1",
    body: { seller_item_id: "sm", treatment: "stock", stock_item_id: "si-fabric", label_id: "lab-m" },
  });
  await dialog.getByRole("button", { name: "Close", exact: true }).click();

  // A supplier not on Taskiem: its items by name, forgotten.
  await page.getByRole("button", { name: "What you buy from Total filling station" }).click();
  await expect(dialog.getByTestId("supplier-item")).toContainText("Just record it · Not labelled");
  page.once("dialog", (d) => void d.accept());
  await dialog.getByRole("button", { name: "Forget Diesel" }).click();
  await expect(dialog).toContainText("Nothing yet");
  expect(writes[1]).toEqual({ method: "DELETE", path: "/v1/stock/suppliers/s2/items?name=Diesel", body: undefined });
});

test("labels in Money: renamed, and merged into another", async ({ page }) => {
  await signUp(page);
  const { sent } = labelsServer(page, [
    { id: "lab-w", name: "Staff welfare", uses: 6, created_by: "user:ada", created_at: now },
    { id: "lab-s", name: "Snacks", uses: 2, created_by: "user:ada", created_at: now },
    { id: "lab-m", name: "Machins", uses: 1, created_by: "user:ada", created_at: now },
  ]);
  await page.goto("/money/labels");
  await expect(page.getByRole("navigation", { name: "Money" }).getByRole("link", { name: "Labels" })).toBeVisible();
  const rows = page.getByTestId("spend-label");
  await expect(rows).toHaveCount(3);
  await expect(rows.first()).toContainText("Staff welfare");
  await expect(rows.first()).toContainText("Used 6 times");
  await axeAt(page, "/money/labels");

  await page.getByRole("button", { name: "Rename Machins" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("New name").fill("staff welfare");
  await expect(dialog).toContainText("Staff welfare is already a label: merge them instead.");
  await dialog.getByLabel("New name").fill("Machines");
  await axeAt(page, "renaming a label");
  await dialog.getByRole("button", { name: "Rename" }).click();
  await expect(rows.filter({ hasText: "Machines" })).toHaveCount(1);

  await page.getByRole("button", { name: "Merge Snacks" }).click();
  await dialog.getByLabel("Into").selectOption({ label: "Staff welfare" });
  await axeAt(page, "merging a label");
  await dialog.getByRole("button", { name: "Merge" }).click();
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText("Used 8 times");
  expect(sent).toEqual([
    { method: "PATCH", path: "/v1/money/labels/lab-m", body: { name: "Machines" } },
    { method: "POST", path: "/v1/money/labels/lab-s/merge", body: { into: "lab-w" } },
  ]);
});
