import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";
import { PUBLIC, familyLink, familySchool } from "./familyschool";

// Automated accessibility checks (axe: WCAG 2.1 A/AA and best practices)
// on the console's pages, in the light and the dark theme, plus keyboard
// checks for the dialogs and the narrow-screen menu. axe is injected into
// the page, which the console's Content Security Policy would refuse.
test.use({ bypassCSP: true });

// The app is light unless the reader picks a theme (src/theme.ts), so the
// emulated colour scheme alone never showed the dark theme: follow the
// device ("system") unless a test picks one itself, so each dark run below
// checks the dark theme.
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    try {
      if (!localStorage.getItem("taskiem-theme")) localStorage.setItem("taskiem-theme", "system");
    } catch {
      // no storage: light
    }
  });
});

const PAGES = [
  "/home",
  "/search",
  "/workflows",
  "/runs",
  "/approvals",
  "/connections",
  "/catalogue",
  "/settings",
  "/policies",
  "/alerts",
  "/audit",
  "/reports",
  "/members",
  "/staff",
  "/tasks",
  "/account",
  "/link-chat",
  "/dashboard",
  "/templates",
  "/settings/keys",
  "/settings/notifications",
  "/settings/payments",
  "/settings/saved",
  "/settings/billing",
  "/storefront",
  "/storefront/look",
  "/storefront/fronts",
  "/storefront/orders",
  "/storefront/bookings",
  "/storefront/riders",
  "/storefront/riders/pay",
  "/storefront/deliveries",
  "/storefront/stock",
  "/storefront/suppliers",
  "/storefront/price-lists",
  "/chat",
  "/settings/areas",
  "/settings/links",
  "/settings/links/profile",
  "/settings/customers",
  "/settings/discover",
  "/bills",
  "/trade-orders",
  "/invoices",
  "/invoices/match",
  "/invoices/settings",
  "/money",
  "/money?period=custom&from=2026-10-01&to=2026-10-31",
  "/money/leaks",
  "/money/statements",
  "/money/entries",
  "/money/transactions",
  "/money/transactions?period=all&direction=out",
  "/money/fees",
  "/payments",
  "/payments/several",
  "/payments/several/list",
  "/payroll",
  "/payroll/settings",
  "/payroll/schedules",
  "/payroll/team",
  "/payroll/account-changes",
  "/payroll/me",
  "/payroll/payslips/0192e2e0-0000-7000-8000-00000000f401",
  "/payroll/me/payslips/0192e2e0-0000-7000-8000-00000000f401",
  "/money/labels",
  "/money/things",
  "/money/things?all=true",
  "/schools",
  "/schools/families",
  "/schools/classes",
  "/schools/import",
  "/schools/students/new",
  "/schools/accounts",
  "/schools/payments",
  "/schools/receipts",
  "/schools/debtors",
  "/schools/clearance",
  "/schools/gate",
  "/settings/schools",
  "/start-something",
  "/pages",
];

async function signIn(page: Page) {
  await page.goto("/login?with=email");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", {
      level: 1,
      name: /^Good (morning|afternoon|evening)/,
    }),
  ).toBeVisible();
}

/** A planned term of a session of its own (made once, whichever theme runs first): its id. */
async function plannedTerm(page: Page): Promise<string> {
  const h = { headers: { "X-Taskiem-Request": "1" } };
  type Sessions = {
    sessions: { id: string; label: string; terms: { id: string }[] }[];
  };
  const find = async () =>
    (
      (await (
        await page.request.get("/v1/schools/sessions")
      ).json()) as Sessions
    ).sessions.find((s) => s.label === "2031/2032");
  let s = await find();
  if (!s) {
    await page.request.post("/v1/schools/sessions", {
      ...h,
      data: { label: "2031/2032" },
    });
    s = await find();
  }
  if (s && s.terms.length === 0) {
    await page.request.post(`/v1/schools/sessions/${s.id}/terms`, {
      ...h,
      data: {
        name: "First term",
        starts_on: "2031-09-15",
        ends_on: "2031-12-12",
        fees_due_on: "2031-09-08",
      },
    });
    s = await find();
  }
  const id = s?.terms[0]?.id;
  expect(id, "the accessibility term").toBeTruthy();
  return id ?? "";
}

/** One of Things we have, added by hand (made once, whichever theme runs
 * first): its id. */
async function aThing(page: Page): Promise<string> {
  type List = { things: { id: string; name: string }[] };
  const find = async () =>
    (
      (await (await page.request.get("/v1/things?all=true")).json()) as List
    ).things.find((t) => t.name === "Accessible generator");
  let t = await find();
  if (!t) {
    const r = await page.request.post("/v1/things", {
      headers: { "X-Taskiem-Request": "1" },
      data: { name: "Accessible generator", cost_kobo: 45_000_000, serial: "G-1" },
    });
    expect(r.status(), await r.text()).toBe(201);
    t = await find();
  }
  expect(t?.id, "the accessibility thing").toBeTruthy();
  return t?.id ?? "";
}

/** A run of a workflow that waits (made once, whichever theme runs first): its id. */
async function aRun(page: Page): Promise<string> {
  const h = { headers: { "X-Taskiem-Request": "1" } };
  type List = { workflows: { id: string; name: string }[] };
  let wf = (
    (await (await page.request.get("/v1/workflows")).json()) as List
  ).workflows.find((w) => w.name === "Accessible run");
  if (!wf) {
    const definition = {
      schema: "wd/v1",
      id: "wf_a11yrun",
      version: 1,
      name: "Accessible run",
      trigger: { type: "manual" },
      steps: [
        { id: "change", type: "transform", config: { output: { ok: true } } },
        {
          id: "wait",
          type: "wait",
          needs: ["change"],
          config: { duration: "1h" },
        },
      ],
    };
    const made = (await (
      await page.request.post("/v1/workflows", {
        ...h,
        data: { name: "Accessible run", definition },
      })
    ).json()) as { id: string };
    expect(
      (
        await page.request.post(
          `/v1/workflows/${made.id}/versions/1/publish`,
          h,
        )
      ).ok(),
      "publish the accessibility workflow",
    ).toBeTruthy();
    wf = { id: made.id, name: "Accessible run" };
  }
  const run = (await (
    await page.request.post(`/v1/workflows/${wf.id}/runs`, {
      ...h,
      data: { input: {} },
    })
  ).json()) as { run_id?: string };
  expect(run.run_id, "the accessibility run").toBeTruthy();
  return run.run_id ?? "";
}

/** Opens a page and waits until it has drawn its data. */
async function open(page: Page, path: string) {
  await page.goto(path);
  await expect(page.locator("main h1").first()).toBeVisible();
  await expect(page.locator(".skeleton")).toHaveCount(0);
}

for (const scheme of ["light", "dark"] as const) {
  test(`pages pass axe in the ${scheme} theme`, async ({ page }) => {
    test.setTimeout(180_000);
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await page.goto("/login?with=email");
    await expect(
      page.getByRole("heading", { name: "Sign in to Taskiem" }),
    ).toBeVisible();
    const found: string[] = [];
    const check = async (where: string) => {
      const vs = await axe(page);
      if (vs.length > 0) found.push(describe(where, vs));
    };
    await check("/login");
    // The connect page a chat's link opens, signed out (connect_links.go):
    // a link that has ended, and how a consent from one ended.
    // And how Add to Slack ended, when Slack did not open the bot.
    for (const path of [
      `/c/${"A".repeat(32)}`,
      "/c/done?service=Google&error=You+did+not+approve",
      "/c/done?service=Google&connected=1",
      "/slack/added",
      "/slack/added?error=You+did+not+add+Taskiem+to+Slack.",
      "/confirm-email",
      // A sign-in link from chat that is not good (chat_signin.go).
      "/signin#token=nope",
      // The phone way in (taskiem-for-everyone §5.1).
      "/login",
    ]) {
      await page.goto(path);
      await expect(page.locator("main h1").first()).toBeVisible();
      await check(path);
    }
    await signIn(page);
    for (const path of PAGES) {
      await open(page, path);
      await check(path);
    }
    // A term's fees and invoices: a term of its own, not started, so the
    // school's current term (schools.spec.ts) is left alone.
    const term = await plannedTerm(page);
    for (const path of [
      `/schools/terms/${term}/fees`,
      `/schools/terms/${term}/invoices`,
    ]) {
      await open(page, path);
      await check(path.replace(term, ":id"));
    }
    // One of Things we have, on its page.
    const thing = await aThing(page);
    await open(page, `/money/things/${thing}`);
    await expect(page.getByTestId("thing-hero")).toBeVisible();
    await check("/money/things/:id");
    // A run, live, on its canvas and its timeline.
    const run = await aRun(page);
    await open(page, `/runs/${run}`);
    await expect(
      page.getByLabel("Run canvas").getByTestId("node-wait"),
    ).toHaveAttribute("data-status", "waiting", { timeout: 30_000 });
    await check("/runs/:id");
    await page.getByRole("tab", { name: "Timeline" }).click();
    await check("/runs/:id (timeline)");
    // A dialog, and the workflow editor.
    await open(page, "/workflows");
    await page.getByRole("link", { name: "New workflow" }).click();
    await expect(
      page.getByRole("heading", { level: 1, name: "When should it run?" }),
    ).toBeVisible();
    await check("/workflows/new");
    await page.getByLabel("Its name").fill(`Accessible ${scheme} flow`);
    await page.getByRole("button", { name: /^A payment comes in/ }).click();
    await expect(page.getByTestId("node-trigger")).toBeVisible();
    await check("/workflows/:id (empty)");
    // The picker, a step's window, and the canvas with an If after a test.
    await page.getByRole("button", { name: "Add the first step" }).click();
    await expect(
      page.getByRole("dialog", { name: "What happens next?" }),
    ).toBeVisible();
    await check("/workflows/:id (what happens next)");
    await page
      .getByRole("dialog", { name: "What happens next?" })
      .getByLabel("Search")
      .fill("change the data");
    await page.getByRole("button", { name: /^Change the data/ }).click();
    await expect(page.getByTestId("step-window")).toBeVisible();
    await check("/workflows/:id (a step's window)");
    await page
      .getByTestId("step-window")
      .getByRole("button", { name: "Done" })
      .click();
    await page
      .getByRole("button", { name: "Add a step after Change the data" })
      .click();
    await page
      .getByRole("dialog", { name: "What happens next?" })
      .getByLabel("Search")
      .fill("if");
    await page.getByRole("button", { name: /^If Go one way/ }).click();
    await page
      .getByTestId("step-window")
      .getByRole("button", { name: "Done" })
      .click();
    await page
      .getByRole("button", { name: "Add a step to this path" })
      .first()
      .click();
    await page
      .getByRole("dialog", { name: "What happens next?" })
      .getByLabel("Search")
      .fill("wait");
    await page.getByRole("button", { name: /^Wait An hour/ }).click();
    await page
      .getByTestId("step-window")
      .getByRole("button", { name: "Done" })
      .click();
    await page.getByRole("button", { name: "Test workflow" }).click();
    await expect(page.getByText("Tested with sample data")).toBeVisible();
    await check("/workflows/:id");
    expect(found, found.join("\n\n")).toEqual([]);
  });
}

// The family page on the public host (docs/schools.md "The family page")
// is drawn in the school's theme: every theme passes, as does the page an
// expired or unknown link answers.
test("the family page passes axe in every public theme", async ({
  page,
  browser,
}) => {
  test.setTimeout(240_000);
  const parent = await (await browser.newContext()).newPage();
  for (const theme of ["clean", "bold", "editorial"]) {
    await familySchool(page, `Axe ${theme} Academy`, theme);
    const link = await familyLink(page);
    for (const path of [
      link,
      `${link}?done=paid`,
      `${link}?err=amount&for=paid`,
    ]) {
      await parent.goto(path);
      await expect(
        parent.getByRole("heading", { level: 1, name: "School fees" }),
      ).toBeVisible();
      await expect(parent.locator(`link[href*="theme-${theme}."]`)).toHaveCount(
        1,
      );
      const vs = await axe(parent);
      expect(vs, describe(`${path} (${theme})`, vs)).toEqual([]);
    }
    await parent.goto(`${PUBLIC}/s/${"B".repeat(32)}`);
    await expect(
      parent.getByRole("heading", {
        level: 1,
        name: "Ask the school for a new link",
      }),
    ).toBeVisible();
    const vs = await axe(parent);
    expect(vs, describe(`an unknown link (${theme})`, vs)).toEqual([]);
  }
});

// A person's own side (taskiem-for-everyone §7), as a phone signs in to
// it: the server's answers stood in for, so every state is drawn (you.spec.ts
// goes through them for real).
const PERSON = {
  tenant_id: null,
  personal: true,
  roles: [],
  permissions: [],
  auth_method: "phone",
  user: {
    id: "0192e2e0-0000-7000-8000-00000000a001",
    email: "person+a11y@phone.taskiem.invalid",
    email_placeholder: true,
    name: "Ada Obi",
    phone: "+2348031234567",
  },
  businesses: [
    { id: "0192e2e0-0000-7000-8000-00000000b001", name: "Ada Kitchen", kind: "business", locked: true, lock: "second_proof_required" },
    { id: "0192e2e0-0000-7000-8000-00000000b002", name: "Ada Obi's workflows", kind: "workflows", locked: false, lock: null },
  ],
  second_proof: { required: true, device_proved: false, ways: { passkey: false, email: true, pin: true } },
};

for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`the way in and You pass axe and fit ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const found: string[] = [];
      const check = async (where: string) => {
        const vs = await axe(page);
        if (vs.length > 0) found.push(describe(where, vs));
        expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
      };
      const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
      await page.route("**/v1/auth/phone/start", (r) => r.fulfill(json({ sent_via: "whatsapp", expires_in: 600 }, 202)));
      await page.route("**/v1/auth/phone/verify", (r) => r.fulfill(json({ status: "new", ticket: "a11y", expires_in: 900 })));
      await page.goto("/login");
      await expect(page.getByRole("heading", { name: "Welcome to Taskiem" })).toBeVisible();
      await check("the way in");
      await page.getByLabel("Your phone number").fill("0803 123 4567");
      await page.getByRole("button", { name: "Continue" }).click();
      await expect(page.getByRole("heading", { name: "Enter the code" })).toBeVisible();
      await check("the code");
      await page.getByLabel("6-digit code").fill("123456");
      await page.getByRole("button", { name: "Continue" }).click();
      await expect(page.getByRole("heading", { name: "Make your account" })).toBeVisible();
      await check("a new account");

      // Signed in, on You.
      await page.route("**/v1/me", (r) => r.fulfill(json(PERSON)));
      // No chats yet, and no orders from shops (stage 2).
      await page.route(/\/v1\/me\/chats(\?|$)/, (r) => r.fulfill(json({ chats: [], unread: 0 })));
      await page.route(/\/v1\/me\/conversations(\?|$)/, (r) => r.fulfill(json({ chats: [], unread: 0, requests: 0 })));
      await page.route("**/v1/me/storefront-orders?*", (r) => r.fulfill(json({ orders: [], waiting: false, waiting_count: 0 })));
      // No saved addresses yet (Settings).
      await page.route("**/v1/me/notifications", (r) => r.fulfill(json({ messages: true, updates: true, offers: true })));
      // No push keys here: this browser's notifications are not offered.
      await page.route("**/v1/me/push/key", (r) => r.fulfill(json({ error: "not found" }, 404)));
      await page.route("**/v1/me/addresses", (r) => r.fulfill(json({ addresses: [] })));
      await page.route(/\/v1\/me\/saved(\?|$)/, (r) => r.fulfill(json({ saved: [], limit: 500 })));
      await page.route(/\/v1\/me\/saved-kinds(\?|$)/, (r) => r.fulfill(json({ kinds: [], starters: [], limit: 40, to_change: true })));
      await page.route("**/v1/me/invitations", (r) =>
        r.fulfill(json({ invitations: [{ tenant_id: "0192e2e0-0000-7000-8000-00000000b003", tenant: "Bola Stores", roles: ["staff"], invited_at: new Date().toISOString() }] })),
      );
      await page.goto("/you");
      await expect(page.getByRole("heading", { name: "Hello, Ada" })).toBeVisible();
      await check("/you");
      await page.getByTestId("switcher").click();
      await expect(page.getByRole("dialog", { name: "Switch to" })).toBeVisible();
      await check("the switcher");
      await page.route("**/v1/me/switch", (r) =>
        r.fulfill(json({ error: "new device", second_proof_required: true, ways: PERSON.second_proof.ways }, 401)),
      );
      await page.getByRole("dialog").getByRole("button", { name: /Ada Kitchen/ }).click();
      await expect(page.getByTestId("second-proof")).toBeVisible();
      await check("a second proof");
      await page.getByRole("button", { name: "Not now" }).click();
      await page.goto("/you/settings");
      await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
      await check("/you/settings");
      await page.getByRole("button", { name: "Change number" }).click();
      await expect(page.getByTestId("change-number")).toBeVisible();
      await check("change number");
      await page.goto("/you/start");
      await page.getByTestId("start-business").click();
      await expect(page.getByLabel("Business name")).toBeVisible();
      await check("/you/start");
      expect(found, found.join("\n\n")).toEqual([]);
    });

// A person's chat with a business and a chat link's page (taskiem-for-
// everyone §8, §9), the server's answers stood in for (customer-chat.spec.ts
// goes through them for real and through every tool).
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`a person's chat and a chat link pass axe and fit ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const found: string[] = [];
      const check = async (where: string) => {
        const vs = await axe(page);
        if (vs.length > 0) found.push(describe(where, vs));
        expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
      };
      const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
      const now = new Date().toISOString();
      await page.route("**/v1/chat-links/a11ya11ya1", (r) =>
        r.fulfill(json({ name: "Mama's Kitchen", area: "Yaba", open_now: true, takes_chats: true, code: "a11ya11ya1", link: "http://localhost/m/a11ya11ya1", words: "", app: "https://taskiem.test/app" })),
      );
      await page.goto("/m/a11ya11ya1");
      await expect(page.getByTestId("chat-here")).toBeVisible();
      await check("/m/{code}");
      // A business with its own WhatsApp number (§11): both ways to chat,
      // then WhatsApp alone with chats in Taskiem off.
      const wa = { number: "+2348000000020", link: "https://wa.me/2348000000020" };
      for (const [code, takes, choices] of [["a11ya11yw1", true, ["whatsapp", "taskiem"]], ["a11ya11yw2", false, ["whatsapp"]]] as const) {
        await page.route(`**/v1/chat-links/${code}`, (r) =>
          r.fulfill(json({ name: "Mama's Kitchen", area: "Yaba", open_now: true, takes_chats: takes, code, link: `http://localhost/m/${code}`, words: "", whatsapp: wa, choices })),
        );
        await page.goto(`/m/${code}`);
        await expect(page.getByTestId("chat-on-whatsapp")).toBeVisible();
        await check(`/m/{code} offering ${choices.join(" and ")}`);
      }
      const chat = { id: "c1", business: "b1", name: "Mama's Kitchen", last: "Hello", last_mine: false, last_at: now, unread: 0, muted: false, archived: false, blocked: false, blocked_you: false, offers: "unasked", ask_offers: true, hours: { open: true, line: "Open now" }, verified: { phone: true, business: true, payments: true, months: 14 }, lines: ["app", "whatsapp"] };
      await page.route("**/v1/me", (r) => r.fulfill(json({ ...PERSON, businesses: [], second_proof: undefined })));
      await page.route("**/v1/me/invitations", (r) => r.fulfill(json({ invitations: [] })));
      await page.route("**/v1/me/chats/c1", (r) => r.fulfill(json(chat)));
      await page.route("**/v1/me/chats/c1/messages", (r) =>
        r.fulfill(
          json({
            messages: [
              { id: "m1", mine: false, kind: "text", body: "Hello", read_at: null, created_at: now },
              { id: "m2", mine: false, kind: "card", body: "Order", card: { type: "order", reference: "MK-1", title: "Order MK-1", line: "1 × Jollof rice", status: "received", total_kobo: 250_000 }, read_at: null, created_at: now },
              { id: "m3", mine: true, kind: "text", body: "Thank you", read_at: now, created_at: now },
              { id: "m3w", mine: false, kind: "text", body: "Your rice is packed", read_at: now, created_at: now, line: "whatsapp" },
              { id: "m3x", mine: true, kind: "text", body: "I'm on my way", read_at: now, created_at: now, line: "whatsapp" },
              { id: "m4", mine: false, kind: "text", body: "Pay to 0123456789 at GTBank", warn_transfer: true, read_at: null, created_at: now },
              { id: "m5", mine: false, kind: "card", body: "Invoice", card: { type: "invoice", reference: "INV-0001", invoice_id: "f1", title: "Invoice INV-0001", status: "part_paid", total_kobo: 150_000, url: "https://pay.example.test/i" }, read_at: null, created_at: now },
              { id: "m6", mine: false, kind: "card", body: "Delivery", card: { type: "delivery", reference: "MK-1", title: "Delivery of MK-1", line: "On its way to you", status: "picked_up", delivery: { status: "picked_up", line: "On its way to you" } }, read_at: null, created_at: now },
            ],
            more: false,
          }),
        ),
      );
      await page.route("**/v1/me/chats/c1/read", (r) => r.fulfill({ status: 204 }));
      await page.route("**/v1/me/chats/c1/orders", (r) => r.fulfill(json({ orders: [] })));
      // The invoice's account number, to pay it by transfer (the payments flow, part 1).
      await page.route("**/v1/me/chats/c1/invoices/f1/transfer", (r) =>
        r.fulfill(
          json({
            invoice: "f1",
            reference: "INV-0001",
            due_kobo: 100_000,
            state: "open",
            bank_name: "Wema Bank",
            account_number: "0123456789",
            account_name: "Mama's Kitchen",
            amount_kobo: 100_000,
            expires_at: new Date(Date.now() + 3 * 60_000).toISOString(),
          }),
        ),
      );
      // A server with no push keys: "We'll text you when they answer."
      await page.route("**/v1/me/push/key", (r) => r.fulfill(json({ error: "not found" }, 404)));
      await page.goto("/you/chats/c1");
      await expect(page.getByTestId("offers-question")).toBeVisible();
      await expect(page.getByTestId("transfer-warning")).toBeVisible();
      await expect(page.getByTestId("tell-me-line")).toHaveText("We'll text you when they answer.");
      await expect(page.getByTestId("person-trust")).toHaveText("Phone checked · Business checked · Payments through Taskiem · Trading 1 year");
      await expect(page.getByTestId("person-whatsapp-here")).toBeVisible();
      await expect(page.getByTestId("person-on-whatsapp")).toHaveCount(2);
      // The number in its last minutes: the warning colour.
      await expect(page.getByTestId("customer-transfer-card").getByTestId("transfer-time")).toHaveText(/^Expires in 2:\d\d$/);
      await check("/you/chats/{id}");
      expect(found, found.join("\n\n")).toEqual([]);
    });

// Orders, Discover and the saved addresses on You (taskiem-for-everyone
// §7, §10, §13), the server's answers stood in for (discover-orders.spec.ts
// goes through them on the real server too).
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`Orders, Discover and addresses on You pass axe and fit ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const found: string[] = [];
      const check = async (where: string) => {
        const vs = await axe(page);
        if (vs.length > 0) found.push(describe(where, vs));
        expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
      };
      const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
      const now = new Date().toISOString();
      const b1 = "0192e2e0-0000-7000-8000-00000000b201";
      const order = { business_id: b1, business: "Mama's Kitchen", reference: "MK-1", kind: "order", state: "awaiting_payment", summary: "2 × Jollof rice", total_kobo: 900_000, due_kobo: 900_000, pay: true, made_at: now, chat_id: "c1" };
      await page.route("**/v1/me", (r) => r.fulfill(json({ ...PERSON, businesses: [], second_proof: undefined })));
      await page.route(/\/v1\/me\/saved(\?|$)/, (r) => r.fulfill(json({ saved: [{ id: "s1", owner: "mine", name: "mum", title: "Mum", kind: "bank_account", hint: "GTBank ••6789", details: { bank_name: "Guaranty Trust Bank", account_number: "••6789", note: "For her birthday" }, check: { state: "unchecked", account_name: null, via: null, at: null }, to_change: true, created_at: now, updated_at: now }], limit: 500 })));
      await page.route(/\/v1\/me\/saved-kinds(\?|$)/, (r) => r.fulfill(json({ kinds: [], starters: [], limit: 40, to_change: true })));
      await page.route("**/v1/me/invitations", (r) => r.fulfill(json({ invitations: [] })));
      const earlier = { ...order, reference: "MK-E", state: "completed", summary: "", due_kobo: 0, pay: false, earlier: true };
      await page.route(/\/v1\/me\/orders(\?|$)/, (r) => r.fulfill(json({ orders: [order, { ...order, reference: "MK-0", state: "completed", due_kobo: 0, pay: false }, earlier], next: "x", waiting_count: 1 })));
      await page.route(`**/v1/me/orders/${b1}/MK-E`, (r) => r.fulfill(json({ ...earlier, lines: [], paid_kobo: 0 })));
      await page.route(`**/v1/me/orders/${b1}/MK-1`, (r) =>
        r.fulfill(json({ ...order, lines: [{ name: "Jollof rice", quantity: 2, price_kobo: 450_000, line_kobo: 900_000 }], fulfilment: "delivery", zone: "Yaba", paid_kobo: 0, address: { address: "12 Allen Avenue, Ikeja", landmark: "By the blue gate" }, delivery: { status: "accepted", line: "A rider is going to collect it" } })),
      );
      // You's settings read which pushes the person gets.
      await page.route("**/v1/me/notifications", (r) => r.fulfill(json({ messages: true, updates: true, offers: true })));
      // No push keys here: this browser's notifications are not offered.
      await page.route("**/v1/me/push/key", (r) => r.fulfill(json({ error: "not found" }, 404)));
      await page.route("**/v1/me/addresses", (r) =>
        r.fulfill(json({ addresses: [{ id: "a1", label: "Home", line: "12 Allen Avenue", area: "Ikeja", landmark: "By the blue gate", phone: "+2348031234567", default: true, created_at: now, updated_at: now }] })),
      );
      await page.route("**/v1/discover/categories", (r) => r.fulfill(json({ categories: [{ key: "food", name: "Food and drinks" }, { key: "fashion", name: "Fashion" }] })));
      await page.route("**/v1/discover/search", (r) =>
        r.fulfill(json({ results: [{ business: b1, name: "Mama's Kitchen", photo: {}, category: "food", category_name: "Food and drinks", area: "Yaba", open_now: true, line: "Open now, closes at 18:00", can_chat: true }], more: true })),
      );
      await page.route(`**/v1/discover/${b1}`, (r) =>
        r.fulfill(
          json({ business: b1, name: "Mama's Kitchen", tagline: "Hot jollof", category: "food", category_name: "Food and drinks", area: "Yaba", address: "3 Montgomery Road", map_link: "https://maps.example.test", photos: [], hours: { days: [[{ open: "08:00", close: "18:00" }], null, null, null, null, null, null] }, open_now: true, line: "Open now, closes at 18:00", shop: "https://mamas.example.test", can_chat: true, has_shop: true, has_book: true }),
        ),
      );
      await page.goto("/you/orders");
      await expect(page.getByTestId("you-order")).toHaveCount(3);
      await check("/you/orders");
      await page.goto(`/you/orders/${b1}/MK-1`);
      await expect(page.getByTestId("you-order-address")).toBeVisible();
      await check("/you/orders/{business}/{reference}");
      await page.goto(`/you/orders/${b1}/MK-E`);
      await expect(page.getByTestId("you-order-earlier")).toBeVisible();
      await check("an earlier order");
      await page.goto("/you/discover");
      await expect(page.getByTestId("discover-result")).toHaveCount(1);
      await check("/you/discover");
      await page.goto(`/you/discover/${b1}`);
      await expect(page.getByTestId("discover-chat")).toBeVisible();
      await check("/you/discover/{business}");
      await page.getByTestId("discover-report").click();
      await expect(page.getByTestId("discover-report-sheet")).toBeVisible();
      await check("reporting a business");
      await page.route(/\/v1\/me\/chats(\?|$)/, (r) => r.fulfill(json({ chats: [], unread: 0 })));
      await page.route(/\/v1\/me\/conversations(\?|$)/, (r) => r.fulfill(json({ chats: [], unread: 0, requests: 0 })));
      await page.goto("/you/settings");
      await expect(page.getByTestId("saved-address")).toBeVisible();
      await check("/you/settings with addresses");
      await page.getByRole("button", { name: "Change Home" }).click();
      await expect(page.getByTestId("address-form")).toBeVisible();
      await check("changing an address");
      await page.getByRole("button", { name: "Cancel" }).click();
      // The person's own saved accounts (saved objects S0).
      await page.route("**/v1/me/banks", (r) => r.fulfill(json({ banks: [{ code: "", name: "Guaranty Trust Bank" }], from: "" })));
      await expect(page.getByTestId("my-saved").getByTestId("saved-row")).toContainText("@mum · GTBank ••6789");
      await page.getByRole("button", { name: "Change @mum" }).click();
      await expect(page.getByTestId("saved-form")).toBeVisible();
      await check("changing a saved account");
      expect(found, found.join("\n\n")).toEqual([]);
    });

// You's settings (§7, §14): the photo, notifications, privacy and
// deleting the account (discover-orders.spec.ts goes through them for real).
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`You's photo, notifications, privacy and deleting pass axe and fit ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const found: string[] = [];
      const check = async (where: string) => {
        const vs = await axe(page);
        if (vs.length > 0) found.push(describe(where, vs));
        expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
      };
      const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
      const b1 = "0192e2e0-0000-7000-8000-00000000b301";
      await page.route("**/v1/me", (r) =>
        r.fulfill(
          json({
            ...PERSON,
            businesses: [],
            second_proof: undefined,
            factors: { passkey: false, totp: false, password: true },
            user: { ...PERSON.user, photo: { url: "/brand/seal/mark-512.png", updated_at: new Date().toISOString() } },
          }),
        ),
      );
      await page.route(/\/v1\/me\/saved(\?|$)/, (r) => r.fulfill(json({ saved: [], limit: 500 })));
      await page.route(/\/v1\/me\/saved-kinds(\?|$)/, (r) => r.fulfill(json({ kinds: [], starters: [], limit: 40, to_change: true })));
      await page.route("**/v1/me/invitations", (r) => r.fulfill(json({ invitations: [] })));
      await page.route("**/v1/me/addresses", (r) => r.fulfill(json({ addresses: [] })));
      await page.route(/\/v1\/me\/chats(\?|$)/, (r) => r.fulfill(json({ chats: [], unread: 0 })));
      await page.route(/\/v1\/me\/conversations(\?|$)/, (r) => r.fulfill(json({ chats: [], unread: 0, requests: 0 })));
      await page.route("**/v1/me/notifications", (r) => r.fulfill(json({ messages: true, updates: true, offers: false })));
      // The server has push keys: this browser's notifications are offered.
      await page.route("**/v1/me/push/key", (r) => r.fulfill(json({ public_key: "BEl62iUYgUivxIkv69yViEuiBIa-Ib9-SkvMeAtA3LFgDzkrxZJjSgSnfckjBJuBkr3qBUYIHBQFLXYp5Nksh8U" })));
      await page.route("**/v1/me/privacy", (r) =>
        r.fulfill(
          json({
            shown_to_businesses: { name: "Ada Obi", phone: "+2348031234567", photo: false },
            businesses: [
              { business_id: b1, business: "Mama's Kitchen", chat_id: "0192e2e0-0000-7000-8000-00000000c301", orders: 3, last_order_at: "2026-11-02T10:00:00Z" },
              { business_id: "0192e2e0-0000-7000-8000-00000000b302", business: "Bola Salon", chat_id: null, orders: 1, last_order_at: "2026-10-12T10:00:00Z" },
            ],
            erase: { words: "Each business keeps its own records under Nigeria's Data Protection Act. To ask one to erase what it holds about you, ask it in its chat.", how: "ask_in_chat" },
          }),
        ),
      );
      await page.route("**/v1/me/delete", (r) => r.fulfill(json({ error: "You own a business on Taskiem.", owns_business: true, businesses: ["Ada Kitchen", "Ada Prints"] }, 409)));

      await page.goto("/you/settings");
      await expect(page.getByTestId("you-photo").getByText("Only you see it.")).toBeVisible();
      await expect(page.getByTestId("you-notify-offers")).not.toBeChecked();
      await expect(page.getByTestId("you-push-on")).toHaveText("Notify me on this browser");
      await check("/you/settings with a photo and notifications");
      await page.goto("/you/settings/privacy");
      await expect(page.getByTestId("privacy-business")).toHaveCount(2);
      await check("/you/settings/privacy");
      await page.goto("/you/settings/delete");
      await expect(page.getByTestId("delete-proof")).toBeVisible();
      await check("/you/settings/delete");
      await page.getByRole("radio", { name: "Your password" }).check();
      await page.getByTestId("delete-proof").locator("input[type=password]").fill("pw");
      await page.getByTestId("delete-sure").check();
      await page.getByTestId("delete-account").click();
      await expect(page.getByTestId("delete-blocked")).toBeVisible();
      await check("deleting refused, businesses named");
      await page.goto("/account-deleted?told=1");
      await expect(page.getByTestId("account-deleted")).toBeVisible();
      await check("/account-deleted");
      expect(found, found.join("\n\n")).toEqual([]);
    });

test("dialogs keep focus inside and give it back when they close", async ({
  page,
}) => {
  await signIn(page);
  await page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name: "Workflows" })
    .click();
  await page.getByRole("link", { name: "Forms", exact: true }).click();
  const opener = page
    .getByRole("button", { name: /^(New form|Make your first form)$/ })
    .first();
  await opener.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  // Focus starts on the first field and Tab stays inside.
  await expect(dialog.getByLabel("What the form is called")).toBeFocused();
  for (let i = 0; i < 6; i++) {
    await page.keyboard.press("Tab");
    expect(
      await dialog.evaluate((d) => d.contains(document.activeElement)),
    ).toBe(true);
  }
  await page.keyboard.press("Shift+Tab");
  expect(await dialog.evaluate((d) => d.contains(document.activeElement))).toBe(
    true,
  );
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
});

test.describe("on a phone-sized screen", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the menu opens as a drawer, works by keyboard, and pages fit the width", async ({
    page,
  }) => {
    await signIn(page);
    const menu = page.getByRole("button", { name: "Open menu" });
    const nav = page.getByRole("navigation", { name: "Main" });
    await expect(menu).toBeVisible();
    await expect(nav).toBeHidden();
    await expect(menu).toHaveAttribute("aria-expanded", "false");

    // Opens by keyboard; Tab stays in the drawer; Escape closes it and focus returns.
    await menu.focus();
    await page.keyboard.press("Enter");
    await expect(nav).toBeVisible();
    await expect(menu).toHaveAttribute("aria-expanded", "true");
    for (let i = 0; i < 25; i++) {
      await page.keyboard.press("Tab");
      expect(
        await nav.evaluate((n) => n.contains(document.activeElement)),
      ).toBe(true);
    }
    const found: string[] = [];
    let vs = await axe(page);
    if (vs.length > 0) found.push(describe("drawer open", vs));
    await page.keyboard.press("Escape");
    await expect(nav).toBeHidden();
    await expect(menu).toBeFocused();

    // Following a link closes the drawer.
    await menu.click();
    await nav.getByRole("link", { name: "Activity" }).click();
    await expect(
      page.getByRole("heading", { name: "Activity", exact: true }),
    ).toBeVisible();
    await expect(nav).toBeHidden();

    // No page scrolls sideways; wide tables scroll inside themselves.
    for (const path of [
      "/workflows",
      "/workflows/new",
      "/runs",
      "/connections",
      "/audit",
      "/members",
      "/staff",
      "/tasks",
      "/alerts",
      "/settings",
      "/dashboard",
      "/storefront",
      "/storefront/fronts",
      "/storefront/orders",
      "/storefront/bookings",
      "/storefront/riders",
      "/storefront/deliveries",
      "/storefront/stock",
      "/storefront/suppliers",
      "/storefront/price-lists",
      "/chat",
      "/settings/links",
      "/settings/links/profile",
      "/trade-orders",
      "/schools",
      "/schools/classes",
    ]) {
      await open(page, path);
      const wide = await page.evaluate(
        () => document.documentElement.scrollWidth,
      );
      expect(wide, `${path} is ${wide}px wide`).toBeLessThanOrEqual(390);
      vs = await axe(page);
      if (vs.length > 0) found.push(describe(`${path} (390px)`, vs));
    }
    expect(found, found.join("\n\n")).toEqual([]);
  });
});

// Chats with people on You (people-chatting.md §5, §7, §8, §10.1, §11): the
// one Chats list with Requests, a chat with ticks, a reply, a forward, a
// warning, cards and a share card, a request, New chat with someone found,
// contacts, the privacy settings, blocked people and the bank account; the
// server's answers stood in for (people.spec.ts goes through them on the
// real server).
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`chats with people pass axe and fit ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const found: string[] = [];
      const check = async (where: string) => {
        const vs = await axe(page);
        if (vs.length > 0) found.push(describe(where, vs));
        expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
      };
      const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
      const now = new Date().toISOString();
      const card = (id: string, extra: Record<string, unknown> = {}) => ({ id, name: "Kemi Adeyemi", deleted: false, contact_name: null, in_contacts: true, they_added_you: true, photo: null, last_seen_at: now, online: false, read_ticks: true, ...extra });
      const kemi = card("p1", { contact_name: "Kemi (school)", online: true });
      const chat = { id: "c1", kind: "person", person_id: "p1", name: "Kemi (school)", gone: false, last: "See you", last_mine: false, last_at: now, unread: 0, muted: false, archived: false, request: false, waiting: false, blocked: false, contact: true, phone: "+2348031234567", person: kemi };
      const req = { ...chat, id: "r1", person_id: "p2", name: "Tunde Bello", request: true, contact: false, phone: null, person: card("p2", { name: "Tunde Bello", in_contacts: false }) };
      await page.route("**/v1/me", (r) => r.fulfill(json({ ...PERSON, businesses: [], second_proof: undefined })));
      await page.route("**/v1/me/invitations", (r) => r.fulfill(json({ invitations: [] })));
      await page.route(/\/v1\/me\/conversations(\?|$)/, (r) =>
        r.fulfill(
          json({
            chats: [
              { kind: "person", id: "c1", name: "Kemi (school)", person_id: "p1", last: "See you", last_mine: false, last_at: now, unread: 2, muted: false, archived: false, blocked: false, person: kemi },
              { kind: "business", id: "b1c", name: "Mama's Kitchen", business_id: "b1", last: "Your rice is ready", last_mine: false, last_at: now, unread: 0, muted: true, archived: false, blocked: false, verified: { phone: true, business: true, payments: false, months: 3 } },
            ],
            unread: 2,
            requests: 1,
          }),
        ),
      );
      await page.route("**/v1/me/people/c1", (r) => r.fulfill(json(chat)));
      await page.route("**/v1/me/people/r1", (r) => r.fulfill(json(req)));
      await page.route(/\/v1\/me\/people\/(c1|r1)\/read$/, (r) => r.fulfill({ status: 204 }));
      await page.route(/\/v1\/me\/people\/c1\/messages(\?|$)/, (r) =>
        r.fulfill(
          json({
            messages: [
              { id: "m1", mine: false, kind: "text", body: "Are you coming on Saturday?", read_at: now, created_at: now },
              { id: "m2", mine: true, kind: "text", body: "Yes, by noon", reply_to: "m1", status: "read", read_at: now, created_at: now, their_reaction: "👍" },
              { id: "m3", mine: true, kind: "text", body: "On my way", status: "delivered", read_at: null, created_at: now },
              { id: "m3b", mine: true, kind: "text", body: "Are you there?", status: "sent", read_at: null, created_at: now },
              { id: "m4", mine: false, kind: "text", body: "Look at this", forwarded: true, forwarded_many: true, read_at: now, created_at: now },
              { id: "m5", mine: false, kind: "text", body: "Pay into my account 0123456789", warn_transfer: true, read_at: now, created_at: now },
              { id: "m6", mine: false, kind: "card", body: "", card: { type: "product", title: "Jollof rice", business: "Mama's Kitchen", business_id: "b1", item_id: "1", price_kobo: 450_000 }, read_at: now, created_at: now },
              { id: "m7", mine: true, kind: "card", body: "", card: { type: "split", title: "Your share", amount_kobo: 300_000, what: "the cake", pay_to: { bank: "GTBank", account_number: "0123456789", account_name: "Ada Obi" }, paid: false }, status: "read", read_at: now, created_at: now },
              { id: "m8", mine: false, kind: "text", body: "This message was deleted", deleted: true, read_at: now, created_at: now },
              { id: "m9", mine: false, kind: "location", body: "", location: { lat: 6.5095, lng: 3.3711, label: "The gate on Herbert Macaulay" }, read_at: now, created_at: now },
            ],
            more: false,
          }),
        ),
      );
      await page.route(/\/v1\/me\/people\/r1\/messages(\?|$)/, (r) =>
        r.fulfill(json({ messages: [{ id: "q1", mine: false, kind: "text", body: "Hello, we met at the market", warn: true, read_at: null, created_at: now }], more: false })),
      );
      await page.route("**/v1/me/people/requests", (r) => r.fulfill(json({ requests: [req] })));
      const saturday = { id: "m1", mine: false, kind: "text", body: "Are you coming on Saturday?", read_at: now, created_at: now, starred: true };
      await page.route(/\/v1\/me\/people\/search\?/, (r) => r.fulfill(json({ results: [{ chat_id: "c1", chat, message: saturday, snippet: "Are you coming on Saturday?" }], more: true, next: "x" })));
      await page.route(/\/v1\/me\/people\/starred(\?|$)/, (r) => r.fulfill(json({ starred: [{ chat_id: "c1", chat, message: saturday, starred_at: now }], more: false })));
      await page.route(/\/v1\/me\/people(\?|$)/, (r) => r.fulfill(json({ chats: [chat], unread: 0, requests: 1 })));
      // Groups are searched, starred and invited beside (stage 5b): none here.
      await page.route(/\/v1\/me\/groups\/search\?/, (r) => r.fulfill(json({ results: [], more: false })));
      await page.route(/\/v1\/me\/groups\/starred(\?|$)/, (r) => r.fulfill(json({ starred: [], more: false })));
      await page.route("**/v1/me/groups/invites", (r) => r.fulfill(json({ invites: [] })));
      await page.route(/\/v1\/me\/groups(\?|$)/, (r) => r.fulfill(json({ groups: [], unread: 0, invites: 0, more: false })));
      await page.route("**/v1/me/people/blocked", (r) => r.fulfill(json({ people: [{ person_id: "p3", name: "Dayo Ola", chat_id: "c3", at: now }] })));
      await page.route("**/v1/me/contacts", (r) =>
        r.fulfill(json({ contacts: [{ ...kemi, source: "number", added_at: now }, { ...card("p4", { name: "Bola Ade", last_seen_at: null, online: null }), source: "link", added_at: now }] })),
      );
      await page.route("**/v1/me/privacy/people", (r) =>
        r.fulfill(json({ find_by_number: "contacts", find_from_contacts: true, message: "everyone", last_seen: "contacts", read_ticks: true, photo: "contacts", show_number: false })),
      );
      await page.route("**/v1/me/payout-account", (r) =>
        r.fulfill(json({ account: { bank: "GTBank", account_number: "0123456789", account_name: "Ada Obi" }, checked: false, words: "Taskiem does not check this account and moves no money." })),
      );
      await page.route("**/v1/people/lookup", (r) => r.fulfill(json({ found: true, person: card("p5", { name: "Chidi Eze", in_contacts: false }), add_token: "t" })));
      // A people chat offers "Tell me when they answer" when the server has keys.
      await page.route("**/v1/me/push/key", (r) => r.fulfill(json({ public_key: "BEl62iUYgUivxIkv69yViEuiBIa-Ib9-SkvMeAtA3LFgDzkrxZJjSgSnfckjBJuBkr3qBUYIHBQFLXYp5Nksh8U" })));
      await page.route("**/v1/me/link", (r) => r.fulfill(json({ code: "ab23cd45ef", url: "http://localhost/p/ab23cd45ef", created_at: now, words: "Chat with me", qr: "/brand/seal/mark-512.png" })));

      await page.goto("/you");
      await expect(page.getByTestId("people-requests-entry")).toBeVisible();
      await expect(page.getByTestId("people-chat")).toBeVisible();
      await expect(page.getByTestId("chat-row-trust")).toHaveText("Phone checked · Business checked · Trading 3 months");
      await check("/you with people and businesses");
      await page.getByTestId("people-new-chat-open").click();
      await page.getByTestId("people-phone").fill("0803 000 1111");
      await page.getByTestId("people-find").click();
      await expect(page.getByTestId("people-found")).toBeVisible();
      await page.getByTestId("people-show-link").click();
      await expect(page.getByTestId("people-qr")).toBeVisible();
      await check("New chat");

      await page.goto("/you/people/c1");
      await expect(page.getByTestId("people-quote")).toBeVisible();
      await expect(page.getByTestId("people-forwarded")).toHaveText(/Forwarded many times/);
      await expect(page.getByTestId("people-transfer-warning")).toBeVisible();
      await expect(page.getByTestId("people-head-line")).toHaveText("online · +234 803 123 4567");
      await expect(page.getByTestId("people-ticks").first()).toBeVisible();
      await expect(page.getByTestId("people-place")).toContainText("The gate on Herbert Macaulay");
      await check("/you/people/{id}");
      // Sending a place: found, seen before it is sent.
      await page.context().grantPermissions(["geolocation"]);
      await page.context().setGeolocation({ latitude: 6.5095, longitude: 3.3711 });
      await page.getByTestId("people-share-button").click();
      await page.getByTestId("share-place").click();
      await check("send a place, asked");
      await page.getByTestId("place-find").click();
      await expect(page.getByTestId("place-preview")).toBeVisible();
      await check("send a place, seen");
      await page.getByTestId("people-place-sheet").getByRole("button", { name: /Close/ }).first().click();
      await page.getByRole("button", { name: "More about this chat" }).click();
      await page.getByTestId("people-info").click();
      await expect(page.getByTestId("people-kept")).toContainText("kept by Taskiem");
      await check("contact info");

      await page.goto("/you/people/requests");
      await expect(page.getByTestId("people-request")).toBeVisible();
      await check("/you/people/requests");
      await page.goto("/you/people/requests/r1");
      await expect(page.getByTestId("request-bar")).toBeVisible();
      await expect(page.getByTestId("people-scam-warning")).toBeVisible();
      await check("/you/people/requests/{id}");
      await page.getByTestId("request-block").click();
      await expect(page.getByTestId("people-report-sheet")).toBeVisible();
      await check("block and report");

      await page.goto("/you/people/search?q=saturday");
      await expect(page.getByTestId("people-found")).toBeVisible();
      await check("/you/people/search");
      await page.goto("/you/people/search?chat=c1&name=Kemi");
      await expect(page.getByRole("heading", { name: "Search in your chat with Kemi" })).toBeVisible();
      await check("/you/people/search in a chat");
      for (const [path, testid] of [
        ["/you/people/starred", "people-starred-row"],
        ["/you/people/contacts", "people-contact"],
        ["/you/settings/people", "people-privacy"],
        ["/you/settings/blocked", "people-blocked"],
        ["/you/settings/payout", "people-payout"],
      ] as const) {
        await page.goto(path);
        await expect(page.getByTestId(testid).first()).toBeVisible();
        await check(path);
      }
      expect(found, found.join("\n\n")).toEqual([]);
    });

// People groups on You (people-chatting.md §9, stage 5b): a group in the one
// Chats list with an invite in Requests, New group with what each add did,
// a group with every kind of message (events, Taskiem's, mentions,
// reactions counted, our ticks, a warning), Read by, a group's info as an
// admin (members, invited, settings, the link and who waits), reporting a
// member, a closed group's notice and appeal, search in a group, and a
// group's link signed out; the server's answers stood in for (groups.spec.ts
// goes through them on the real server).
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`people groups pass axe and fit ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const found: string[] = [];
      const check = async (where: string) => {
        const vs = await axe(page);
        if (vs.length > 0) found.push(describe(where, vs));
        expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
      };
      const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
      const now = new Date().toISOString();
      const card = (id: string, name: string) => ({ id, name, deleted: false, contact_name: null, in_contacts: true, they_added_you: true, photo: null, last_seen_at: now, online: false, read_ticks: true });
      const group = (over: Record<string, unknown> = {}) => ({
        id: "g1",
        kind: "group",
        name: "Yaba friends",
        description: "Saturday football and jollof",
        photo: null,
        members: 3,
        role: "admin",
        left: false,
        admins_only_post: false,
        admins_only_edit: true,
        approve_joins: true,
        can_post: true,
        can_edit: true,
        muted: false,
        archived: false,
        mentions: true,
        unread: 1,
        mentioned: true,
        last: "@Ada see you",
        last_mine: false,
        last_sender: "Bola Ade",
        last_at: now,
        joined_at: now,
        join_requests: 1,
        ...over,
      });
      const closedWords = "Taskiem's moderators closed this group. No one can send messages in it, and it will be deleted on Fri 18 Dec 2026 unless its admins appeal.";
      const closed = group({ id: "g2", name: "Market deals", role: "admin", can_post: false, can_edit: false, closed: { at: now, until: now, words: closedWords, can_appeal: true, action_id: "a1", reason: "Scams" } });
      await page.route("**/v1/me", (r) => r.fulfill(json({ ...PERSON, businesses: [], second_proof: undefined })));
      await page.route("**/v1/me/invitations", (r) => r.fulfill(json({ invitations: [] })));
      await page.route("**/v1/me/push/key", (r) => r.fulfill({ status: 404, contentType: "application/json", body: "{}" }));
      await page.route(/\/v1\/me\/conversations(\?|$)/, (r) =>
        r.fulfill(json({ chats: [{ kind: "group", id: "g1", name: "Yaba friends", last: "@Ada see you", last_mine: false, last_at: now, unread: 1, muted: false, archived: false, blocked: false, group: group() }], unread: 1, requests: 0, invites: 1 })),
      );
      await page.route("**/v1/me/groups/invites", (r) => r.fulfill(json({ invites: [{ group_id: "g9", name: "Church choir", photo: null, invited_by: "p2", by: "Bola Ade", at: now }] })));
      await page.route("**/v1/me/people/requests", (r) => r.fulfill(json({ requests: [] })));
      await page.route(/\/v1\/me\/people(\?|$)/, (r) => r.fulfill(json({ chats: [], unread: 0, requests: 0 })));
      await page.route(/\/v1\/me\/groups(\?|$)/, (r) => r.fulfill(json({ groups: [group()], unread: 1, invites: 1, more: false })));
      await page.route("**/v1/me/contacts", (r) => r.fulfill(json({ contacts: [{ ...card("p2", "Bola Ade"), source: "number", added_at: now }, { ...card("p3", "Chidi Eze"), source: "link", added_at: now }] })));
      await page.route(/\/v1\/me\/groups\/g1(\?|$)/, (r) => r.fulfill(json(group())));
      await page.route(/\/v1\/me\/groups\/g2(\?|$)/, (r) => r.fulfill(json(closed)));
      await page.route(/\/v1\/me\/groups\/g[12]\/read$/, (r) => r.fulfill({ status: 204 }));
      const members = {
        members: [
          { person_id: "me", name: "Ada Obi", role: "admin", joined_at: now, you: true },
          { person_id: "p2", name: "Bola Ade", role: "member", joined_at: now, you: false, person: card("p2", "Bola Ade") },
          { person_id: "p3", name: "Chidi Eze", role: "admin", joined_at: now, you: false, person: card("p3", "Chidi Eze") },
        ],
        invited: [{ person_id: "p4", name: "Dayo Ola", at: now }],
        waiting: [{ person_id: "p5", name: "Efe Ige", at: now }],
      };
      await page.route(/\/v1\/me\/groups\/g[12]\/members$/, (r) => r.fulfill(json(members)));
      await page.route(/\/v1\/me\/groups\/g[12]\/link$/, (r) => r.fulfill(json({ on: true, code: "ab23cd45ef", url: "http://localhost/g/ab23cd45ef", created_at: now, words: "Join my group", qr: "/brand/seal/mark-512.png" })));
      await page.route(/\/v1\/me\/groups\/g[12]\/media(\?|$)/, (r) => r.fulfill(json({ messages: [], more: false })));
      await page.route(/\/v1\/me\/groups\/g[12]\/joins$/, (r) => r.fulfill(json({ requests: [{ person_id: "p5", name: "Efe Ige", at: now }] })));
      const event = (id: string, type: string, body: string, taskiem = false) => ({ id, group_id: "g1", mine: false, sender_id: taskiem ? null : "p2", sender: taskiem ? "Taskiem" : "Bola Ade", kind: "event", body, event: { type, actor_id: "p2" }, created_at: now });
      await page.route(/\/v1\/me\/groups\/g1\/messages(\?|$)/, (r) =>
        r.fulfill(
          json({
            messages: [
              event("e1", "created", "Ada Obi made the group"),
              { id: "m1", group_id: "g1", mine: false, sender_id: "p2", sender: "Bola Ade", kind: "text", body: "Who is coming on Saturday?", created_at: now, reactions: [{ reaction: "👍", count: 2 }], my_reaction: "👍" },
              { id: "m2", group_id: "g1", mine: true, sender_id: "me", sender: "You", kind: "text", body: "@Chidi Eze bring the ball", mentions: ["p3"], reply_to: "m1", status: "read", created_at: now },
              { id: "m3", group_id: "g1", mine: false, sender_id: "p3", sender: "Chidi Eze", kind: "text", body: "@Ada see you", mentions: ["me"], mentions_me: true, created_at: now },
              { id: "m4", group_id: "g1", mine: false, sender_id: null, sender: "Deleted account", kind: "text", body: "Pay into my account 0123456789", warn_transfer: true, warn: true, created_at: now },
              { id: "m5", group_id: "g1", mine: true, sender_id: "me", sender: "You", kind: "card", body: "", card: { type: "split", title: "Your share", amount_kobo: 300_000, what: "the ball", pay_to: { bank: "GTBank", account_number: "0123456789", account_name: "Ada Obi" }, paid: true, paid_by_me: false }, status: "delivered", created_at: now },
              event("e2", "taskiem_link_off", "Taskiem's moderators turned this group's invite link off.", true),
            ],
            more: false,
          }),
        ),
      );
      await page.route(/\/v1\/me\/groups\/g2\/messages(\?|$)/, (r) =>
        r.fulfill(json({ messages: [{ ...event("e3", "taskiem_closed", closedWords, true), group_id: "g2" }], more: false })),
      );
      await page.route(/\/v1\/me\/groups\/g1\/messages\/m2\/info$/, (r) =>
        r.fulfill(json({ read_by: [{ person_id: "p3", name: "Chidi Eze", at: now }], delivered_to: [{ person_id: "p2", name: "Bola Ade", at: now }], waiting: 0 })),
      );
      await page.route(/\/v1\/me\/groups\/search\?/, (r) =>
        r.fulfill(json({ results: [{ group_id: "g1", group: group(), message: { id: "m1", group_id: "g1", mine: false, sender_id: "p2", sender: "Bola Ade", kind: "text", body: "Who is coming on Saturday?", created_at: now }, snippet: "Who is coming on Saturday?" }], more: false })),
      );
      await page.route(/\/v1\/me\/groups$/, (r) =>
        r.request().method() === "POST" ? r.fulfill(json({ group: group({ id: "g3", name: "Book club" }), added: [{ person_id: "p2", state: "added" }, { person_id: "p3", state: "invited" }] }, 201)) : r.fallback(),
      );
      await page.route("**/v1/group-links/ab23cd45ef", (r) => r.fulfill(json({ code: "ab23cd45ef", link: "http://localhost/g/ab23cd45ef", name: "Yaba friends", photo: null, members: 3, full: false })));

      await page.goto("/you");
      await expect(page.getByTestId("group-chat")).toBeVisible();
      await expect(page.getByTestId("people-requests-entry")).toContainText("Invites to groups");
      await check("/you with a group");
      await page.getByTestId("group-new-open").click();
      await page.getByTestId("group-new-name").fill("Book club");
      await page.getByTestId("group-pick-person").first().check();
      await page.getByTestId("group-pick-person").nth(1).check();
      await check("New group");
      await page.getByTestId("group-new-make").click();
      await expect(page.getByTestId("group-added-row")).toHaveCount(2);
      await expect(page.getByTestId("group-added")).toContainText("Invited: they'll see it in their Requests");
      await check("New group made");

      await page.goto("/you/people/requests");
      await expect(page.getByTestId("group-invite")).toBeVisible();
      await check("/you/people/requests with a group invite");

      await page.goto("/you/groups/g1");
      await expect(page.getByTestId("group-event").first()).toBeVisible();
      await expect(page.getByTestId("group-mentions-me")).toBeVisible();
      await expect(page.getByTestId("group-mention").first()).toBeVisible();
      await expect(page.getByTestId("group-reactions")).toBeVisible();
      await expect(page.getByTestId("group-transfer-warning")).toBeVisible();
      await expect(page.getByTestId("group-sender").first()).toBeVisible();
      await check("/you/groups/{id}");
      const mine = page.getByTestId("group-message").filter({ hasText: "bring the ball" });
      await mine.hover();
      await mine.locator(".bubble-chevron").click({ force: true });
      await page.getByTestId("chat-more").getByRole("button", { name: "Message info", exact: true }).click();
      await expect(page.getByTestId("group-read-by-list")).toContainText("Chidi Eze");
      await check("Read by");

      await page.goto("/you/groups/g1/info");
      await expect(page.getByTestId("group-member")).toHaveCount(3);
      await expect(page.getByTestId("group-invited")).toBeVisible();
      await expect(page.getByTestId("group-waiting")).toBeVisible();
      await expect(page.getByTestId("group-qr")).toBeVisible();
      await check("/you/groups/{id}/info");
      await page.getByTestId("group-add-open").click();
      await expect(page.getByTestId("group-add-sheet")).toBeVisible();
      await check("add people");
      await page.getByTestId("group-add-sheet").getByRole("button", { name: /Close/ }).first().click();
      await page.getByTestId("group-member-report").first().click();
      await expect(page.getByTestId("group-report-sheet")).toBeVisible();
      await check("report a member");

      await page.goto("/you/groups/g2");
      await expect(page.getByTestId("group-closed")).toBeVisible();
      await check("a closed group");
      await page.goto("/you/groups/g2/info");
      await expect(page.getByTestId("group-appeal-text")).toBeVisible();
      await check("a closed group's info with the appeal");

      await page.goto("/you/people/search?q=saturday&group=g1&name=Yaba%20friends");
      await expect(page.getByTestId("group-found")).toBeVisible();
      await check("search in a group");
      expect(found, found.join("\n\n")).toEqual([]);
    });

// A group's link signed out (people-chatting §9, A4): its name, photo and count, Join on Taskiem.
for (const scheme of ["light", "dark"] as const)
  test(`a group's link passes axe, ${scheme} theme`, async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 800 });
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await page.route("**/v1/group-links/ab23cd45ef", (r) =>
      r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ code: "ab23cd45ef", link: "http://localhost/g/ab23cd45ef", name: "Yaba friends", photo: null, members: 12, full: false }) }),
    );
    await page.goto("/g/ab23cd45ef");
    await expect(page.getByTestId("group-link-signin")).toBeVisible();
    await expect(page.getByTestId("group-link-count")).toContainText("12 members");
    const vs = await axe(page);
    expect(vs, describe("/g/{code}", vs)).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(360);
  });

// A person's link signed out (people-chatting §5.2): the name, Chat on Taskiem.
for (const scheme of ["light", "dark"] as const)
  test(`a person's link passes axe, ${scheme} theme`, async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 800 });
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await page.route("**/v1/person-links/ab23cd45ef", (r) =>
      r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ code: "ab23cd45ef", link: "http://localhost/p/ab23cd45ef", name: "Kemi Adeyemi", photo: null, app: "https://taskiem.test/app" }) }),
    );
    await page.goto("/p/ab23cd45ef");
    await expect(page.getByTestId("person-link-chat")).toBeVisible();
    const vs = await axe(page);
    expect(vs, describe("/p/{code}", vs)).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(360);
  });

// People chats' safety (people-chatting "As built: stage 5a gaps, safety and
// moderation"): the operator console's moderators' screens (reports of
// people and businesses, a report opened with its photo, held messages and
// verdicts, a person's standing, appeals, lawful requests), the person's
// Your standing with an appeal, and the published count of lawful requests
// signed out; the server's answers stood in for (ops-people.spec.ts goes
// through them on the real server).
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`people safety screens pass axe and fit ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const found: string[] = [];
      const check = async (where: string) => {
        const vs = await axe(page);
        if (vs.length > 0) found.push(describe(where, vs));
        expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
      };
      const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
      const now = new Date().toISOString();
      const later = new Date(Date.now() + 7 * 864e5).toISOString();
      const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "base64");
      const ops = (path: string, body: unknown) => page.route(`**/v1/ops${path}`, (r) => r.fulfill(json(body)));
      await ops("/me", { operator: { id: "o1", email: "mod@taskiem.test", name: "Mod", auth_method: "passkey", expires_at: later }, reviewer: false });
      await ops("/people/overview", {
        open: { people_reports: 2, customer_reports: 1, held: 3, appeals: 1, lawful_requests: 1 },
        storage: [
          { table: "people_files", rows: 1200, bytes: 734003200 },
          { table: "people_messages", rows: 98000, bytes: 52428800 },
        ],
        storage_words: "People chats take 750.0 MB on disk.",
      });
      const row = { id: "r1", reporter_id: "p1", detached: false, reported_id: "p2", reported_name: "Dayo Ola", reported_gone: false, reported_suspended: false, reason: "scam", note: "Wants money first", from_request: true, files: 1, sealed: true, status: "open", created_at: now, reviewed_by: null, reviewed_at: null, reports_against: 2 };
      await page.route(/\/v1\/ops\/people\/reports\?/, (r) => r.fulfill(json({ reports: [row, { ...row, id: "r2", status: "reviewed", reviewed_by: "operator:ana@taskiem.test", reviewed_at: now, detached: true }, { ...row, id: "r3", reported_id: null, reported_name: "", from_request: false, group_id: "g1" }] })));
      await page.route(/\/v1\/ops\/customer-reports\?/, (r) =>
        r.fulfill(json({ reports: [{ id: "cr1", reporter_id: "p1", detached: false, tenant_id: "t1", business: "Mama's Kitchen", reason: "abuse", note: "", messages: 4, status: "open", created_at: now, reviewed_by: null, reviewed_at: null }] })),
      );
      await page.route(/\/v1\/ops\/people\/reports\/r1\?why=/, (r) =>
        r.fulfill(
          json({
            report: { id: "r1", reporter_id: "p1", reported_id: "p2", reported_name: "Dayo Ola", reason: "scam", note: "Wants money first", from_request: true, status: "open", created_at: now, reviewed_by: null, reviewed_at: null, review_note: null },
            messages: [
              { id: "m2", from_reporter: false, kind: "media", body: "Only 5000 naira", attachment: { kind: "photo", type: "image/png", size: 68, name: "dress.png" }, file: true, at: now },
              { id: "m1", from_reporter: false, kind: "text", body: "Hello, send the code you got", at: now },
            ],
            files: [{ message_id: "m2", type: "image/png", size: 68 }],
            sealed: true,
          }),
        ),
      );
      await page.route(/\/v1\/ops\/people\/reports\/r1\/files\/m2\?why=/, (r) => r.fulfill({ status: 200, contentType: "image/png", body: png }));
      const held = { id: "h1", sender_id: "p2", sender_name: "Dayo Ola", recipient_id: "p1", recipient_name: "Ada Obi", kind: "text", reasons: ["code", "prize"], has_file: false, state: "held", created_at: now, decided_by: null, decided_at: null };
      await page.route(/\/v1\/ops\/people\/held\?/, (r) => r.fulfill(json({ held: [held] })));
      await page.route(/\/v1\/ops\/people\/held\/h1\?why=/, (r) =>
        r.fulfill(json({ held: { ...held, file_type: null, file_size: null, decision_note: null, released_message: null }, message: { body: "You have won! Send me the code" } })),
      );
      await ops("/people/verdicts", { verdicts: [{ from_person: "p2", from_name: "Dayo Ola", to_person: "p1", to_name: "Ada Obi", verdict: "hold", reasons: ["code"], at: now }] });
      await ops("/people/persons/p2", {
        person: { id: "p2", name: "Dayo Ola", active: true, deleted: false, suspended: false, created_at: now, reports_against: 2, held: 3, verdicts_week: 4, device_accounts: 3, requests_waiting: 1, requests_accepted: 0, requests_refused: 5 },
        actions: [{ id: "a1", action: "limit", reason: "Many requests deleted", operator: "operator:ana@taskiem.test", report_id: null, until: later, lifted_at: null, lifted_by: null, lift_reason: null, closed_at: null, close_note: null, created_at: now, appeal_id: null, appeal_state: null, appeal_text: null, appeal_at: null }],
      });
      await page.route(/\/v1\/ops\/people\/appeals\?/, (r) =>
        r.fulfill(json({ appeals: [{ id: "ap1", action_id: "a1", person_id: "p2", name: "Dayo Ola", action: "limit", action_reason: "Many requests deleted", operator: "operator:ana@taskiem.test", until: later, text: "I sell clothes and message buyers who asked me to.", state: "open", created_at: now, decided_by: null, decided_at: null, decision_note: null }] })),
      );
      const lawful = { id: "l1", authority: "Nigeria Police Force", reference: "NPF/CID/2026/114", legal_basis: "Cybercrimes Act 2015 s.38, court order of 2 Nov 2026", person_id: "p2", person_name: "Dayo Ola", from: now, to: later, with_person_id: null, files: false, scope_note: "", received_at: now, recorded_by: "operator:ana@taskiem.test", recorded_at: now, approved_by: null, approved_at: null, state: "recorded", answered_by: null, answered_at: null, messages_given: null, refused_reason: null, told_at: null, not_told_reason: null };
      await ops("/lawful-requests", { requests: [lawful] });
      await ops("/lawful-requests/l1", { request: lawful });

      for (const [path, testid] of [
        ["/ops/people", "ops-people-storage"],
        ["/ops/people/reports", "ops-people-report"],
        ["/ops/people/reports?kind=business", "ops-customer-report"],
        ["/ops/people/held", "ops-held"],
        ["/ops/people/persons/p2", "ops-person-signals"],
        ["/ops/people/appeals", "ops-appeal"],
        ["/ops/lawful", "ops-lawful"],
        ["/ops/lawful/l1", "ops-lawful-detail"],
      ] as const) {
        await page.goto(path);
        await expect(page.getByTestId(testid).first()).toBeVisible();
        await check(path);
      }
      await page.goto("/ops/people/persons/p2");
      await page.getByTestId("ops-lift-open").click();
      await expect(page.getByLabel(/Why you are lifting/)).toBeVisible();
      await check("lifting a decision");
      await page.goto("/ops/lawful");
      await page.getByRole("button", { name: "Record a request" }).click();
      await expect(page.getByTestId("ops-lawful-record")).toBeVisible();
      await check("record a lawful request");
      await page.goto("/ops/people/reports/r1");
      await expect(page.getByTestId("ops-why")).toBeVisible();
      await check("why are you opening this report");
      await page.getByLabel(/Your reason/).fill("Handling the queue");
      await page.getByRole("button", { name: "Open report" }).click();
      await expect(page.getByTestId("ops-report-message")).toHaveCount(2);
      await page.getByRole("button", { name: /Show the photo/ }).click();
      await expect(page.getByRole("img", { name: /Photo sent/ })).toBeVisible();
      await check("/ops/people/reports/{id}");
      await page.goto("/ops/people/held/h1");
      await page.getByLabel(/Your reason/).fill("Handling the queue");
      await page.getByRole("button", { name: "Open message" }).click();
      await expect(page.getByTestId("ops-held-message")).toContainText("You have won");
      await check("/ops/people/held/{id}");
      // A group's page (5b): opened saying why; members, reports, decisions, and its actions.
      await page.route(/\/v1\/ops\/people\/groups\/g1\?why=/, (r) =>
        r.fulfill(
          json({
            group: { id: "g1", created_at: now, created_by: "p2", creator_name: "Dayo Ola", members: 41, admins: 2, link_on: false, last_message_at: now, closed_at: now, close_until: later, link_stopped_at: now, reports: 3, held: 1 },
            gone: false,
            members: [
              { person_id: "p2", name: "Dayo Ola", role: "admin", how: "created", joined_at: now, left_at: null, left_how: null, deleted: false, suspended: false },
              { person_id: "p1", name: "Ada Obi", role: "member", how: "link", joined_at: now, left_at: now, left_how: "left", deleted: false, suspended: false },
            ],
            reports: [{ id: "r3", reporter_id: "p1", reported_id: null, reported_name: "", reason: "scam", status: "open", created_at: now }],
            actions: [{ id: "ga1", action: "group_close", reason: "Selling fake prizes", operator: "operator:ana@taskiem.test", report_id: "r3", until: later, lifted_at: null, lifted_by: null, lift_reason: null, closed_at: null, close_note: null, created_at: now, appeal_id: "ap2", appeal_state: "open", appeal_text: "We are real", appeal_at: now }],
          }),
        ),
      );
      await page.goto("/ops/people/groups/g1");
      await page.getByLabel(/Your reason/).fill("Handling the queue");
      await page.getByRole("button", { name: "Open group" }).click();
      await expect(page.getByTestId("ops-group")).toContainText("Closed");
      await expect(page.getByTestId("ops-group-member")).toHaveCount(2);
      await check("/ops/people/groups/{id}");

      // The person's side: the notice on You and Your standing with an appeal.
      await page.route("**/v1/me", (r) => r.fulfill(json({ ...PERSON, businesses: [], second_proof: undefined })));
      await page.route("**/v1/me/invitations", (r) => r.fulfill(json({ invitations: [] })));
      await page.route(/\/v1\/me\/conversations(\?|$)/, (r) => r.fulfill(json({ chats: [], unread: 0, requests: 0 })));
      // Seen once Your standing is opened, as the server keeps it.
      let unseen = 1;
      await page.route("**/v1/me/standing/seen", (r) => ((unseen = 0), r.fulfill({ status: 204 })));
      await page.route("**/v1/me/standing", (r) =>
        r.fulfill(
          json({
            state: "limited",
            words: "You can send only a few message requests to people you don't know, until Wed 25 Nov 2026.",
            until: later,
            unseen,
            appeal_words: "If you think a decision is wrong, tell us why. A different moderator reads your appeal, and we tell you what they decide.",
            actions: [
              { id: "a1", action: "limit", reason: "Many of your requests were deleted.", at: now, until: later, in_force: true, lifted_at: null, seen: false, words: "You can send only a few message requests to people you don't know, until Wed 25 Nov 2026.", can_appeal: true, appeal: null },
              { id: "a0", action: "warn", reason: "Asking strangers for codes.", at: now, until: null, in_force: false, lifted_at: null, seen: true, words: "", can_appeal: false, appeal: { id: "ap0", text: "It was a joke", state: "upheld", at: now, decided_at: now, note: "Codes are never asked for." } },
            ],
          }),
        ),
      );
      await page.goto("/you");
      await expect(page.getByTestId("standing-notice")).toBeVisible();
      await check("/you with a standing notice");
      // Opened from the notice (in the page, not a new load): once read, the
      // notice is gone on You.
      await page.getByTestId("standing-notice").getByRole("link").click();
      await expect(page).toHaveURL(/\/you\/settings\/standing$/);
      await expect(page.getByTestId("standing-now")).toContainText("Your message requests are limited");
      await expect(page.getByTestId("standing-notice")).toHaveCount(0);
      await page.getByTestId("standing-appeal-open").click();
      await check("/you/settings/standing");
      await expect.poll(() => unseen).toBe(0);
      await page.getByRole("navigation", { name: "You" }).getByRole("link", { name: "Chats" }).click();
      await expect(page).toHaveURL(/\/you$/);
      await expect(page.getByTestId("standing-notice")).toHaveCount(0);
      expect(found, found.join("\n\n")).toEqual([]);
    });

// The published count of lawful requests, signed out (people-chatting §8.5).
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`the transparency page passes axe and fits ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      await page.route("**/v1/transparency/lawful-requests", (r) =>
        r.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ periods: [{ year: 2026, half: 1, label: "January to June 2026", received: 2, answered: 1, refused: 1, pending: 0 }], words: "Taskiem answers only proper legal process." }),
        }),
      );
      await page.goto("/transparency");
      await expect(page.getByTestId("transparency-period").filter({ hasText: "January to June 2026" })).toContainText("2 received, 1 answered, 1 refused.");
      const vs = await axe(page);
      expect(vs, describe("/transparency", vs)).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    });

// The privacy notice and how to delete an account, signed out (Google Play asks both).
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`the privacy and account deletion pages pass axe and fit ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      await page.route("**/v1/app/config", (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ support_email: "help@taskiem.test" }) }));
      for (const [path, id] of [["/privacy", "privacy"], ["/delete-account", "delete-account"]] as const) {
        await page.goto(path);
        await expect(page.getByTestId(id)).toBeVisible();
        await expect(page.getByTestId("legal-email").first()).toHaveAttribute("href", "mailto:help@taskiem.test");
        const vs = await axe(page);
        expect(vs, describe(path, vs)).toEqual([]);
        expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
      }
      await expect(page.getByTestId("delete-steps-app")).toContainText("Delete your account");
    });

// Pay on a rider (paying people, phase 2): the riders' pay page with a Pay
// on each rider whose bank account is kept, and the Pay sheet it opens
// (what for: a delivery), signed in as a new owner. The riders and pay
// APIs are stood in for: the e2e server sends no rider messages.
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`Pay on a rider passes axe and fits ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const stamp = String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
      const r = await page.request.post("/v1/signup", {
        headers: { "X-Taskiem-Request": "1" },
        data: { tenant: `Rider Pay ${stamp}`, email: `${stamp}-${Math.random().toString(36).slice(2, 8)}@riderpay.test`, name: "Ada Owner", password: "correct horse battery" },
      });
      expect(r.status(), await r.text()).toBe(201);
      const fulfil = (body: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
      const rider = "0192e2e0-0000-7000-8000-00000000e001";
      await page.route("**/v1/riders/owed", (q) => q.fulfill(fulfil({ owed: [] })));
      await page.route("**/v1/riders/pay", (q) =>
        q.fulfill(fulfil({ workflow_id: null, payouts: [], lines: [{ rider_id: rider, rider: "Musa Bello", deliveries: 3, fees_kobo: 450_000, owed_kobo: 0, net_kobo: 450_000, bank: "GTBank ••6789" }] })),
      );
      let body: unknown;
      await page.route("**/v1/payments", (q) => {
        body = q.request().postDataJSON();
        return q.fulfill(
          fulfil({
            payment: {
              id: "0192e2e0-0000-7000-8000-00000000e002", saved_id: null, personal: false, payee: "Musa Bello", bank_name: "Guaranty Trust Bank", account: "••6789",
              account_name: "MUSA BELLO", amount_kobo: 450_000, purpose: "delivery", purpose_note: "", note: "", first_payment: false, changed_account: false, needs_ok: "",
              status: "prepared", reason: null, fee_kobo: null, started_from: "tools", prepared_by: "user:ada", confirmed_by: null,
              created_at: new Date().toISOString(), confirmed_at: null, finished_at: null, again: false,
            },
          }),
        );
      });
      await page.goto("/storefront/riders/pay");
      const row = page.getByTestId("rider-due").filter({ hasText: "Musa Bello" });
      await expect(row).toContainText("GTBank ••6789");
      let vs = await axe(page);
      expect(vs, describe("/storefront/riders/pay with Pay", vs)).toEqual([]);
      await row.getByRole("button", { name: "Pay Musa Bello" }).click();
      const sheet = page.getByTestId("pay-sheet");
      await expect(sheet.getByTestId("pay-rider-lead")).toContainText("GTBank ••6789");
      await expect(sheet.getByLabel("How much? in naira")).toHaveValue("4,500");
      await expect(sheet.getByRole("group", { name: "What is it for?" }).getByRole("button", { name: "a delivery" })).toHaveAttribute("aria-pressed", "true");
      vs = await axe(page);
      expect(vs, describe("the Pay sheet for a rider", vs)).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
      await sheet.getByTestId("pay-prepare").click();
      await expect(sheet.getByTestId("pay-name")).toHaveText("MUSA BELLO");
      expect(body).toEqual({ from: "tools", rider_id: rider, amount_kobo: 450_000, purpose: "delivery" });
    });

// Pay several people (paying people, phase 3): one list's page, to
// confirm with a row held out and a changed account, and once sent with
// rows paid, waiting and failed. The batch API is stood in for: the e2e
// server has no payments account to pay from.
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`A list of people to pay passes axe and fits ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const stamp = String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
      const r = await page.request.post("/v1/signup", {
        headers: { "X-Taskiem-Request": "1" },
        data: { tenant: `Pay List ${stamp}`, email: `${stamp}-${Math.random().toString(36).slice(2, 8)}@paylist.test`, name: "Ada Owner", password: "correct horse battery" },
      });
      expect(r.status(), await r.text()).toBe(201);
      const fulfil = (body: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
      const now = new Date().toISOString();
      const pay = (id: string, payee: string, status: string, over: Record<string, unknown> = {}) => ({
        id, saved_id: null, personal: false, payee, bank_name: "GTBank", account: "••6789", account_name: payee.toUpperCase(), amount_kobo: 450_000, purpose: "delivery",
        purpose_note: "", note: "", first_payment: false, changed_account: false, needs_ok: "", status, reason: null, fee_kobo: null, run_id: null, started_from: "tools",
        prepared_by: "user:ada", confirmed_by: null, created_at: now, confirmed_at: null, finished_at: null, again: false, ...over,
      });
      const list = (status: string, payments: ReturnType<typeof pay>[]) => ({
        id: "0192e2e0-0000-7000-8000-00000000f001", title: "Riders, week 45", kind: "riders", status, created_by: "user:ada", created_at: now, confirmed_at: status === "sent" ? now : null,
        payments, total_kobo: 450_000 * payments.length, count: payments.length, paid_kobo: 450_000, waiting: 1, failed: 1,
      });
      let sent = false;
      await page.route(
        (u) => u.pathname.startsWith("/v1/payment-batches/"),
        (q) =>
          q.fulfill(
            fulfil({
              batch: sent
                ? list("sent", [pay("p1", "Musa Bello", "paid"), pay("p2", "Kemi Ade", "waiting_ok"), pay("p3", "Tunde Bello", "failed", { reason: "account closed" })])
                : list("prepared", [pay("p1", "Musa Bello", "prepared", { first_payment: true }), pay("p2", "Kemi Ade", "prepared", { changed_account: true })]),
            }),
          ),
      );
      await page.goto("/payments/batches/0192e2e0-0000-7000-8000-00000000f001");
      await expect(page.getByTestId("batch-status")).toHaveText("To confirm");
      await expect(page.getByTestId("batch-warn-changed")).toBeVisible();
      let vs = await axe(page);
      expect(vs, describe("a list to confirm", vs)).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
      sent = true;
      await page.reload();
      await expect(page.getByTestId("batch-status")).toHaveText("Waiting for an OK");
      await expect(page.getByTestId("batch-approve")).toBeVisible();
      vs = await axe(page);
      expect(vs, describe("a list sent", vs)).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    });

// Your list to pay, collected from messages (Pay several people, From
// messages): a row from a link chat, one from a photo with no amount yet,
// the check held up until it has one. The list's API is stood in for.
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`Your list to pay passes axe and fits ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      // The theme the app shows is the one kept in this browser.
      await page.addInitScript((t) => {
        try {
          localStorage.setItem("taskiem-theme", t);
        } catch {
          // the default theme, then
        }
      }, scheme);
      const stamp = String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
      const r = await page.request.post("/v1/signup", {
        headers: { "X-Taskiem-Request": "1" },
        data: { tenant: `Collect ${stamp}`, email: `${stamp}-${Math.random().toString(36).slice(2, 8)}@collect.test`, name: "Ada Owner", password: "correct horse battery" },
      });
      expect(r.status(), await r.text()).toBe(201);
      const now = new Date().toISOString();
      const row = (id: string, name: string, amount: number | null, source: Record<string, string>) => ({
        id, saved_id: null, payee: "", title: "Account ••6789", name, bank_name: "GTBank", last4: "6789", amount_kobo: amount, purpose: "", note: "", source, added_at: now,
      });
      const rows = [
        row("0192e2e0-0000-7000-8000-00000000c001", "Chidi Okeke", 4_500_000, { kind: "link", chat: "L1", message: "m1" }),
        row("0192e2e0-0000-7000-8000-00000000c002", "Tunde", null, { kind: "photo", chat: "customer:C1", message: "m2" }),
      ];
      await page.route(
        (u) => u.pathname === "/v1/pay-collection",
        (q) => q.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ rows, count: 2, total_kobo: 4_500_000, no_amount: 1, in_batch: null }) }),
      );
      await page.goto("/payments/several/list");
      await expect(page.getByTestId("collect-row")).toHaveCount(2);
      await page.getByTestId("collect-check").click();
      await expect(page.getByTestId("collect-blocked")).toBeVisible();
      const vs = await axe(page);
      expect(vs, describe("your list to pay", vs)).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    });

// Where we pay a staff member (outside payroll): their page with the
// account linked, Pay and the owner's Change and Unlink, choosing one of
// the saved accounts, and Add staff in Pay several people with someone
// not payable. The staff and saved APIs are stood in for.
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`Where we pay a staff member passes axe and fits ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const stamp = String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
      const r = await page.request.post("/v1/signup", {
        headers: { "X-Taskiem-Request": "1" },
        data: { tenant: `Staff Pay ${stamp}`, email: `${stamp}-${Math.random().toString(36).slice(2, 8)}@staffpay.test`, name: "Ada Owner", password: "correct horse battery" },
      });
      expect(r.status(), await r.text()).toBe(201);
      const fulfil = (body: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
      const musa = "0192e2e0-0000-7000-8000-00000000a201";
      const acct = { saved_id: "0192e2e0-0000-7000-8000-00000000b201", name: "musa", title: "Musa Bello", bank_name: "Access Bank", last4: "6789", account_name: "MUSA BELLO", linked_at: new Date().toISOString() };
      const person = (id: string, name: string, linked: typeof acct | null) => ({ user_id: id, name, phone: "•••4567", roles: ["cashier"], chat: true, profile: { title: "", note: "", rota: [] }, pay_account: linked });
      await page.route(
        (u) => u.pathname.startsWith("/v1/staff"),
        (q) => {
          const path = new URL(q.request().url()).pathname;
          if (path === "/v1/staff") return q.fulfill(fulfil({ people: [person(musa, "Musa Bello", acct), person("0192e2e0-0000-7000-8000-00000000a202", "Kemi Ade", null)], invites: [] }));
          if (path.endsWith("/figures"))
            return q.fulfill(
              fulfil({ period: "week", from: "2026-11-16", to: "2026-11-22", minutes: 0, shifts: 0, late_days: [], missed_days: [], sales: { count: 0, total_kobo: 0, by_how: {} }, handovers: [], orders: 0, tasks: { done: 0, done_late: 0, cant: 0, open: 0, late: 0 } }),
            );
          if (path === `/v1/staff/${musa}`) return q.fulfill(fulfil(person(musa, "Musa Bello", acct)));
          return q.fulfill(fulfil(person("0192e2e0-0000-7000-8000-00000000a202", "Kemi Ade", null)));
        },
      );
      await page.route(
        (u) => u.pathname === "/v1/saved" && u.searchParams.get("kind") === "bank_account",
        (q) =>
          q.fulfill(
            fulfil({
              limit: 100,
              saved: [{ id: acct.saved_id, owner: "business", name: "musa", title: "Musa Bello", kind: "bank_account", hint: "Access Bank ••6789", details: {}, check: { state: "checked", account_name: "MUSA BELLO", via: null, at: null, problem: null }, to_change: false, created_at: "", updated_at: "" }],
            }),
          ),
      );
      await page.goto(`/staff/${musa}`);
      await expect(page.getByTestId("person-pay-account")).toHaveText("@musa · Access Bank ••6789 · MUSA BELLO");
      await expect(page.getByTestId("staff-pay")).toBeVisible();
      let vs = await axe(page);
      expect(vs, describe("a staff member's page with where we pay them", vs)).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
      await page.getByRole("button", { name: "Change where Musa Bello is paid" }).click();
      await expect(page.getByRole("dialog", { name: "Where we pay Musa Bello" }).getByRole("radio", { name: /MUSA BELLO/ })).toBeChecked();
      vs = await axe(page);
      expect(vs, describe("choosing where we pay them", vs)).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
      await page.goto("/payments/several");
      await page.getByTestId("add-staff").click();
      const picker = page.getByRole("dialog", { name: "Add staff" });
      await expect(picker).toContainText("No account linked: link one on their page");
      vs = await axe(page);
      expect(vs, describe("Add staff in Pay several people", vs)).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    });

// Payroll, read from Payrolla (P4a): the runs, one run waiting for
// approval with someone not paid, and Staff in Payrolla with a likely
// match. Payrolla's answers are stood in for.
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`Payroll passes axe and fits ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const stamp = String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
      const r = await page.request.post("/v1/signup", {
        headers: { "X-Taskiem-Request": "1" },
        data: { tenant: `Payroll ${stamp}`, email: `${stamp}-${Math.random().toString(36).slice(2, 8)}@payroll.test`, name: "Ada Owner", password: "correct horse battery" },
      });
      expect(r.status(), await r.text()).toBe(201);
      const fulfil = (body: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
      const id = "0192e2e0-0000-7000-8000-00000000d201";
      const musa = "0192e2e0-0000-7000-8000-00000000a301";
      const run = { run_id: id, period_start: "2026-11-01", period_end: "2026-11-30", pay_date: "2026-11-30", run_type: "REGULAR", status: "COMPUTED", employee_count: 2, total_gross_kobo: 36_000_000, total_net_kobo: 30_000_000, awaiting_approval: true };
      await page.route(
        (u) => u.pathname.startsWith("/v1/payroll"),
        (q) => {
          const p = new URL(q.request().url()).pathname;
          if (p === "/v1/payroll") return q.fulfill(fulfil({ connected: true, connection: "payrolla", test: true, company: { name: "Mama's Kitchen Ltd", pay_frequency: "MONTHLY" }, runs: [run, { ...run, run_id: "0192e2e0-0000-7000-8000-00000000d202", period_start: "2026-10-01", period_end: "2026-10-31", status: "COMPLETED" }], next_cursor: "c2" }));
          if (p === "/v1/payroll/staff")
            return q.fulfill(
              fulfil({
                connection: "payrolla",
                staff: [
                  { user_id: musa, name: "Musa Bello", payrolla: null, suggested: { employee_id: "e1", name: "Musa Bello", by: "phone" } },
                  { user_id: "0192e2e0-0000-7000-8000-00000000a302", name: "Kemi Ade", payrolla: { employee_id: "e2", name: "Kemi Ade", linked_by_name: "Ada Owner", linked_at: new Date().toISOString() }, suggested: null },
                ],
                employees: [{ id: "e1", name: "Musa Bello", status: "ACTIVE", linked_to: null }, { id: "e2", name: "Kemi Ade", status: "ACTIVE", linked_to: "0192e2e0-0000-7000-8000-00000000a302" }],
              }),
            );
          return q.fulfill(
            fulfil({
              connection: "payrolla",
              run: { summary: run, created_at: null, updated_at: null, approved_at: null, locked_at: null },
              lines: [
                { payslip_id: "p1", employee_id: "e1", name: "Musa Bello", net_kobo: 15_000_000, status: "CONFIRMED", state: "paid", staff: { user_id: musa, name: "Musa Bello" } },
                { payslip_id: "p2", employee_id: "e2", name: "Kemi Ade", net_kobo: 15_000_000, status: "REVERSED", state: "returned", reason: "The bank sent it back", staff: null },
              ],
              counts: { paid: 1, waiting: 0, failed: 0, returned: 1 },
              approval_url: "https://app.payrolla.example/approve",
            }),
          );
        },
      );
      for (const [path, ready] of [
        ["/payroll", "payroll-hero"],
        [`/payroll/runs/${id}`, "payroll-approve-open"],
        ["/payroll/staff", "payroll-staff-confirm"],
      ] as const) {
        await page.goto(path);
        await expect(page.getByTestId(ready).first()).toBeVisible();
        const vs = await axe(page);
        expect(vs, describe(path, vs)).toEqual([]);
        expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
      }
    });

// Save as… beside a message's Pay this account (paying people, phase 2):
// the chip line, the sheet with the likely banks, and the sheet once saved
// with the bank's name, signed in as a new owner. The customer chat, the
// message's reading, the banks and the save are stood in for.
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`Save as… from a message passes axe and fits ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      const stamp = String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
      const r = await page.request.post("/v1/signup", {
        headers: { "X-Taskiem-Request": "1" },
        data: { tenant: `Save As ${stamp}`, email: `${stamp}-${Math.random().toString(36).slice(2, 8)}@saveas.test`, name: "Ada Owner", password: "correct horse battery" },
      });
      expect(r.status(), await r.text()).toBe(201);
      const fulfil = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
      const now = new Date().toISOString();
      const text = "Acct: 0123456789, send 45k. Chidi Okafor";
      const chat = { id: "0192e2e0-0000-7000-8000-00000000e101", name: "Tolu Bello", phone: "+234 803 ••• 0099", last: text, last_mine: false, last_at: now, unread: 0, window_open: true, window_until: null, stopped: false, archived: false, line: "whatsapp" };
      await page.route(
        (u) => u.pathname.startsWith("/v1/customer-chats"),
        (q) => {
          const p = new URL(q.request().url()).pathname;
          if (p === "/v1/customer-chats") return q.fulfill(fulfil({ chats: [chat] }));
          if (p === "/v1/customer-chats/unread") return q.fulfill(fulfil({ chats: 0, messages: 0 }));
          if (p === `/v1/customer-chats/${chat.id}`) return q.fulfill(fulfil(chat));
          if (p.endsWith("/messages")) return q.fulfill(fulfil({ messages: [{ id: "m1", mine: false, kind: "text", body: text, read_at: now, created_at: now }], more: false }));
          if (p.endsWith("/orders")) return q.fulfill(fulfil({ orders: [] }));
          return q.fulfill(fulfil({}));
        },
      );
      const banks = [
        { code: "058", name: "Guaranty Trust Bank", likely: true },
        { code: "044", name: "Access Bank", likely: true },
      ];
      await page.route("**/v1/payments/read", (q) => q.fulfill(fulfil({ found: true, account_number: "0123456789", amount_kobo: 4_500_000, name: "Chidi Okafor", likely: banks })));
      await page.route("**/v1/payments/banks?*", (q) => q.fulfill(fulfil({ from: "paystack", banks })));
      await page.route(
        (u) => u.pathname === "/v1/saved",
        (q) => {
          if (q.request().method() !== "POST") return q.fallback();
          return q.fulfill(
            fulfil(
              {
                saved: {
                  id: "0192e2e0-0000-7000-8000-00000000e102", owner: "business", name: "chidi-okafor", title: "Chidi Okafor", kind: "bank_account", hint: "GTBank ••6789",
                  details: { bank_name: "Guaranty Trust Bank", account_number: "••6789" }, check: { state: "checked", account_name: "CHIDI OKEKE", via: "paystack", at: now, problem: null },
                  to_change: false, created_at: now, updated_at: now,
                },
              },
              201,
            ),
          );
        },
      );
      await page.goto(`/chat?with=c-${chat.id}`);
      const message = page.getByTestId("customer-pane").getByTestId("customer-message").filter({ hasText: "send 45k" });
      await expect(message.getByTestId("pay-chip-save")).toBeVisible();
      let vs = await axe(page);
      expect(vs, describe("a message with Save as…", vs)).toEqual([]);
      await message.getByTestId("pay-chip-save").click();
      const sheet = page.getByTestId("save-as-sheet");
      await expect(sheet.getByRole("group", { name: "Banks this number could be with" })).toBeVisible();
      vs = await axe(page);
      expect(vs, describe("the Save as sheet", vs)).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
      await sheet.getByRole("button", { name: "Guaranty Trust Bank" }).click();
      await sheet.getByTestId("save-as-save").click();
      await expect(sheet.getByTestId("save-as-bank-name")).toContainText("CHIDI OKEKE");
      vs = await axe(page);
      expect(vs, describe("the Save as sheet, saved", vs)).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    });


/** A tool of the business (made tools M1), published once whichever test runs first. */
async function aTool(page: Page): Promise<string> {
  const h = { headers: { "X-Taskiem-Request": "1" } };
  const tools = (await (await page.request.get("/v1/tools")).json()) as { tools: { command: string }[] };
  if (tools.tools.some((t) => t.command === "a11y-note")) return "a11y-note";
  const definition = {
    schema: "wd/v1",
    id: "wf_a11ynote",
    version: 1,
    name: "Accessible note",
    trigger: { type: "manual" },
    inputs: { schema: { type: "object", properties: { what: { type: "string", title: "What for" }, amount: { type: "integer", title: "Amount" } } } },
    steps: [{ id: "note", type: "transform", config: { output: { said: "=trigger.body.what", amount: "=trigger.body.amount" } } }],
    outputs: [
      { name: "what", type: "string", title: "What for", description: "What it was for", value: "=steps.note.output.said" },
      { name: "amount_kobo", type: "integer", title: "Amount", description: "How much", value: "=steps.note.output.amount" },
    ],
    tool: {
      name: "Accessible note",
      command: "a11y-note",
      about: "Note what money went on",
      answer: { type: "card", title: "Noted", ref: "=outputs.what", state: { text: "Kept", tone: "good" }, lines: [{ label: "What for", value: "=outputs.what" }], amount: "=outputs.amount_kobo" },
    },
  };
  const made = (await (await page.request.post("/v1/workflows", { ...h, data: { name: "Accessible note", definition } })).json()) as { id: string };
  const pub = await page.request.post(`/v1/workflows/${made.id}/versions/1/publish`, h);
  // Another test may have published it a moment before.
  const now = (await (await page.request.get("/v1/tools")).json()) as { tools: { command: string }[] };
  expect(pub.ok() || now.tools.some((t) => t.command === "a11y-note"), await pub.text()).toBeTruthy();
  return "a11y-note";
}

// Your tools (made tools M1): a tool's page with its sheet, the tools
// panel with Your tools, and a card answer, at a phone's widths.
for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`Your tools pass axe and fit ${width} px, ${scheme} theme`, async ({ page }) => {
      test.setTimeout(120_000);
      await page.setViewportSize({ width, height: 800 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      await signIn(page);
      const command = await aTool(page);
      const found: string[] = [];
      const check = async (where: string) => {
        const vs = await axe(page);
        if (vs.length > 0) found.push(describe(where, vs));
        expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
      };
      await page.goto(`/tools/${command}`);
      const sheet = page.getByTestId("made-tool-sheet");
      await expect(sheet.getByLabel("What for")).toBeVisible();
      await check("/tools/:command (its sheet)");
      await sheet.getByRole("button", { name: "Close" }).click();
      await expect(page.getByTestId("tool-page")).toBeVisible();
      await check("/tools/:command");
      // The chat: "/" and Your tools, then its card answer.
      await page.goto("/chat?with=taskiem");
      await page.getByLabel("Message to Taskiem").fill("/a11y");
      await expect(page.getByTestId("your-tools")).toBeVisible();
      await check("/chat (Your tools)");
      await page.getByTestId("tools-panel").getByRole("button", { name: /Accessible note/ }).click();
      await sheet.getByLabel("What for").fill("Diesel");
      await sheet.getByLabel("Amount").fill("450000");
      await sheet.getByRole("button", { name: "Go" }).click();
      const answer = sheet.getByTestId("tool-answer");
      await expect(answer).toHaveAttribute("data-status", "completed", { timeout: 30_000 });
      await expect(answer).toContainText("₦4,500");
      await check("/chat (a tool's card answer)");
      expect(found, found.join("\n\n")).toEqual([]);
    });
