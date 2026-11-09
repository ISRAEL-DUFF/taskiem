import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Chat tools, phase 4 (docs/spec/chat-tools.md §6): the Taskiem
// conversation ends with alerts you can act on and approvals. Each alert's
// button opens the right tool, filled in, or acts in place; an approval
// cannot pass without its confirmation. The alerts and the tools' own APIs
// are answered here (api/chat_alerts_test.go reads the real ones).
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

async function owner(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: `Ada Kitchen ${letters()}`, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@alerts.test`, name: "Ada Owner", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
  await page.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(300);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
const approval = (step_up: string | null) => ({
  run_id: "11111111-1111-4111-8111-111111111111",
  step_id: "ok",
  step_name: "Pay Bayo Rice Mills",
  workflow: "Pay supplier bills",
  environment: "prod",
  role: "owner",
  required: 1,
  approvals: 0,
  level: 1,
  levels: 1,
  step_up,
  policy: null,
  sole_approver: false,
  requested_at: ago(2),
  timeout_at: null,
  subject: { amount_kobo: 45_000_000, account_name: "Bayo Rice Mills", account_number: "0123452291", bank_name: "GTBank", invoice: "INV-2291" },
});
const ALERTS = [
  { key: "low_stock:s1", kind: "low_stock", at: ago(30), title: "Rice, 25kg bag is running low", line: "4 bags left · you reorder at 10", id: "s1",
    reorder: { link: "l1", supplier: "Bayo Rice Mills", seller_item_id: "p2", quantity: 20, price_kobo: 4_100_000 } },
  { key: "low_stock:s3", kind: "low_stock", at: ago(29), title: "Garri, Ijebu is running low", line: "1 bag left · you reorder at 5", id: "s3" },
  { key: "overdue:i1", kind: "overdue", at: ago(28), title: "Invoice INV-0042 is overdue", line: "B••• · ₦90,000 to pay · due 1 Nov", id: "i1", reference: "INV-0042", amount_kobo: 9_000_000 },
  { key: "new_order:o1", kind: "new_order", at: ago(20), title: "New order AD-7Q4K2P from Rivers Foods", line: "₦820,000", id: "o1", reference: "AD-7Q4K2P", amount_kobo: 82_000_000 },
  { key: "rider:r1", kind: "rider", at: ago(15), title: "Order ADA-1234 needs a rider", line: "Paid ₦6,500 · for delivery", id: "r1", reference: "ADA-1234", amount_kobo: 650_000 },
  { key: "unmatched:e1", kind: "unmatched", at: ago(10), title: "A payment of ₦9,000 is not matched yet", line: "Reference VA-9 · 6 Nov, 14:20", id: "e1", reference: "VA-9", amount_kobo: 900_000 },
  { key: "approval:run/ok", kind: "approval", at: ago(2), title: "Pay Bayo Rice Mills", line: "Asked by Tunde (cashier) · Pay supplier bills", id: "run",
    who: "Tunde (cashier)", approval: approval(null) },
];

/** The alerts and every API their buttons reach, answered and remembered. */
async function answer(page: Page, alerts: unknown[] = ALERTS) {
  const sent: { path: string; body: unknown; key?: string }[] = [];
  const remember = async (r: Route, reply: unknown, status = 200) => {
    const q = r.request();
    sent.push({ path: new URL(q.url()).pathname, body: q.postData() ? q.postDataJSON() : undefined, key: q.headers()["idempotency-key"] });
    await r.fulfill(json(reply, status));
  };
  let list = alerts;
  await page.route("**/v1/chat-alerts", (r) => r.fulfill(json({ alerts: list })));
  await page.route("**/v1/chat-alerts/later", (r) => remember(r, { key: "x", until: ago(-240) }));
  const items = [
    { id: "s1", name: "Rice, 25kg bag", available: true, stock: 4, low: true, unit: "bag", price_kobo: 4_300_000, reorder_level: 10 },
    { id: "s3", name: "Garri, Ijebu", available: true, stock: 1, low: true, unit: "bag", price_kobo: 3_200_000, reorder_level: 5 },
  ];
  await page.route("**/v1/stock", (r) => r.fulfill(json({ keeps_stock: true, catalogue: "products", items })));
  await page.route("**/v1/stock/movements", (r) => remember(r, { movement: { id: "m1" } }));
  await page.route("**/v1/supplier-prices?*", (r) =>
    r.fulfill(json({ suppliers: [{ link: "l1", business: "b", name: "Trade", updated_at: ago(60), items: [
      { item_id: "p1", name: "Rice, 50kg bag", unit: "bag", price_kobo: 7_800_000, min_quantity: 2, available: true },
      { item_id: "p2", name: "Rice, 25kg bag", unit: "bag", price_kobo: 4_100_000, min_quantity: 5, available: true },
    ] }] })),
  );
  await page.route("**/v1/item-links/l1", (r) => r.fulfill(json({ item_links: [{ link: "l1", item_id: "s1", seller_item_id: "p2", seller_item: "Rice, 25kg bag" }] })));
  await page.route("**/v1/stock/suppliers", (r) => r.fulfill(json({ suppliers: [{ id: "sup", name: "Bayo Rice Mills", link_id: "l1" }] })));
  await page.route("**/v1/stock/purchases", (r) => r.fulfill(json({ purchases: [] })));
  await page.route("**/v1/invoices/i1/send", (r) => remember(r, { invoice: { id: "i1" } }));
  await page.route("**/v1/trade-orders", (r) =>
    r.fulfill(json({ orders: [{ id: "o1", order_reference: "AD-7Q4K2P", buyer_name: "Rivers Foods", total_kobo: 82_000_000, status: "received",
      lines: [{ item: "Rice, 25kg bag", quantity: 20, price_kobo: 4_100_000 }] }] })),
  );
  await page.route("**/v1/trade-orders/o1/accept", (r) => remember(r, { order: {} }));
  await page.route("**/v1/riders", (r) =>
    r.fulfill(json({ riders: [{ id: "rd1", name: "Musa Bello", active: true, zones: ["Lekki"] }, { id: "rd2", name: "Off Duty", active: false, zones: [] }] })),
  );
  let offerStatus = "offered";
  await page.route("**/v1/storefront/orders/ADA-1234/offer", (r) => remember(r, { status: offerStatus, rider: offerStatus === "offered" ? "Musa Bello" : undefined }));
  await page.route("**/v1/chat-alerts/match-options", (r) =>
    r.fulfill(json({ options: [
      { kind: "invoice", number: "INV-0007", customer: "C•••", due_kobo: 2_000_000 },
      { kind: "invoice", number: "INV-0009", customer: "B•••", due_kobo: 900_000 },
      { kind: "order", number: "ADA-2001", customer: "E•••", due_kobo: 1_500_000 },
    ] })),
  );
  await page.route("**/v1/money/entries/e1/match", (r) => remember(r, { payments: [] }));
  await page.route("**/v1/approvals/*/ok", async (r) => {
    const body = r.request().postDataJSON() as { decision: string; totp?: string };
    // The rules ask for a code on this one: refused without it.
    if (list.some((a) => (a as { approval?: { step_up: string | null } }).approval?.step_up === "totp") && body.decision === "approved" && !body.totp)
      return remember(r, { error: "this approval needs your passkey or a code", step_up: "totp" }, 403);
    return remember(r, { status: body.decision });
  });
  return { sent, set: (l: unknown[]) => (list = l), offerAs: (st: string) => (offerStatus = st) };
}

for (const [size, viewport] of [
  ["a phone", { width: 390, height: 844 }],
  ["a computer", { width: 1440, height: 900 }],
] as const) {
  test(`alerts on ${size}: each card's button opens its tool, filled in, or acts`, async ({ browser }) => {
    test.setTimeout(180_000);
    const page = await (await browser.newContext({ viewport })).newPage();
    await owner(page);
    const { sent } = await answer(page);
    await page.goto("/chat?with=taskiem");
    const cards = page.getByTestId("chat-alerts");
    await expect(cards.getByTestId("chat-alert")).toHaveCount(6);
    await expect(cards.getByTestId("chat-approval")).toHaveCount(1);
    const approvalCard = cards.getByTestId("chat-approval");
    await expect(approvalCard).toContainText("Needs your OK");
    await expect(approvalCard).toContainText("Tunde (cashier) asks: Pay Bayo Rice Mills");
    await expect(approvalCard).toContainText("₦450,000");
    await expect(approvalCard).toContainText("to Bayo Rice Mills");
    await expect(approvalCard).toContainText("2 min ago");
    await axeBoth(page, "alert cards");
    if (size === "a phone") {
      await page.waitForTimeout(500);
      await page.screenshot({ path: "test-results/alerts-phone.png" });
    }
    const card = (title: string) => cards.getByRole("article", { name: title });

    // Low stock: Reorder opens the Order sheet with the quantity in the bag.
    await card("Rice, 25kg bag is running low").getByRole("button", { name: /^Reorder 20 from Bayo Rice Mills/ }).click();
    const order = page.getByTestId("order-sheet");
    await expect(order.getByRole("heading", { name: "Order from Bayo Rice Mills" })).toBeVisible();
    await expect(order.getByRole("group", { name: "How many Rice, 25kg bag" })).toContainText("20");
    await expect(order.getByTestId("order-bag")).toContainText("₦820,000");
    await order.getByRole("button", { name: "Close" }).first().click();
    await expect(order).toHaveCount(0);

    // Low, with no supplier on Taskiem: Record stock in, at its form.
    await card("Garri, Ijebu is running low").getByRole("button", { name: /^Record stock in/ }).click();
    const sheet = page.getByTestId("tool-sheet");
    await expect(sheet.getByRole("heading", { name: "Garri, Ijebu" })).toBeVisible();
    await sheet.getByRole("button", { name: /^Close/ }).click();
    await expect(sheet).toHaveCount(0);

    // Overdue: the reminder goes, once, and the card leaves.
    await card("Invoice INV-0042 is overdue").getByRole("button", { name: /^Send reminder/ }).click();
    await expect(card("Invoice INV-0042 is overdue")).toHaveCount(0);
    const reminder = sent.find((x) => x.path === "/v1/invoices/i1/send");
    expect(reminder?.key).toBeTruthy();
    // Put off for a day, so it does not come straight back.
    expect(sent.find((x) => x.path === "/v1/chat-alerts/later")?.body).toEqual({ key: "overdue:i1", hours: 24 });

    // A new order: its lines first, in the Orders tool, and Accept there.
    await card("New order AD-7Q4K2P from Rivers Foods").getByRole("button", { name: /^Review order/ }).click();
    await expect(sheet.getByRole("heading", { name: "AD-7Q4K2P" })).toBeVisible();
    await expect(sheet).toContainText("20 × Rice, 25kg bag");
    await sheet.getByRole("button", { name: "Accept" }).click();
    await expect(sheet).toHaveCount(0, { timeout: 5000 });
    expect(sent.some((x) => x.path === "/v1/trade-orders/o1/accept")).toBe(true);

    // A rider: the riders on duty, Musa chosen.
    await card("Order ADA-1234 needs a rider").getByRole("button", { name: /^Assign rider/ }).click();
    const riders = page.getByTestId("rider-sheet");
    await expect(riders.getByRole("button", { name: /Next in turn/ })).toBeVisible();
    await expect(riders).not.toContainText("Off Duty");
    await axeBoth(page, "assigning a rider");
    await riders.getByRole("button", { name: /Musa Bello/ }).click();
    await expect(riders).toHaveCount(0, { timeout: 5000 });
    expect(sent.find((x) => x.path === "/v1/storefront/orders/ADA-1234/offer")?.body).toEqual({ rider_id: "rd1" });

    // A payment: the open invoices, the same amount first.
    await card("A payment of ₦9,000 is not matched yet").getByRole("button", { name: /^Match it/ }).click();
    const match = page.getByTestId("match-sheet");
    const rows = match.getByRole("button", { name: /INV-|Order / });
    await expect(rows).toHaveCount(3);
    await expect(rows.first()).toContainText("INV-0009");
    await expect(rows.first()).toContainText("Same amount");
    await rows.first().click();
    await expect(match).toHaveCount(0, { timeout: 5000 });
    expect(sent.find((x) => x.path === "/v1/money/entries/e1/match")?.body).toEqual({ invoice: "INV-0009" });

    // The approval: confirmed in its sheet.
    await approvalCard.getByRole("button", { name: /^Approve/ }).click();
    const ok = page.getByTestId("approve-sheet");
    await expect(ok.getByRole("heading", { name: "Approve it?" })).toBeVisible();
    await expect(ok).toContainText("Account");
    await axeBoth(page, "approving");
    await ok.getByLabel("A comment (optional, recorded)").fill("Checked with Bayo");
    await ok.getByRole("button", { name: "Approve", exact: true }).click();
    await expect(ok).toHaveCount(0, { timeout: 5000 });
    expect(sent.find((x) => x.path.startsWith("/v1/approvals/"))?.body).toEqual({ decision: "approved", comment: "Checked with Bayo" });
  });
}

test("Later puts a card off, and Decline is confirmed too", async ({ browser }) => {
  test.setTimeout(120_000);
  const page = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await owner(page);
  const { sent } = await answer(page);
  await page.goto("/chat?with=taskiem");
  const cards = page.getByTestId("chat-alerts");
  await cards.getByRole("article", { name: "Order ADA-1234 needs a rider" }).getByRole("button", { name: /^Later/ }).click();
  await expect(cards.getByRole("article", { name: "Order ADA-1234 needs a rider" })).toHaveCount(0);
  expect(sent.find((x) => x.path === "/v1/chat-alerts/later")?.body).toEqual({ key: "rider:r1" });
  await cards.getByTestId("chat-approval").getByRole("button", { name: /^Decline/ }).click();
  const sheet = page.getByTestId("approve-sheet");
  await expect(sheet.getByRole("heading", { name: "Decline it?" })).toBeVisible();
  await sheet.getByRole("button", { name: "Decline", exact: true }).click();
  await expect(sheet).toHaveCount(0, { timeout: 5000 });
  expect(sent.find((x) => x.path.startsWith("/v1/approvals/"))?.body).toEqual({ decision: "rejected" });
});

test("an approval whose rules ask for a code cannot pass without it", async ({ browser }) => {
  test.setTimeout(120_000);
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
  // No passkeys in this browser: the code is the way.
  await context.addInitScript(() => delete (window as { PublicKeyCredential?: unknown }).PublicKeyCredential);
  const page = await context.newPage();
  await owner(page);
  const { sent } = await answer(page, [{ ...ALERTS[6], approval: approval("totp") }]);
  await page.goto("/chat?with=taskiem");
  const card = page.getByTestId("chat-approval");
  await expect(card).toContainText("Needs your passkey or a code");
  await card.getByRole("button", { name: /^Approve/ }).click();
  const sheet = page.getByTestId("approve-sheet");
  const approve = sheet.getByRole("button", { name: "Approve", exact: true });
  await expect(approve).toBeDisabled();
  await sheet.getByLabel("Or a code from your authenticator app").fill("12345");
  await expect(approve).toBeDisabled();
  await sheet.getByLabel("Or a code from your authenticator app").fill("123456");
  await approve.click();
  await expect(sheet).toHaveCount(0, { timeout: 5000 });
  const decided = sent.filter((x) => x.path.startsWith("/v1/approvals/"));
  expect(decided).toHaveLength(1);
  expect(decided[0]?.body).toEqual({ decision: "approved", totp: "123456" });
});

test("with chat tools off there are no alert cards", async ({ browser }) => {
  const page = await (await browser.newContext()).newPage();
  await owner(page);
  await page.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: false } });
  await answer(page);
  await page.goto("/chat?with=taskiem");
  await expect(page.getByTestId("taskiem-pane")).toBeVisible();
  await expect(page.getByTestId("chat-alerts")).toHaveCount(0);
});

test("a decline asks for the code too; an offer nobody took says so and the card stays", async ({ browser }) => {
  test.setTimeout(120_000);
  const context = await browser.newContext({ viewport: { width: 360, height: 780 } });
  await context.addInitScript(() => delete (window as { PublicKeyCredential?: unknown }).PublicKeyCredential);
  const page = await context.newPage();
  await owner(page);
  const { sent, offerAs } = await answer(page, [ALERTS[4], { ...ALERTS[6], approval: { ...approval("totp"), levels: 2, required: 2 } }]);
  await page.goto("/chat?with=taskiem");
  const card = page.getByTestId("chat-approval");
  await expect(card).toContainText("Level 1 of 2");
  await expect(card).toContainText("0 of 2 approvals");
  // Nothing scrolls sideways on a small phone.
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await card.getByRole("button", { name: /^Decline/ }).click();
  const sheet = page.getByTestId("approve-sheet");
  const decline = sheet.getByRole("button", { name: "Decline", exact: true });
  await expect(decline).toBeDisabled();
  await sheet.getByLabel("Or a code from your authenticator app").fill("654321");
  await decline.click();
  await expect(sheet).toHaveCount(0, { timeout: 5000 });
  expect(sent.find((x) => x.path.startsWith("/v1/approvals/"))?.body).toEqual({ decision: "rejected", totp: "654321" });

  offerAs("no_rider");
  await page.getByTestId("chat-alerts").getByRole("button", { name: /^Assign rider/ }).click();
  const riders = page.getByTestId("rider-sheet");
  await riders.getByRole("button", { name: /Next in turn/ }).click();
  await expect(riders.getByRole("status")).toContainText("No rider could take it");
  await expect(riders).toBeVisible();
  await riders.getByRole("button", { name: /^Close/ }).click();
  await expect(page.getByTestId("chat-alerts").getByRole("article", { name: "Order ADA-1234 needs a rider" })).toBeVisible();
});

test("the Taskiem row says how many things need you", async ({ browser }) => {
  const page = await (await browser.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
  await owner(page);
  await answer(page);
  await page.goto("/chat");
  await expect(page.getByTestId("taskiem-waiting")).toHaveText("7 things need you");
});

test("Give this to… on a low stock card makes a task about the item", async ({ browser }) => {
  const page = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await owner(page);
  await answer(page);
  const given: unknown[] = [];
  await page.route("**/v1/staff/tasks", (r) =>
    r.request().method() === "POST"
      ? (given.push(r.request().postDataJSON()), r.fulfill(json({ task: null, repeat_id: null }, 201)))
      : r.fulfill(json({ tasks: [], repeats: [], give: true, people: [{ id: "u2", name: "Tunde Bakare", roles: ["cashier"] }] })),
  );
  await page.goto("/chat?with=taskiem");
  const garri = page.getByTestId("chat-alerts").getByRole("article", { name: "Garri, Ijebu is running low" });
  await garri.getByRole("button", { name: /^Give this to/ }).click();
  const form = page.getByRole("dialog", { name: "Give this to someone" });
  await expect(form.getByLabel("What is to be done")).toHaveValue("Restock Garri, Ijebu");
  await form.getByLabel("Who it is for").selectOption({ label: "Tunde Bakare" });
  await form.getByRole("button", { name: "Give the task" }).click();
  await expect(form).toHaveCount(0);
  expect(given).toEqual([{ what: "Restock Garri, Ijebu", photo: false, to_user: "u2", link: { kind: "stock", id: "s3", name: "Garri, Ijebu" } }]);
});
