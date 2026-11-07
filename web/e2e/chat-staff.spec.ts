import { execFileSync } from "node:child_process";
import { expect, test, type Browser, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Chat tools, phase 5 (docs/spec/chat-tools.md §7): staff use chat as
// their app, and the owner gets the 8am card. A cashier's tools are Sell,
// Stock and Orders, and Sell records a sale; a shift manager hands the shift
// over; the owner's briefing leads the Taskiem conversation from 8am, and
// "how did we do?" is answered with a chart card. The numbers are answered
// here (api/chat_staff_test.go reads the real ones).
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const PASSWORD = "correct horse battery";
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });

/** An owner, with chat tools on; and, when a role is named, a member of
 * theirs signed in on a page of their own. */
async function people(browser: Browser, role?: string, viewport = { width: 390, height: 844 }) {
  const owner = await (await browser.newContext({ viewport })).newPage();
  const ownerEmail = `${letters()}-${Math.random().toString(36).slice(2, 8)}@staff.test`;
  const r = await owner.request.post("/v1/signup", {
    headers: H,
    data: { tenant: `Ada Kitchen ${letters()}`, email: ownerEmail, name: "Ada Obi", password: PASSWORD },
  });
  expect(r.status(), await r.text()).toBe(201);
  // The e2e server sends no email: the owner's address is marked confirmed
  // (as cash.spec.ts), so they may add their staff.
  sql(`UPDATE users SET email_verified_at = now() WHERE lower(email) = lower('${ownerEmail}')`);
  await owner.request.put("/v1/settings/chat-tools", { headers: H, data: { chat_tools: true } });
  if (!role) return { owner, member: owner };
  const email = `${role}-${letters()}-${Math.random().toString(36).slice(2, 6)}@staff.test`;
  const added = await owner.request.post("/v1/members", { headers: H, data: { email, password: PASSWORD, roles: [role] } });
  expect(added.status(), await added.text()).toBe(201);
  const member = await (await browser.newContext({ viewport })).newPage();
  const login = await member.request.post("/v1/auth/login", { headers: H, data: { email, password: PASSWORD } });
  expect(login.status(), await login.text()).toBe(200);
  return { owner, member };
}

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
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

const day = (back: number) => new Date(Date.now() - back * 86_400_000).toISOString().slice(0, 10);
const WEEK = [6, 5, 4, 3, 2, 1, 0].map((n, i) => ({ day: day(n + 1), label: ["Fri", "Sat", "Sun", "Mon", "Tue", "Wed", "Thu"][i], kobo: ([30, 42, 38, 51, 47, 40, 48.65][i] ?? 0) * 1_000_000 }));

async function answer(page: Page) {
  const sent: { path: string; body: unknown; key?: string }[] = [];
  const remember = async (r: Route, reply: unknown, status = 200) => {
    const q = r.request();
    sent.push({ path: new URL(q.url()).pathname, body: q.postData() ? q.postDataJSON() : undefined, key: q.headers()["idempotency-key"] });
    await r.fulfill(json(reply, status));
  };
  await page.route("**/v1/stock", (r) =>
    r.fulfill(json({ keeps_stock: true, catalogue: "products", items: [
      { id: "s1", name: "Rice, 50kg bag", available: true, stock: 12, unit: "bag", price_kobo: 7_800_000 },
      { id: "s2", name: "Garri, Ijebu", available: true, stock: 0, unit: "bag", price_kobo: 3_200_000 },
      { id: "s3", name: "Pepper", available: true, stock: 5, unit: "basket" },
    ] })),
  );
  await page.route("**/v1/stock/sales", (r) => remember(r, { sale: { id: "x", reference: "S-1" } }, 201));
  await page.route("**/v1/chat-handover", (r) =>
    r.request().method() === "GET"
      ? r.fulfill(json({ day: day(0), sales: { total_kobo: 23_400_000, count: 3, by_how: { cash: 15_600_000, pos: 7_800_000 }, waiting: 0 }, orders_open: 2, deliveries: { out: 1, done: 3 }, low: ["Garri, Ijebu"] }))
      : remember(r, { handover: {}, words: "" }),
  );
  await page.route("**/v1/chat-briefing", (r) =>
    r.fulfill(json({ ready: true, day: day(0), key: `briefing:${day(0)}`, put_off: false,
      sales: { yesterday_kobo: 48_650_000, change_pct: 12, weekday: "Wednesday", week: WEEK }, owed_kobo: 21_200_000, low: 3, deliveries: 4, bookings: 0 })),
  );
  await page.route("**/v1/chat-alerts/later", (r) => remember(r, { key: "x", until: day(-1) }));
  await page.route("**/v1/chat-alerts", (r) => r.fulfill(json({ alerts: [] })));
  return sent;
}

test("a cashier's tools are Sell, Stock and Orders, and Sell records a sale", async ({ browser }) => {
  test.setTimeout(120_000);
  const { member } = await people(browser, "cashier");
  const sent = await answer(member);
  await member.goto("/chat?with=taskiem");
  const pane = member.getByTestId("taskiem-pane");
  await pane.getByRole("button", { name: "Open tools" }).click();
  const panel = member.getByRole("region", { name: "Tools" });
  await expect(panel.locator(".tk-tile .tk-tile-name")).toHaveText(["Sell", "Stock", "Orders"]);
  await panel.getByRole("button", { name: /^Sell/ }).click();
  const sheet = member.getByTestId("sell-sheet");
  // Only what has a price.
  await expect(sheet.getByTestId("sell-row")).toHaveCount(2);
  await sheet.getByRole("button", { name: "One more Rice, 50kg bag" }).click();
  await sheet.getByRole("button", { name: "One more Rice, 50kg bag" }).click();
  await sheet.getByRole("button", { name: "One more Garri, Ijebu" }).click();
  // More than the stock holds is said, not refused.
  await expect(sheet.getByTestId("sell-row").filter({ hasText: "Garri" })).toContainText("None left in your stock");
  await expect(sheet.getByTestId("sell-bag")).toContainText("₦188,000");
  await expect(sheet.getByTestId("sell-bag")).toContainText("3 things");
  await axeBoth(member, "selling");
  await sheet.getByRole("button", { name: /^Next/ }).click();
  await expect(sheet.getByRole("heading", { name: "How was it paid?" })).toBeVisible();
  // The ways to pay move with the arrow keys, as radios do.
  await sheet.getByRole("radio", { name: "Cash" }).focus();
  await member.keyboard.press("ArrowRight");
  await expect(sheet.getByRole("radio", { name: "Transfer" })).toBeFocused();
  await member.keyboard.press("ArrowRight");
  await expect(sheet.getByRole("radio", { name: "POS" })).toHaveAttribute("aria-checked", "true");
  await axeBoth(member, "how a sale was paid");
  await sheet.getByRole("button", { name: "Record the sale" }).click();
  await expect(sheet).toHaveCount(0, { timeout: 5000 });
  const sale = sent.find((x) => x.path === "/v1/stock/sales");
  expect(sale?.body).toEqual({ lines: [{ item_id: "s1", quantity: 2 }, { item_id: "s2", quantity: 1 }], paid_how: "pos" });
  expect(sale?.key).toBeTruthy();
});

test("a shift manager hands the shift over, the cash counted against the cash sold", async ({ browser }) => {
  test.setTimeout(120_000);
  const { member } = await people(browser, "shift_manager");
  const sent = await answer(member);
  await member.goto("/chat?with=taskiem");
  await member.getByTestId("taskiem-pane").getByRole("button", { name: "Open tools" }).click();
  await member.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Handover/ }).click();
  const sheet = member.getByTestId("handover-sheet");
  await expect(sheet.getByTestId("handover-hero")).toContainText("₦234,000");
  await expect(sheet).toContainText("Orders still open");
  await expect(sheet).toContainText("Garri, Ijebu");
  await sheet.getByLabel("Cash counted (₦)").fill("150,000");
  await expect(sheet).toContainText("₦6,000 short");
  await sheet.getByLabel("A note for the next shift (optional)").fill("Till drawer sticks");
  await axeBoth(member, "handing over");
  await sheet.getByRole("button", { name: "Hand over" }).click();
  await expect(sheet).toHaveCount(0, { timeout: 5000 });
  expect(sent.find((x) => x.path === "/v1/chat-handover")?.body).toEqual({ cash_counted_kobo: 15_000_000, note: "Till drawer sticks" });
});

for (const [size, viewport] of [
  ["a phone", { width: 390, height: 844 }],
  ["a computer", { width: 1440, height: 900 }],
] as const) {
  test(`the owner's 8am card on ${size}, and how did we do as a chart`, async ({ browser }) => {
    test.setTimeout(120_000);
    const { owner } = await people(browser, undefined, viewport);
    const sent = await answer(owner);
    const at = new Date().toISOString();
    await owner.route("**/v1/taskiem-chat", (r) =>
      r.request().method() === "GET"
        ? r.fulfill(json({ messages: [
            { id: "m1", channel: "web", inbound: true, kind: "text", text: "how did we do this week", created_at: at },
            { id: "m2", channel: "web", inbound: false, kind: "text", text: "Sales, the last 7 days: ₦2,966,500, up 8% on the week before", created_at: at },
            { id: "m3", channel: "web", inbound: false, kind: "chart", created_at: at,
              text: JSON.stringify({ title: "Sales, the last 7 days", total_kobo: 296_650_000, change_pct: 8, than: "the week before", bars: WEEK, highlight: -1 }) },
          ] }))
        : r.fallback(),
    );
    await owner.goto("/chat?with=taskiem");
    const card = owner.getByTestId("briefing-card");
    await expect(card).toContainText("Good morning, Ada");
    await expect(card).toContainText("₦486,500");
    await expect(card).toContainText("Up 12% on last Wednesday");
    await expect(card).toContainText("₦212,000");
    await expect(card).toContainText("3 items");
    await expect(card).toContainText("4 today");
    const chart = owner.getByTestId("chart-card");
    await expect(chart).toContainText("₦2,966,500");
    await expect(chart).toContainText("Up 8% on the week before");
    await expect(chart.locator(".tk-bar")).toHaveCount(7);
    await axeBoth(owner, "the briefing and a chart");
    if (size === "a phone") {
      await card.scrollIntoViewIfNeeded();
      await owner.waitForTimeout(600);
      await owner.screenshot({ path: "test-results/briefing-phone.png" });
    }
    // Low stock opens the Stock tool; Got it puts the card away for the day.
    await card.getByRole("button", { name: /Low stock/ }).click();
    await expect(owner.getByTestId("tool-sheet").getByRole("heading", { name: /Stock/ })).toBeVisible();
    await owner.getByTestId("tool-sheet").getByRole("button", { name: /^Close/ }).click();
    await card.getByRole("button", { name: /^Got it/ }).click();
    await expect(card).toHaveCount(0);
    expect(sent.find((x) => x.path === "/v1/chat-alerts/later")?.body).toEqual({ key: `briefing:${day(0)}`, hours: 24 });
  });
}

test("a cashier gets no briefing card", async ({ browser }) => {
  const { member } = await people(browser, "cashier");
  await answer(member);
  await member.goto("/chat?with=taskiem");
  await expect(member.getByTestId("taskiem-pane")).toBeVisible();
  await expect(member.getByTestId("briefing-card")).toHaveCount(0);
});
