import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Phase 2 additions (docs/spec/chat-five-more-flows.md): invoices and the
// money log as chat tools, worked on as orders and stock are, and "/" to
// tag an invoice, a product, an order or a customer. The e2e server has no
// invoicing rail, so the invoice and money endpoints are answered here
// (api/chat_money_test.go reads the real ones).
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

async function owner(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: `Ada Kitchen ${letters()}`, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@money.test`, name: "Ada Obi", password: "correct horse battery" },
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

const now = new Date().toISOString();
const INVOICES = [
  { id: "i1", number: "INV-0001", status: "sent", kind: "invoice", customer: { name: "B•••" }, lines: [{ description: "Catering for 50", quantity: 1, unit_kobo: 4_500_000 }],
    total_kobo: 4_500_000, paid_kobo: 0, due_kobo: 4_500_000, currency: "NGN", connection: "", fee_paid_by: "business", allow_part_payments: true,
    sent_at: now, paid_at: null, voided_at: null, created_by: "x", created_at: now },
  { id: "i2", number: "INV-0002", status: "overdue", kind: "invoice", customer: { name: "T•••" }, lines: [{ description: "Chairs", quantity: 10, unit_kobo: 120_000 }],
    total_kobo: 1_200_000, paid_kobo: 0, due_kobo: 1_200_000, currency: "NGN", connection: "", fee_paid_by: "business", allow_part_payments: true,
    sent_at: now, paid_at: null, voided_at: null, created_by: "x", created_at: now },
  { id: "i3", number: "INV-0003", status: "paid", kind: "invoice", customer: { name: "K•••" }, lines: [],
    total_kobo: 900_000, paid_kobo: 900_000, due_kobo: 0, currency: "NGN", connection: "", fee_paid_by: "business", allow_part_payments: true,
    sent_at: now, paid_at: now, voided_at: null, created_by: "x", created_at: now },
];
const ENTRIES = [
  { id: "e1", kind: "cash", direction: "in", amount_kobo: 2_500_000, status: "settled", channel: "cash", when: now, description: "Cash from Bisi", left_kobo: 2_500_000, to_match: false },
  { id: "e2", kind: "expense", direction: "out", amount_kobo: 300_000, status: "settled", channel: "cash", when: now, description: "Diesel", vendor: "Total", to_match: false },
  { id: "e3", kind: "payment", direction: "in", amount_kobo: 900_000, status: "settled", channel: "paystack", when: now, left_kobo: 0, towards: "invoice INV-0003", to_match: false },
];

async function answer(page: Page) {
  const sent: { path: string; body: unknown; key?: string }[] = [];
  const remember = async (r: Route, reply: unknown, status = 200) => {
    const q = r.request();
    sent.push({ path: new URL(q.url()).pathname, body: q.postData() ? q.postDataJSON() : undefined, key: q.headers()["idempotency-key"] });
    await r.fulfill(json(reply, status));
  };
  await page.route(/\/v1\/invoices(\?.*)?$/, (r) =>
    r.request().method() === "GET" ? r.fulfill(json({ invoices: INVOICES })) : remember(r, { ...INVOICES[0], id: "i9", number: "INV-0009", status: "draft" }, 201),
  );
  await page.route(/\/v1\/invoices\/[^/]+\/(send|void)$/, (r) => remember(r, { invoice: INVOICES[0] }));
  await page.route(/\/v1\/money\/entries(\?.*)?$/, (r) =>
    r.request().method() === "GET" ? r.fulfill(json({ entries: ENTRIES })) : remember(r, { entry_id: "e9", created: true }, 201),
  );
  await page.route(/\/v1\/money\/entries\/[^/]+\/match$/, (r) => remember(r, { ok: true }));
  await page.route("**/v1/chat-refs", (r) =>
    r.fulfill(json({ refs: [
      { kind: "invoice", id: "i1", name: "INV-0001", party: "B•••", when: now, total_kobo: 4_500_000, status: "sent" },
      { kind: "invoice", id: "i2", name: "INV-0002", party: "Tola's Kitchen", when: "2026-11-03T10:00:00Z", total_kobo: 1_200_000, status: "overdue" },
      { kind: "bill", id: "b1", name: "INV-0420", party: "Dangote Stores", when: now, total_kobo: 7_800_000, status: "open" },
      { kind: "order", id: "o1", name: "AK-7QX4KD", party: "A•••", when: now, total_kobo: 1_330_000, status: "paid" },
      { kind: "customer", id: "c1", name: "Bisi Okafor", party: "•••0555", when: now },
    ] })),
  );
  await page.route(/\/v1\/chat-card\?/, (r) => {
    const q = new URL(r.request().url()).searchParams;
    if (q.get("kind") === "invoice")
      return r.fulfill(json({ kind: "invoice", id: "i2", name: "INV-0002", party: "Tola's Kitchen", when: "2026-11-03T10:00:00Z", total_kobo: 1_200_000, status: "overdue",
        due_on: "2026-11-08", paid_kobo: 300_000, due_kobo: 900_000, lines: [{ name: "Chairs", quantity: 10, total_kobo: 1_000_000 }, { name: "Tables", quantity: 2, total_kobo: 200_000 }], action: "remind" }));
    if (q.get("kind") === "customer")
      return r.fulfill(json({ kind: "customer", id: "c1", name: "Bisi Okafor", party: "•••0555", when: now, orders: 3, owes_kobo: 450_000,
        last_order: { reference: "AK-7QX4KD", total_kobo: 1_330_000, status: "paid", when: now }, action: "message" }));
    return r.fulfill(json({ kind: "order", id: "o1", name: "AK-7QX4KD", party: "A•••", when: now, total_kobo: 1_330_000, status: "paid", lines: [] }));
  });
  // Taskiem answers what was written: the words come back as the person's.
  await page.route("**/v1/taskiem-chat", (r) =>
    r.request().method() === "POST"
      ? r.fulfill(json({ messages: [{ id: "m1", channel: "web", inbound: true, kind: "text", text: (r.request().postDataJSON() as { text: string }).text, created_at: now }] }))
      : r.fulfill(json({ messages: [] })),
  );
  await page.route("**/v1/chat-alerts", (r) => r.fulfill(json({ alerts: [] })));
  await page.route(/\/v1\/chat-briefing(\?.*)?$/, (r) => r.fulfill(json({ ready: false, day: "", key: "", put_off: false, part: "morning", now: "07:00" })));
  return sent;
}

for (const width of [390, 360]) {
  test(`invoices and money in the chat tools, at ${width} px`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 844 });
    await owner(page);
    const sent = await answer(page);
    await page.goto("/chat?with=taskiem");
    const pane = page.getByTestId("taskiem-pane");
    await pane.getByRole("button", { name: "Open tools" }).click();
    const panel = page.getByRole("region", { name: "Tools" });
    await expect(panel.getByRole("button", { name: /^Invoices/ })).toContainText("2 owed");
    await expect(panel.getByRole("button", { name: /^Invoices/ })).toContainText("1 overdue");
    await panel.getByRole("button", { name: /^Invoices/ }).click();

    // The list, searched and by chip.
    const sheet = page.getByTestId("tool-sheet");
    await expect(sheet.getByRole("heading", { name: /Invoices/ })).toBeVisible();
    await expect(sheet.locator(".tk-sheet-list")).toContainText("INV-0001");
    await sheet.getByRole("button", { name: /^Overdue/ }).click();
    await expect(sheet.locator(".tk-sheet-list")).not.toContainText("INV-0001");
    await expect(sheet.locator(".tk-sheet-list")).toContainText("INV-0002");
    await sheet.getByRole("button", { name: "All" }).click();
    await sheet.getByRole("searchbox").fill("catering");
    await expect(sheet.locator(".tk-sheet-list")).toContainText("INV-0001");
    await expect(sheet.locator(".tk-sheet-list")).not.toContainText("INV-0002");
    await axeBoth(page, "the invoices tool");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

    // One invoice: a payment recorded, in the money log and counted to it.
    await sheet.getByRole("button", { name: /INV-0001/ }).click();
    await expect(sheet.getByTestId("invoice-hero")).toContainText("₦45,000");
    await sheet.getByRole("button", { name: "Record a payment" }).click();
    await sheet.getByLabel("What they paid (₦)").fill("20000");
    await axeBoth(page, "recording a payment");
    await sheet.getByRole("button", { name: /^Record ₦20,000/ }).click();
    await expect(page.locator(".tk-saved")).toBeVisible();
    const entry = sent.find((x) => x.path === "/v1/money/entries");
    expect(entry?.body).toEqual({ kind: "cash", direction: "in", amount_kobo: 2_000_000, description: "Payment for INV-0001", channel: "cash" });
    expect(entry?.key).toBeTruthy();
    expect(sent.find((x) => x.path === "/v1/money/entries/e9/match")?.body).toEqual({ invoice: "INV-0001" });

    // The money log: cash in matched to an owed invoice.
    await pane.getByRole("button", { name: "Open tools" }).click();
    await panel.getByRole("button", { name: /^Money/ }).click();
    await expect(sheet.locator(".tk-sheet-list")).toContainText("Cash from Bisi");
    await expect(sheet.locator(".tk-sheet-list")).toContainText("Counted");
    await sheet.getByRole("button", { name: /^Out/ }).click();
    await expect(sheet.locator(".tk-sheet-list")).toContainText("Diesel");
    await expect(sheet.locator(".tk-sheet-list")).not.toContainText("Cash from Bisi");
    await sheet.getByRole("button", { name: "All" }).click();
    await sheet.getByRole("button", { name: /Cash from Bisi/ }).click();
    await expect(sheet.getByTestId("money-hero")).toContainText("+₦25,000");
    await sheet.getByRole("button", { name: "Match to an invoice" }).click();
    await sheet.getByLabel("The invoice it pays").selectOption("INV-0002");
    await axeBoth(page, "matching money");
    await sheet.getByRole("button", { name: "Match it" }).click();
    await expect(page.locator(".tk-saved")).toBeVisible();
    expect(sent.find((x) => x.path === "/v1/money/entries/e1/match")?.body).toEqual({ invoice: "INV-0002" });
  });

  test(`"/" tags an invoice, an order or a customer, at ${width} px`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width, height: 844 });
    await owner(page);
    await answer(page);
    await page.goto("/chat?with=taskiem");
    const pane = page.getByTestId("taskiem-pane");
    const box = pane.locator("#taskiem-text");
    await box.click();
    await box.pressSequentially("Did they pay /invoice cat");
    const picker = page.getByTestId("mention-list");
    // Nothing matches "cat" by number: no picker.
    await expect(picker).toHaveCount(0);
    await box.press("Backspace");
    await box.press("Backspace");
    await box.press("Backspace");
    await box.pressSequentially("INV-000");
    await expect(picker.getByRole("button", { name: /INV-0001/ })).toBeVisible();
    // Each row says who, when, how much and where it stands, and what it is.
    await expect(picker.getByRole("button", { name: /INV-0002/ })).toContainText("Tola's Kitchen · 3 Nov");
    await expect(picker.getByRole("button", { name: /INV-0002/ })).toContainText("overdue");
    await expect(picker.getByRole("button", { name: /INV-0002/ })).toContainText("Invoice");
    // The tools panel is not opened by it.
    await expect(page.getByRole("region", { name: "Tools" })).toHaveCount(0);
    await axeBoth(page, "the / picker");
    await picker.getByRole("button", { name: /INV-0002/ }).click();
    await expect(box).toHaveValue("Did they pay #INV-0002 ");
    await expect(box).toBeFocused();
    await box.pressSequentially("and /order/ak");
    await picker.getByRole("button", { name: /AK-7QX4KD/ }).click();
    await expect(box).toHaveValue("Did they pay #INV-0002 and #AK-7QX4KD ");
    await expect(box).toBeFocused();
    await box.pressSequentially("for /customer bisi");
    await picker.getByRole("button", { name: /Bisi Okafor/ }).click();
    await expect(box).toHaveValue("Did they pay #INV-0002 and #AK-7QX4KD for #Bisi Okafor ");
    // Sent: each thing named is a card, read now.
    await box.press("Enter");
    const cards = pane.getByTestId("ref-card");
    await expect(cards).toHaveCount(3);
    const inv = pane.getByRole("article", { name: "Invoice INV-0002" });
    await expect(inv).toContainText("Tola's Kitchen");
    await expect(inv).toContainText("₦9,000");
    await expect(inv).toContainText("Overdue");
    await expect(inv).toContainText("10 × Chairs");
    await expect(inv.getByRole("button", { name: /^Send a reminder/ })).toBeVisible();
    const bisi = pane.getByRole("article", { name: "Customer Bisi Okafor" });
    await expect(bisi).toContainText("•••0555");
    await expect(bisi).toContainText("₦4,500");
    await expect(bisi).toContainText("AK-7QX4KD");
    await expect(bisi.getByRole("button", { name: /^Message them/ })).toBeVisible();
    await axeBoth(page, "the cards");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    if (width === 390) await page.screenshot({ path: "test-results/ref-cards-phone.png", fullPage: false });
  });

  test(`"/bill" and "/order" in the Taskiem chat, at ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await owner(page);
    await answer(page);
    await page.goto("/chat?with=taskiem");
    const box = page.getByTestId("taskiem-pane").locator("#taskiem-text");
    await box.click();
    await box.pressSequentially("/bill ");
    const picker = page.getByTestId("mention-list");
    await expect(picker.getByRole("button")).toHaveCount(1);
    await expect(picker.getByRole("button", { name: /INV-0420/ })).toContainText("Dangote Stores");
    await box.fill("");
    await box.pressSequentially("/invoice ");
    // "/invoice" is invoices and bills.
    await expect(picker.getByRole("button")).toHaveCount(3);
  });
}
