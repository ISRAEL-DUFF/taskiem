import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Pay several people (docs/spec/chat-pay-people.md, P3): the list filled
// from saved accounts and a pasted list, checked (a row held out in amber
// with why), confirmed with one passkey for the batch's rows, "pay again?"
// asked per row, a row per person filling in; the owner's OK for all and
// the failed ones paid again; Same as last time from the lists paid
// before; and Pay all riders from Riders > Pay. The e2e server has no
// payments account to pay from, so the batch, saved, riders and step-up
// APIs are stood in for, and the browser's passkey is a stand-in too.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Many Pay ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@many.test`,
      name: "Ada Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

const json = (r: Route, status: number, body: unknown) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

const B1 = "0192e2e0-0000-7000-8000-00000000c001";
const B2 = "0192e2e0-0000-7000-8000-00000000c002";
const P1 = "0192e2e0-0000-7000-8000-00000000d001";
const P2 = "0192e2e0-0000-7000-8000-00000000d002";
const P3 = "0192e2e0-0000-7000-8000-00000000d003";

const payment = (id: string, over: Record<string, unknown> = {}) => ({
  id,
  saved_id: "0192e2e0-0000-7000-8000-00000000b001",
  personal: false,
  payee: "Bayo Rice Mills",
  bank_name: "GTBank",
  account: "••6789",
  account_name: "BAYO ADEWALE",
  amount_kobo: 1_000_000,
  purpose: "bill",
  purpose_note: "",
  note: "",
  first_payment: false,
  changed_account: false,
  needs_ok: "",
  status: "prepared",
  reason: null,
  fee_kobo: null,
  run_id: null,
  started_from: "tools",
  prepared_by: "user:ada",
  confirmed_by: null,
  created_at: new Date().toISOString(),
  confirmed_at: null,
  finished_at: null,
  again: false,
  ...over,
});

function batch(id: string, payments: ReturnType<typeof payment>[], over: Record<string, unknown> = {}) {
  const live = payments.filter((x) => x.status !== "cancelled");
  return {
    id,
    title: "",
    kind: "list",
    status: "prepared",
    created_by: "user:ada",
    created_at: new Date().toISOString(),
    confirmed_at: null,
    payments,
    total_kobo: live.reduce((t, x) => t + x.amount_kobo, 0),
    count: live.length,
    paid_kobo: live.filter((x) => x.status === "paid").reduce((t, x) => t + x.amount_kobo, 0),
    waiting: live.filter((x) => x.status === "waiting_ok").length,
    failed: live.filter((x) => x.status === "failed").length,
    ...over,
  };
}

/** The browser's passkey, stood in for: any assertion asked for is given. */
async function fakePasskey(page: Page) {
  await page.addInitScript(() => {
    const buf = (s: string) => new TextEncoder().encode(s).buffer;
    Object.defineProperty(navigator, "credentials", {
      configurable: true,
      value: {
        get: async () => ({
          id: "cred-1",
          rawId: buf("cred-1"),
          type: "public-key",
          response: { clientDataJSON: buf("{}"), authenticatorData: buf("a"), signature: buf("s"), userHandle: buf("u") },
        }),
      },
    });
  });
  const asked: unknown[] = [];
  await page.route("**/v1/me/step-up/options", (r) => {
    asked.push(r.request().postDataJSON());
    return json(r, 200, { publicKey: { challenge: "Y2hhbGxlbmdl", rpId: "localhost", allowCredentials: [], userVerification: "required" } });
  });
  return asked;
}

async function noAxe(page: Page, what: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describe(`${what} (${scheme})`, vs)).toEqual([]);
  }
  await page.emulateMedia({ colorScheme: "light" });
}

const fits = async (page: Page, width: number) => expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

for (const width of [390, 360])
  test(`a list filled, checked, confirmed with one passkey and paid, ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await signUp(page);
    const asked = await fakePasskey(page);
    await page.route((u) => u.pathname === "/v1/saved" && u.searchParams.get("kind") === "bank_account", (r) =>
      json(r, 200, {
        limit: 100,
        saved: [
          { id: "0192e2e0-0000-7000-8000-00000000b001", owner: "business", name: "bayo", title: "Bayo Rice Mills", kind: "bank_account", hint: "GTBank ••6789", details: {}, check: { state: "checked", account_name: "BAYO ADEWALE", via: null, at: null, problem: null }, to_change: false, created_at: "", updated_at: "" },
          { id: "0192e2e0-0000-7000-8000-00000000b002", owner: "business", name: "ngozi", title: "Ngozi", kind: "bank_account", hint: "Opay ••1111", details: {}, check: { state: "unchecked", account_name: null, via: null, at: null, problem: null }, to_change: false, created_at: "", updated_at: "" },
        ],
      }),
    );
    const calls: { method: string; path: string; body: Record<string, unknown> }[] = [];
    // Bayo prepared, Chidi held out; confirmed: Bayo asked "pay again?",
    // then sent, then paid when read again.
    let stage = "prepared";
    const bayo = () =>
      payment(P1, stage === "prepared" ? {} : { status: stage === "sending" ? "sending" : "paid", confirmed_at: new Date().toISOString() });
    const now = () => batch(B1, [bayo()], { status: stage === "prepared" ? "prepared" : "sent", title: "Friday", confirmed_at: stage === "prepared" ? null : new Date().toISOString() });
    await page.route(
      (u) => u.pathname.startsWith("/v1/payment-batches"),
      async (r) => {
        const req = r.request();
        const path = new URL(req.url()).pathname;
        const body = (req.postDataJSON() ?? {}) as Record<string, unknown>;
        calls.push({ method: req.method(), path, body });
        if (path === "/v1/payment-batches" && req.method() === "GET") return json(r, 200, { batches: [] });
        if (path === "/v1/payment-batches/read")
          return json(r, 200, {
            lines: [
              { line: 1, text: "Chidi 0123456789 GTBank 45k", account_number: "0123456789", bank_name: "GTBank", amount_kobo: 4_500_000, name: "Chidi" },
              { line: 2, text: "Tunde 20k", amount_kobo: 2_000_000, name: "Tunde", problem: "No account number of 10 digits on this line." },
            ],
          });
        if (path === "/v1/payment-batches" && req.method() === "POST")
          return json(r, 201, { batch: { ...now(), held: [{ row: 1, payee: "GTBank ••6789", code: "name_check", problem: "The bank has no account with that number." }] } });
        if (path === `/v1/payment-batches/${B1}/confirm`) {
          if (!body.passkey) return json(r, 403, { error: "confirm with your passkey", step_up: "required", methods: ["passkey"], operation: "money.pay.batch", target: `batch:${B1}` });
          if (!(body.again as string[]).includes(P1)) return json(r, 202, { batch: { ...now(), results: [{ payment: P1, code: "pay_again", problem: "You paid Bayo Rice Mills ₦10,000 in the last day." }] } });
          stage = "sending";
          return json(r, 202, { batch: { ...now(), results: [{ payment: P1 }] } });
        }
        if (path === `/v1/payment-batches/${B1}`) {
          const b = now();
          if (stage === "sending") stage = "paid";
          return json(r, 200, { batch: b });
        }
        return json(r, 404, { error: "not found" });
      },
    );

    await page.goto("/payments");
    await page.getByTestId("pay-several").click();
    await expect(page.getByRole("heading", { name: "Pay several people", level: 1 })).toBeVisible();
    await noAxe(page, "an empty list");

    // Saved accounts picked.
    await page.getByTestId("add-saved").click();
    const picker = page.getByRole("dialog", { name: "Add saved accounts" });
    await picker.getByRole("checkbox", { name: /BAYO ADEWALE/ }).check();
    await noAxe(page, "the saved accounts to pick");
    await picker.getByTestId("pick-add").click();
    await expect(page.getByRole("status")).toContainText("Added 1 person.");

    // A pasted list: each line a row, the one that cannot be read said.
    await page.getByTestId("add-paste").click();
    const paste = page.getByRole("dialog", { name: "Paste a list" });
    await paste.getByTestId("paste-text").fill("Chidi 0123456789 GTBank 45k\nTunde 20k");
    await paste.getByTestId("paste-read").click();
    const rows = page.getByTestId("list-row");
    await expect(rows).toHaveCount(3);
    await expect(rows.nth(1).getByTestId("list-number")).toHaveValue("0123456789");
    await expect(rows.nth(1).getByTestId("list-amount")).toHaveValue("45,000");
    await expect(rows.nth(2).getByTestId("list-row-problem")).toHaveText("No account number of 10 digits on this line.");
    await expect(rows.nth(2)).toContainText("As pasted: Tunde 20k");

    await rows.nth(0).getByTestId("list-amount").fill("10k");
    await rows.nth(0).getByTestId("list-for").selectOption("a bill");
    await rows.nth(1).getByTestId("list-for").selectOption("wages");
    await page.getByTestId("list-title").fill("Friday");
    await expect(page.getByTestId("list-total")).toHaveText("3 people · ₦75,000");
    await noAxe(page, "the list with a row to fix");
    await fits(page, width);

    // Tunde's line cannot go: Check says so; removed, it goes.
    await page.getByTestId("list-check").click();
    await expect(page.getByText("Fix the rows marked, or remove them.")).toBeVisible();
    await rows.nth(2).getByRole("button", { name: "Remove Tunde" }).click();
    await expect(page.getByTestId("list-total")).toHaveText("2 people · ₦55,000");
    await page.getByTestId("list-check").click();
    const made = calls.find((c) => c.method === "POST" && c.path === "/v1/payment-batches");
    expect(made?.body).toEqual({
      from: "tools",
      title: "Friday",
      kind: "list",
      rows: [
        { saved_id: "0192e2e0-0000-7000-8000-00000000b001", amount_kobo: 1_000_000, purpose: "bill" },
        { account_number: "0123456789", bank_name: "GTBank", amount_kobo: 4_500_000, purpose: "wages" },
      ],
    });

    // Checked: Bayo to pay, Chidi held out in amber with why.
    await expect(page).toHaveURL(new RegExp(`/payments/batches/${B1}$`));
    await expect(page.getByTestId("batch-amount")).toHaveText("₦10,000");
    await expect(page.getByTestId("batch-status")).toHaveText("To confirm");
    const held = page.getByTestId("batch-held-row");
    await expect(held).toHaveCount(1);
    await expect(held).toContainText("The bank has no account with that number.");
    await expect(page.getByTestId("batch-row")).toContainText("BAYO ADEWALE");
    await expect(page.getByTestId("batch-confirm")).toHaveText("Pay 1 person ₦10,000 with your passkey");
    await noAxe(page, "the checked list");
    await fits(page, width);
    await held.getByRole("button", { name: "Remove GTBank ••6789" }).click();
    await expect(page.getByTestId("batch-held")).toBeHidden();

    // Confirmed with one passkey; "pay again?" asked for Bayo's row.
    await page.getByTestId("batch-confirm").click();
    await expect(page.getByTestId("batch-again")).toBeVisible();
    await expect(page.getByTestId("batch-row-problem")).toHaveText("You paid Bayo Rice Mills ₦10,000 in the last day.");
    await noAxe(page, "pay again asked");
    await page.getByTestId("batch-pay-again").check();
    await page.getByTestId("batch-confirm").click();
    await expect(page.getByTestId("batch-status")).toHaveText(/Sending|All paid/);
    const confirms = calls.filter((c) => c.path.endsWith("/confirm")).map((c) => ({ payments: c.body.payments, again: c.body.again, passkey: !!c.body.passkey }));
    expect(confirms).toEqual([
      { payments: [P1], again: [], passkey: false },
      { payments: [P1], again: [], passkey: true },
      { payments: [P1], again: [P1], passkey: false },
      { payments: [P1], again: [P1], passkey: true },
    ]);
    expect(asked).toEqual([
      { operation: "money.pay.batch", target: `batch:${B1}` },
      { operation: "money.pay.batch", target: `batch:${B1}` },
    ]);

    // The row fills in as it goes: read again until paid.
    await expect(page.getByTestId("batch-status")).toHaveText("All paid", { timeout: 10_000 });
    await expect(page.getByTestId("batch-row")).toContainText("Paid");
    await expect(page.getByTestId("batch-amount")).toHaveText("₦10,000");
    await expect(page.getByTestId("batch-again-failed")).toBeHidden();
    await noAxe(page, "the list paid");
    await fits(page, width);
  });

test("the owner's OK for all, the failed ones again, and Same as last time", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await signUp(page);
  await fakePasskey(page);
  const calls: { path: string; body: Record<string, unknown> }[] = [];
  let approved = false;
  const sent = () =>
    batch(
      B1,
      [
        payment(P1, { status: "paid" }),
        payment(P2, { payee: "Chidi", account_name: "CHIDI OKEKE", account: "••1234", status: approved ? "sending" : "waiting_ok", needs_ok: "limit", confirmed_by: "user:ada", amount_kobo: 4_500_000 }),
        payment(P3, { payee: "Tunde", account_name: "TUNDE BELLO", account: "••2222", status: "failed", reason: "account closed", amount_kobo: 2_000_000 }),
      ],
      { status: "sent", title: "Riders, week 45", kind: "riders", confirmed_at: new Date().toISOString() },
    );
  await page.route(
    (u) => u.pathname.startsWith("/v1/payment-batches"),
    async (r) => {
      const req = r.request();
      const path = new URL(req.url()).pathname;
      const body = (req.postDataJSON() ?? {}) as Record<string, unknown>;
      calls.push({ path, body });
      if (path === "/v1/payment-batches" && req.method() === "GET") return json(r, 200, { batches: [{ ...sent(), payments: [] }] });
      if (path === "/v1/payment-batches" && req.method() === "POST") return json(r, 201, { batch: batch(B2, [payment(P1, { id: "0192e2e0-0000-7000-8000-00000000d009" })], { title: "Riders, week 45" }) });
      if (path === `/v1/payment-batches/${B1}/approve`) {
        if (!body.passkey) return json(r, 403, { error: "confirm with your passkey", step_up: "required", methods: ["passkey"], operation: "money.pay.approve", target: `batch:${B1}` });
        approved = true;
        return json(r, 200, { batch: { ...sent(), results: [{ payment: P2 }] } });
      }
      if (path === `/v1/payment-batches/${B1}/again`) return json(r, 201, { batch: batch(B2, [payment("0192e2e0-0000-7000-8000-00000000d00a", { payee: "Tunde", account_name: "TUNDE BELLO", amount_kobo: 2_000_000 })], { title: "Riders, week 45 (again)" }) });
      if (path === `/v1/payment-batches/${B1}`) return json(r, 200, { batch: sent() });
      if (path === `/v1/payment-batches/${B2}`) return json(r, 200, { batch: batch(B2, [payment("0192e2e0-0000-7000-8000-00000000d00a", { payee: "Tunde", account_name: "TUNDE BELLO", amount_kobo: 2_000_000 })], { title: "Riders, week 45 (again)" }) });
      return json(r, 404, { error: "not found" });
    },
  );

  // The lists paid before, on Pay people.
  await page.goto("/payments");
  const listed = page.getByTestId("batch");
  await expect(listed).toHaveCount(1);
  await expect(listed).toContainText("Riders, week 45");
  await expect(listed).toContainText("Waiting for an OK");
  await expect(listed).toContainText("₦10,000 paid · 1 waiting for an OK · 1 not paid");
  await noAxe(page, "the lists paid before");
  await listed.getByRole("link", { name: "Open Riders, week 45" }).click();

  await expect(page.getByTestId("batch-amount")).toHaveText("₦10,000");
  await expect(page.getByTestId("batch-sub")).toHaveText("of ₦75,000 to 3 people");
  await expect(page.getByTestId("batch-progress")).toHaveText("1 paid · 1 waiting for the owner's OK · 1 not paid");
  await expect(page.getByTestId("batch-row").nth(2)).toContainText("account closed");
  await noAxe(page, "a list with rows waiting and failed");
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);

  await page.getByTestId("batch-approve").click();
  await expect(page.getByTestId("batch-approve")).toBeHidden();
  const ok = calls.filter((c) => c.path.endsWith("/approve")).map((c) => ({ payments: c.body.payments, passkey: !!c.body.passkey }));
  expect(ok).toEqual([
    { payments: [P2], passkey: false },
    { payments: [P2], passkey: true },
  ]);

  // The failed one as a new list, to check and confirm.
  await page.getByTestId("batch-again-failed").click();
  await expect(page).toHaveURL(new RegExp(`/payments/batches/${B2}$`));
  await expect(page.getByTestId("batch-row")).toContainText("TUNDE BELLO");
  await expect(page.getByTestId("batch-confirm")).toHaveText("Pay 1 person ₦20,000 with your passkey");

  // Same as last time, from the lists paid before.
  await page.goto("/payments");
  await page.getByRole("button", { name: "Same as last time for Riders, week 45" }).click();
  await expect(page).toHaveURL(new RegExp(`/payments/batches/${B2}$`));
  expect(calls.find((c) => c.path === "/v1/payment-batches" && c.body.copy_of)?.body).toEqual({ from: "tools", copy_of: B1 });
});

test("Pay all riders fills a list from what is due to each", async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await signUp(page);
  const rider = (n: number) => `0192e2e0-0000-7000-8000-00000000e00${n}`;
  await page.route("**/v1/riders/owed", (r) => json(r, 200, { owed: [] }));
  await page.route("**/v1/riders/pay", (r) =>
    json(r, 200, {
      workflow_id: null,
      payouts: [],
      lines: [
        { rider_id: rider(1), rider: "Musa Bello", deliveries: 3, fees_kobo: 450_000, owed_kobo: 0, net_kobo: 450_000, bank: "GTBank ••6789" },
        { rider_id: rider(2), rider: "Kemi Ade", deliveries: 2, fees_kobo: 300_000, owed_kobo: 0, net_kobo: 300_000, bank: "Opay ••1111" },
        { rider_id: rider(3), rider: "No Bank", deliveries: 1, fees_kobo: 100_000, owed_kobo: 0, net_kobo: 100_000, bank: "" },
      ],
    }),
  );
  let made: Record<string, unknown> | undefined;
  await page.route(
    (u) => u.pathname.startsWith("/v1/payment-batches"),
    (r) => {
      const req = r.request();
      const path = new URL(req.url()).pathname;
      if (path === "/v1/payment-batches" && req.method() === "POST") {
        made = req.postDataJSON() as Record<string, unknown>;
        return json(r, 201, { batch: batch(B1, [payment(P1, { payee: "Musa Bello", account_name: "MUSA BELLO", amount_kobo: 450_000, purpose: "delivery" })], { kind: "riders", title: "Riders' pay" }) });
      }
      if (path === `/v1/payment-batches/${B1}`) return json(r, 200, { batch: batch(B1, [payment(P1, { payee: "Musa Bello", account_name: "MUSA BELLO", amount_kobo: 450_000, purpose: "delivery" })], { kind: "riders", title: "Riders' pay" }) });
      return json(r, 200, { batches: [] });
    },
  );
  await page.goto("/storefront/riders/pay");
  await page.getByTestId("riders-pay-all").click();
  await expect(page).toHaveURL(/\/payments\/several\?riders=1$/);
  const rows = page.getByTestId("list-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("Musa Bello");
  await expect(rows.nth(0).getByTestId("list-amount")).toHaveValue("4,500");
  await expect(rows.nth(1).getByTestId("list-for")).toHaveValue("a delivery");
  await expect(page.getByTestId("list-title")).toHaveValue("Riders' pay");
  await expect(page.getByTestId("list-total")).toHaveText("2 people · ₦7,500");
  await noAxe(page, "the riders' list");
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(360);

  // Add riders: one with no bank account cannot be ticked.
  await page.getByTestId("add-riders").click();
  const picker = page.getByRole("dialog", { name: "Add riders" });
  await expect(picker.getByRole("checkbox", { name: /No Bank/ })).toBeDisabled();
  await noAxe(page, "the riders to pick");
  await picker.getByRole("button", { name: "Cancel" }).click();

  await page.getByTestId("list-check").click();
  await expect(page).toHaveURL(new RegExp(`/payments/batches/${B1}$`));
  expect(made).toEqual({
    from: "tools",
    title: "Riders' pay",
    kind: "riders",
    rows: [
      { rider_id: rider(1), amount_kobo: 450_000, purpose: "delivery" },
      { rider_id: rider(2), amount_kobo: 300_000, purpose: "delivery" },
    ],
  });
});
