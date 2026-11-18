import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";
import { PUBLIC, familyLink, familySchool } from "./familyschool";

// Automated accessibility checks (axe: WCAG 2.1 A/AA and best practices)
// on the console's pages, in the light and the dark theme, plus keyboard
// checks for the dialogs and the narrow-screen menu. axe is injected into
// the page, which the console's Content Security Policy would refuse.
test.use({ bypassCSP: true });

const PAGES = [
  "/home",
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
  "/money/fees",
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
      await page.route("**/v1/me/addresses", (r) => r.fulfill(json({ addresses: [] })));
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
      // A server with no push keys: "We'll text you when they answer."
      await page.route("**/v1/me/push/key", (r) => r.fulfill(json({ error: "not found" }, 404)));
      await page.goto("/you/chats/c1");
      await expect(page.getByTestId("offers-question")).toBeVisible();
      await expect(page.getByTestId("transfer-warning")).toBeVisible();
      await expect(page.getByTestId("tell-me-line")).toHaveText("We'll text you when they answer.");
      await expect(page.getByTestId("person-trust")).toHaveText("Phone checked · Business checked · Payments through Taskiem · Trading 1 year");
      await expect(page.getByTestId("person-whatsapp-here")).toBeVisible();
      await expect(page.getByTestId("person-on-whatsapp")).toHaveCount(2);
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
      await page.route("**/v1/me/invitations", (r) => r.fulfill(json({ invitations: [] })));
      const earlier = { ...order, reference: "MK-E", state: "completed", summary: "", due_kobo: 0, pay: false, earlier: true };
      await page.route(/\/v1\/me\/orders(\?|$)/, (r) => r.fulfill(json({ orders: [order, { ...order, reference: "MK-0", state: "completed", due_kobo: 0, pay: false }, earlier], next: "x", waiting_count: 1 })));
      await page.route(`**/v1/me/orders/${b1}/MK-E`, (r) => r.fulfill(json({ ...earlier, lines: [], paid_kobo: 0 })));
      await page.route(`**/v1/me/orders/${b1}/MK-1`, (r) =>
        r.fulfill(json({ ...order, lines: [{ name: "Jollof rice", quantity: 2, price_kobo: 450_000, line_kobo: 900_000 }], fulfilment: "delivery", zone: "Yaba", paid_kobo: 0, address: { address: "12 Allen Avenue, Ikeja", landmark: "By the blue gate" }, delivery: { status: "accepted", line: "A rider is going to collect it" } })),
      );
      // You's settings read which pushes the person gets.
      await page.route("**/v1/me/notifications", (r) => r.fulfill(json({ messages: true, updates: true, offers: true })));
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
      await page.route("**/v1/me/invitations", (r) => r.fulfill(json({ invitations: [] })));
      await page.route("**/v1/me/addresses", (r) => r.fulfill(json({ addresses: [] })));
      await page.route(/\/v1\/me\/chats(\?|$)/, (r) => r.fulfill(json({ chats: [], unread: 0 })));
      await page.route(/\/v1\/me\/conversations(\?|$)/, (r) => r.fulfill(json({ chats: [], unread: 0, requests: 0 })));
      await page.route("**/v1/me/notifications", (r) => r.fulfill(json({ messages: true, updates: true, offers: false })));
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
              { kind: "business", id: "b1c", name: "Mama's Kitchen", business_id: "b1", last: "Your rice is ready", last_mine: false, last_at: now, unread: 0, muted: true, archived: false, blocked: false },
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
            ],
            more: false,
          }),
        ),
      );
      await page.route(/\/v1\/me\/people\/r1\/messages(\?|$)/, (r) =>
        r.fulfill(json({ messages: [{ id: "q1", mine: false, kind: "text", body: "Hello, we met at the market", warn: true, read_at: null, created_at: now }], more: false })),
      );
      await page.route("**/v1/me/people/requests", (r) => r.fulfill(json({ requests: [req] })));
      await page.route(/\/v1\/me\/people(\?|$)/, (r) => r.fulfill(json({ chats: [chat], unread: 0, requests: 1 })));
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
      await page.route("**/v1/me/link", (r) => r.fulfill(json({ code: "ab23cd45ef", url: "http://localhost/p/ab23cd45ef", created_at: now, words: "Chat with me", qr: "/brand/seal/mark-512.png" })));

      await page.goto("/you");
      await expect(page.getByTestId("people-requests-entry")).toBeVisible();
      await expect(page.getByTestId("people-chat")).toBeVisible();
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
      await check("/you/people/{id}");
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

      for (const [path, testid] of [
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
