import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Pay a bill by transfer, in the chat (the payments flow, part 1;
// docs/spec/chat-payments-flow.md). The e2e server has no payments
// provider that gives one-time account numbers, so the bill's number is
// stood in for (page.route); the pages, the sheet, the card and its
// states are the app's own. At 390 and 360 px, axe in both themes.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@transfer.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function axeBoth(page: Page, where: string) {
  const width = page.viewportSize()?.width ?? 390;
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(300);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme}, ${width} px)`, vs)).toEqual([]);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
}

const ID = "0192e2e0-0000-7000-8000-0000000b1110";
const NEXT = "0192e2e0-0000-7000-8000-0000000b1111";
const SUPPLIER = "0192e2e0-0000-7000-8000-0000000b5001";
const at = (ms: number) => new Date(Date.now() + ms).toISOString();
const OPEN = { bank_name: "Wema Bank", account_number: "0123456789", account_name: "Dangote Depot", amount_kobo: 8_300_000 };

function bill(over: Record<string, unknown> = {}) {
  const now = new Date().toISOString();
  return {
    id: ID,
    link: "0192e2e0-0000-7000-8000-0000000b9001",
    supplier: SUPPLIER,
    supplier_name: "Dangote Depot",
    reference: "INV-0042",
    lines: [{ description: "Rice, 50 kg", quantity: 1, unit_kobo: 8_300_000 }],
    amount_kobo: 8_300_000,
    paid_kobo: 0,
    due_kobo: 8_300_000,
    status: "open",
    allow_part_payments: true,
    disputed: false,
    declared_paid_kobo: 0,
    paying_kobo: 0,
    sent_kobo: 0,
    uncounted_kobo: 0,
    pay_link: "https://pay.example.test/INV-0042",
    ways_to_pay: ["transfer", "checkout"],
    transfer: null,
    mirrored_at: now,
    created_at: now,
    ...over,
  };
}

/** The bill's server, stood in for: `state.bill` is what it answers; a
 * POST to its transfer runs `state.ask`. */
async function standIn(page: Page, state: { bill: ReturnType<typeof bill>; ask?: (body: Record<string, unknown>) => { status: number; body: unknown }; asked: Record<string, unknown>[] }) {
  await page.route(/\/v1\/link-chats\/thread\?/, (r) => r.fulfill(json({ messages: [] })));
  await page.route(new RegExp(`/v1/bills/${ID}(/transfer)?$`), async (r: Route) => {
    if (r.request().method() === "POST") {
      const body = (r.request().postDataJSON() ?? {}) as Record<string, unknown>;
      state.asked.push(body);
      const out = state.ask?.(body) ?? { status: 200, body: state.bill };
      return r.fulfill(json(out.body, out.status));
    }
    return r.fulfill(json(state.bill));
  });
}

test("Pay a bill by transfer: the ways, the number, and each state", async ({ page, context }) => {
  test.setTimeout(120_000);
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await signUp(page, `Ada Kitchen ${letters()}`);
  const state: Parameters<typeof standIn>[1] = { bill: bill(), asked: [] };
  await standIn(page, state);
  await page.goto(`/bills/${ID}`);
  await expect(page.getByTestId("bill-hero")).toContainText("₦83,000");

  // Pay opens the ways: by transfer first.
  await page.getByRole("button", { name: "Pay ₦83,000" }).click();
  const sheet = page.getByRole("dialog");
  const ways = sheet.getByTestId("pay-ways").getByRole("listitem");
  await expect(ways).toHaveCount(2);
  await expect(ways.first()).toContainText("Pay by transfer");
  await expect(ways.nth(1)).toContainText("Pay on their checkout");
  await axeBoth(page, "the ways to pay");

  // Pay by transfer: asked (202), then the number arrives within seconds.
  state.ask = () => {
    state.bill = bill({ transfer: { state: "asking" } });
    // The seller's provider gives longer: I need longer is offered.
    setTimeout(() => (state.bill = bill({ transfer: { state: "open", ...OPEN, longer: true, expires_at: at(29 * 60_000 + 30_000) } })), 1500);
    return { status: 202, body: state.bill };
  };
  await sheet.getByRole("button", { name: /Pay by transfer/ }).click();
  await expect(sheet.getByTestId("transfer-head")).toHaveText("Getting an account number…");
  await axeBoth(page, "getting an account number");
  await expect(sheet.getByTestId("transfer-head")).toHaveText("Send exactly ₦83,000", { timeout: 10_000 });
  const card = sheet.getByTestId("transfer-card");
  await expect(card).toContainText("Wema Bank");
  await expect(card).toContainText("0123 456 789");
  await expect(card).toContainText("Dangote Depot");
  await expect(card.getByTestId("transfer-time")).toHaveText(/^Good for 29:\d\d$/);
  await expect(card).toContainText("We'll mark it paid here as soon as it lands.");
  await expect(card).toContainText("Waiting for your transfer");
  await expect(card).not.toContainText(/virtual account|dynamic|rail|reference/i);
  // The number copies without spaces.
  await card.getByRole("button", { name: "Copy the account number" }).click();
  await expect(card.getByRole("status").filter({ hasText: "Copied" })).toHaveText("Copied the account number");
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("0123456789");
  await axeBoth(page, "the account number");
  await page.setViewportSize({ width: 360, height: 800 });
  await axeBoth(page, "the account number");
  await page.setViewportSize({ width: 390, height: 844 });
  // The choice is remembered for this seller.
  expect(await page.evaluate((s) => localStorage.getItem(`taskiem.pay-way.${s}`), SUPPLIER)).toBe("transfer");

  // I need longer asks again, for longer.
  state.ask = () => ({ status: 200, body: (state.bill = bill({ transfer: { state: "open", ...OPEN, expires_at: at(3 * 3600_000) } })) });
  await card.getByRole("button", { name: "I need longer" }).click();
  await expect(card.getByTestId("transfer-time")).toHaveText(/^Good for 2:59:\d\d$/);
  expect(state.asked.at(-1)).toEqual({ longer: true });
  await sheet.getByRole("button", { name: "Done" }).click();
  // On the bill's page, the number stays; Pay waits while it is open.
  await expect(page.getByTestId("transfer-card")).toContainText("Send exactly ₦83,000");
  await expect(page.getByTestId("bill-pay-button")).toHaveCount(0);

  // The last five minutes: the warning colour.
  state.bill = bill({ transfer: { state: "open", ...OPEN, expires_at: at(4 * 60_000 + 5_500) } });
  await page.reload();
  const time = page.getByTestId("transfer-time");
  await expect(time).toHaveText(/^Expires in 4:0\d$/);
  await expect(time).toHaveClass(/warn/);
  await axeBoth(page, "the last five minutes");
  await page.setViewportSize({ width: 360, height: 800 });
  await axeBoth(page, "the last five minutes");
  await page.setViewportSize({ width: 390, height: 844 });

  // Expired: don't send to it; Get a new number.
  state.bill = bill({ transfer: { state: "open", ...OPEN, expires_at: at(-1000) } });
  await page.reload();
  await expect(page.getByTestId("transfer-head")).toHaveText("This number has expired. Don't send to it");
  await axeBoth(page, "an expired number");
  state.ask = () => ({ status: 200, body: (state.bill = bill({ transfer: { state: "open", ...OPEN, expires_at: at(60 * 60_000) } })) });
  await page.getByRole("button", { name: "Get a new number" }).click();
  await expect(page.getByTestId("transfer-head")).toHaveText("Send exactly ₦83,000");
  expect(state.asked.at(-1)).toEqual({});

  // Part paid: Pay the rest asks for what is left.
  state.bill = bill({ status: "part_paid", paid_kobo: 5_000_000, due_kobo: 3_300_000, transfer: { state: "part", ...OPEN, received_kobo: 5_000_000 } });
  await page.reload();
  await expect(page.getByTestId("transfer-head")).toHaveText("Received ₦50,000 of ₦83,000. ₦33,000 left");
  await axeBoth(page, "part paid");
  state.ask = () => ({ status: 200, body: (state.bill = bill({ status: "part_paid", paid_kobo: 5_000_000, due_kobo: 3_300_000, transfer: { state: "open", ...OPEN, amount_kobo: 3_300_000, expires_at: at(60 * 60_000) } })) });
  await page.getByRole("button", { name: "Pay the rest" }).click();
  await expect(page.getByTestId("transfer-head")).toHaveText("Send exactly ₦33,000");

  // Paid: the tick, and the same seller's next bill.
  state.bill = bill({ status: "paid", paid_kobo: 8_300_000, due_kobo: 0, ways_to_pay: [], transfer: { state: "paid", ...OPEN, next: NEXT } });
  await page.reload();
  await expect(page.getByTestId("transfer-head")).toHaveText(/^Paid ₦83,000 · Today, \d\d:\d\d$/);
  await expect(page.getByRole("link", { name: "Pay the next one" })).toHaveAttribute("href", `/bills/${NEXT}?pay=1`);
  await axeBoth(page, "paid");

  // Couldn't get one: why, and the other ways.
  state.bill = bill({ transfer: { state: "failed", problem: "Their bank did not give a number just now. Try again, or pay another way." } });
  await page.reload();
  await expect(page.getByTestId("transfer-head")).toHaveText("Their bank did not give a number just now. Try again, or pay another way.");
  await expect(page.getByTestId("transfer-card").getByRole("link", { name: /Pay on their checkout/ })).toHaveAttribute("href", "https://pay.example.test/INV-0042");
  await axeBoth(page, "no number");

  // Too many asked: the refusal in words.
  state.ask = () => ({ status: 429, body: { error: "Too many numbers asked for this bill. Try again in a while, or pay on their checkout.", code: "too_many" } });
  await page.getByTestId("transfer-card").getByRole("button", { name: "Try again" }).click();
  await expect(page.getByTestId("transfer-head")).toHaveText(/^Too many numbers asked for this bill/);
});

test("with one way to pay, Pay goes straight to it; the last way chosen comes first", async ({ page }) => {
  await signUp(page, `Ada Kitchen ${letters()}`);
  const state: Parameters<typeof standIn>[1] = { bill: bill({ ways_to_pay: ["transfer"] }), asked: [] };
  state.ask = () => ({ status: 200, body: (state.bill = bill({ ways_to_pay: ["transfer"], transfer: { state: "open", ...OPEN, expires_at: at(60 * 60_000) } })) });
  await standIn(page, state);
  await page.goto(`/bills/${ID}`);
  await page.getByRole("button", { name: "Pay ₦83,000" }).click();
  const sheet = page.getByRole("dialog");
  await expect(sheet.getByTestId("pay-ways")).toHaveCount(0);
  await expect(sheet.getByTestId("transfer-head")).toHaveText("Send exactly ₦83,000");
  // Their provider gives no longer than the default: no I need longer.
  await expect(sheet.getByRole("button", { name: "I need longer" })).toHaveCount(0);

  // The checkout chosen last time for this seller leads next time.
  state.bill = bill();
  await page.evaluate((s) => localStorage.setItem(`taskiem.pay-way.${s}`, "checkout"), SUPPLIER);
  await page.reload();
  await page.getByRole("button", { name: "Pay ₦83,000" }).click();
  await expect(page.getByRole("dialog").getByTestId("pay-ways").getByRole("listitem").first()).toContainText("Pay on their checkout");
});

test("a customer pays an invoice by transfer from their chat with a business", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  const now = new Date().toISOString();
  const PERSON = {
    tenant_id: null,
    personal: true,
    roles: [],
    permissions: [],
    auth_method: "phone",
    user: { id: "0192e2e0-0000-7000-8000-00000000a001", email: "person+xfer@phone.taskiem.invalid", email_placeholder: true, name: "Ada Obi", phone: "+2348031234567" },
    businesses: [],
  };
  const chat = { id: "c1", business: "b1", name: "Bola Stores", last: "Invoice", last_mine: false, last_at: now, unread: 0, muted: false, archived: false, blocked: false, blocked_you: false, offers: "no", ask_offers: false, hours: { open: true, line: "Open now" }, lines: ["app"] };
  await page.route("**/v1/me", (r) => r.fulfill(json(PERSON)));
  await page.route("**/v1/me/invitations", (r) => r.fulfill(json({ invitations: [] })));
  await page.route(/\/v1\/me\/chats(\?|$)/, (r) => r.fulfill(json({ chats: [chat], unread: 0 })));
  await page.route(/\/v1\/me\/conversations(\?|$)/, (r) => r.fulfill(json({ chats: [], unread: 0, requests: 0 })));
  await page.route("**/v1/me/chats/c1", (r) => r.fulfill(json(chat)));
  await page.route(/\/v1\/me\/chats\/c1\/messages/, (r) =>
    r.fulfill(
      json({
        messages: [
          { id: "m1", mine: false, kind: "card", body: "Invoice", card: { type: "invoice", reference: "INV-0007", invoice_id: "f1", title: "Invoice INV-0007", status: "sent", total_kobo: 4_500_000, url: "https://pay.example.test/i" }, read_at: null, created_at: now },
        ],
        more: false,
      }),
    ),
  );
  await page.route("**/v1/me/chats/c1/read", (r) => r.fulfill({ status: 204 }));
  await page.route("**/v1/me/chats/c1/orders", (r) => r.fulfill(json({ orders: [] })));
  await page.route("**/v1/me/push/key", (r) => r.fulfill(json({ error: "not found" }, 404)));
  let transfer: Record<string, unknown> = { invoice: "f1", reference: "INV-0007", due_kobo: 4_500_000, state: "none" };
  const asked: unknown[] = [];
  await page.route("**/v1/me/chats/c1/invoices/f1/transfer", (r) => {
    if (r.request().method() === "POST") {
      asked.push(r.request().postDataJSON());
      transfer = { ...transfer, state: "open", ...OPEN, account_name: "Bola Stores", amount_kobo: 4_500_000, expires_at: at(60 * 60_000) };
    }
    return r.fulfill(json(transfer));
  });
  await page.goto("/you/chats/c1");
  const pay = page.getByRole("button", { name: /^Pay ₦45,000/ });
  await expect(pay).toBeVisible();
  await pay.click();
  const sheet = page.getByTestId("pay-sheet");
  await expect(sheet.getByTestId("pay-ways").getByRole("listitem").first()).toContainText("Pay by transfer");
  await axeBoth(page, "a customer's ways to pay");
  await sheet.getByRole("button", { name: /Pay by transfer/ }).click();
  const card = page.getByTestId("customer-transfer-card");
  await expect(card.getByTestId("transfer-head")).toHaveText("Send exactly ₦45,000");
  await expect(card).toContainText("0123 456 789");
  await expect(card.getByTestId("transfer-time")).toHaveText(/^Good for (59:\d\d|1:00:00)$/);
  await card.getByRole("button", { name: "Copy the account number" }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("0123456789");
  expect(asked).toEqual([{}]);
  await expect(card.getByRole("button", { name: "I need longer" })).toHaveCount(0);
  await axeBoth(page, "a customer's account number");
  await page.setViewportSize({ width: 360, height: 800 });
  await axeBoth(page, "a customer's account number");

  // Expired, then a new number.
  transfer = { ...transfer, expires_at: at(-1000), state: "expired" };
  await page.reload();
  await expect(page.getByTestId("customer-transfer-card").getByTestId("transfer-head")).toHaveText("This number has expired. Don't send to it");
  // This time the business's provider gives longer: I need longer is offered.
  transfer = { ...transfer, longer: true };
  await page.getByRole("button", { name: "Get a new number" }).click();
  await expect(page.getByTestId("customer-transfer-card").getByTestId("transfer-head")).toHaveText("Send exactly ₦45,000");
  await page.getByTestId("customer-transfer-card").getByRole("button", { name: "I need longer" }).click();
  await expect.poll(() => asked.at(-1)).toEqual({ longer: true });
});

test("the chat's cards: the buyer's number, the seller's 'is paying by transfer', expired, and paid", async ({ browser }) => {
  test.setTimeout(120_000);
  const stamp = letters();
  const sellerName = `Dangote Depot ${stamp}`;
  const buyerName = `Ada Kitchen ${stamp}`;
  const seller = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await signUp(seller, sellerName);
  const buyer = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await signUp(buyer, buyerName);
  expect((await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } })).status()).toBe(200);
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`, { headers: H })).json();
  expect((await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } })).status()).toBe(201);
  const pending = await (await seller.request.get("/v1/links", { headers: H })).json();
  const link = pending.links[0].id as string;
  expect((await seller.request.post(`/v1/links/${link}/accept`, { headers: H })).status()).toBe(200);
  expect((await seller.request.post(`/v1/link-chats/${link}/messages`, { headers: H, data: { text: "Your invoice is in" } })).status()).toBe(201);

  // The cards the server posts when a number is given, added to what each
  // side's conversation answers.
  // Each card is [card, mine], oldest first; the list can change between
  // loads. A transfer card's note is when its number expires.
  type Cards = [Record<string, unknown>, boolean][];
  const withCards = async (page: Page, cards: { now: Cards }) =>
    page.route(new RegExp(`/v1/link-chats/${link}/messages(\\?|$)`), async (r) => {
      if (r.request().method() !== "GET") return r.continue();
      const res = await r.fetch();
      const body = (await res.json()) as { messages: unknown[] };
      const t0 = Date.now() - 60_000;
      body.messages = [
        ...(body.messages ?? []),
        ...cards.now.map(([card, mine], i) => ({ id: `0192e2e0-0000-7000-8000-00000000c0d${i}`, link, mine, kind: "card", card, created_at: new Date(t0 + i * 1000).toISOString() })),
      ];
      return r.fulfill({ response: res, json: body });
    });
  const given = { reference: "INV-0042", id: "INV-0042", amount_kobo: 8_300_000, event: "transfer" };
  const buyerCards = { now: [[{ ...given, doc: "bill", note: at(60 * 60_000) }, true]] as Cards };
  const sellerCards = { now: [[{ ...given, doc: "invoice", note: at(60 * 60_000) }, false]] as Cards };
  await withCards(buyer, buyerCards);
  await withCards(seller, sellerCards);
  let asks = 0;
  await buyer.route(/\/v1\/bills\/[^/]+\/transfer$/, (r) => {
    // Read by the seller's name and the number, then by the bill's id.
    expect([`/v1/bills/${sellerName} INV-0042/transfer`, `/v1/bills/${ID}/transfer`]).toContain(decodeURIComponent(new URL(r.request().url()).pathname));
    if (r.request().method() === "POST") asks++;
    return r.fulfill(json(bill({ transfer: asks ? { state: "open", ...OPEN, expires_at: at(60 * 60_000) } : { state: "open", ...OPEN, expires_at: at(-1000) } })));
  });

  await buyer.goto(`/chat?with=${link}`);
  const card = buyer.getByTestId("chat-transfer-card");
  await expect(buyer.getByText("You are paying by transfer", { exact: true })).toBeVisible();
  await expect(card.getByTestId("transfer-head")).toHaveText("This number has expired. Don't send to it");
  await card.getByRole("button", { name: "Get a new number" }).click();
  await expect(card.getByTestId("transfer-head")).toHaveText("Send exactly ₦83,000");
  await expect(card).toContainText("0123 456 789");
  await axeBoth(buyer, "the buyer's transfer card in the chat");
  await buyer.setViewportSize({ width: 360, height: 800 });
  await axeBoth(buyer, "the buyer's transfer card in the chat");

  await seller.goto(`/chat?with=${link}`);
  await expect(seller.getByText(`${buyerName} is paying by transfer`, { exact: true })).toBeVisible();
  await expect(seller.getByTestId("chat-transfer-card")).toHaveCount(0);
  await axeBoth(seller, "the seller's card in the chat");

  const sellerCard = seller.getByRole("article", { name: "Invoice · INV-0042" });
  // The number's time passed with nothing in: back to To pay.
  sellerCards.now = [[{ ...given, doc: "invoice", note: at(-1000) }, false]];
  await seller.reload();
  await expect(seller.getByText(`${buyerName}'s account number expired`, { exact: true })).toBeVisible();
  await expect(sellerCard.locator(".tk-pill")).toHaveText("To pay");
  await expect(sellerCard).toContainText("Number expired");

  // Paid in full: the earlier card stops saying paying; the paid card says how much.
  sellerCards.now = [
    [{ ...given, doc: "invoice", note: at(60 * 60_000) }, false],
    [{ ...given, doc: "invoice", event: "transfer_paid" }, false],
  ];
  await seller.reload();
  await expect(seller.getByText(`${buyerName} paid ₦83,000 by transfer`, { exact: true })).toBeVisible();
  await expect(seller.getByText(`${buyerName} paid by transfer`, { exact: true })).toBeVisible();
  await expect(seller.getByText(`${buyerName} is paying by transfer`, { exact: true })).toHaveCount(0);
  await expect(sellerCard.locator(".tk-pill")).toHaveText(["Paid", "Paid"]);
  await axeBoth(seller, "the seller's paid card in the chat");

  // The buyer, part paid: a card, not a diary line, and no live number under the old card.
  buyerCards.now = [
    [{ ...given, doc: "bill", note: at(60 * 60_000) }, true],
    [{ ...given, doc: "bill", event: "transfer_part", amount_kobo: 5_000_000 }, true],
  ];
  await buyer.reload();
  await expect(buyer.getByText("Part paid by transfer: ₦50,000 so far", { exact: true })).toBeVisible();
  await expect(buyer.getByText("You paid part by transfer", { exact: true })).toBeVisible();
  await expect(buyer.getByText("You are paying by transfer", { exact: true })).toHaveCount(0);
  await expect(buyer.getByTestId("chat-transfer-card")).toHaveCount(0);
  await axeBoth(buyer, "the buyer's part-paid card in the chat");

  // The buyer's number ran out: its card says so.
  buyerCards.now = [[{ ...given, doc: "bill", note: at(-1000) }, true]];
  await buyer.reload();
  await expect(buyer.getByText("Your account number expired", { exact: true })).toBeVisible();
  await expect(buyer.getByRole("article", { name: "Invoice · INV-0042" }).locator(".tk-pill")).toHaveText("Expired");
});
