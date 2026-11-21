import { expect, test, type Page, type Route } from "@playwright/test";
import { axe, describe } from "./a11y";

// Saved buttons for more kinds of field (docs/spec/saved-objects.md §6.4:
// "Anywhere a form, an order or a sheet asks for an address, a phone, a
// person, a bank account or a date, a Saved button offers the owner's
// objects of the right kind"): a phone (Add staff), a bank account (a
// rider's payout, its number shown with a passkey), a date (Cash and
// expenses), and the list when it fails or has nothing. The saved lists
// are answered here (api/saved_test.go reads the real ones). WebAuthn
// cannot run here, so navigator.credentials.get is a stand-in that
// answers as a passkey would; the reveal it is sent to is answered here.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
const now = new Date().toISOString();

async function owner(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: `Ada Kitchen ${letters()}`, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@saved.test`, name: "Ada Obi", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function axeBoth(page: Page, where: string, width: number) {
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
  expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
}

const thing = (x: Record<string, unknown>) => ({
  check: { state: "none", account_name: null, via: null, at: null, problem: null },
  to_change: false,
  created_at: now,
  updated_at: now,
  ...x,
});

/** Both saved lists, filtered by ?kind= as the server does. */
async function lists(page: Page, mine: Record<string, unknown>[], business: Record<string, unknown>[]) {
  const answer = (r: Route, saved: Record<string, unknown>[]) => {
    const kind = new URL(r.request().url()).searchParams.get("kind");
    return r.fulfill(json({ limit: 500, saved: saved.filter((x) => !kind || x.kind === kind) }));
  };
  await page.unroute(/\/v1\/me\/saved(\?.*)?$/);
  await page.unroute(/\/v1\/saved(\?.*)?$/);
  await page.route(/\/v1\/me\/saved(\?.*)?$/, (r) => answer(r, mine));
  await page.route(/\/v1\/saved(\?.*)?$/, (r) => answer(r, business));
}

const BISI = thing({ id: "s1", owner: "business", kind: "person", name: "bisi", title: "Bisi Okafor", hint: "•••4567", details: { phone: "08031234567", birthday: "1990-07-04" } });
const SHOP = thing({ id: "s2", owner: "business", kind: "place", name: "shop", title: "The shop", hint: "Allen Avenue", details: { line: "12 Allen Avenue", phone: "08037654321" } });
const NOTE = thing({ id: "s3", owner: "business", kind: "text", name: "thanks", title: "Thanks", hint: "", details: { body: "Thank you" } });

for (const width of [390, 360]) {
  test(`Saved fills a phone, a date, and says when it fails or has nothing, at ${width} px`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 844 });
    await owner(page);

    // When both lists fail: one error, and no "nothing saved" over it.
    await page.route(/\/v1\/(me\/)?saved(\?.*)?$/, (r) => r.fulfill(json({ error: "Saved things could not be read just now." }, 500)));
    await page.goto("/invoices/new");
    await page.getByRole("button", { name: "Saved people for the customer" }).click();
    const picker = page.getByRole("dialog", { name: "Saved people" });
    await expect(picker.getByRole("alert")).toHaveCount(1);
    await expect(picker.getByRole("alert")).toHaveText("Saved things could not be read just now.");
    await expect(picker).not.toContainText("Nothing saved");
    await axeBoth(page, "the Saved list when it fails", width);
    await picker.getByRole("button", { name: "Close" }).click();
    await page.unroute(/\/v1\/(me\/)?saved(\?.*)?$/);

    // Nothing saved: the way to Saved things opens in a new tab, so the
    // form being filled stays.
    await lists(page, [], [NOTE]);
    await page.getByRole("button", { name: "Saved people for the customer" }).click();
    await expect(picker).toContainText("Nothing saved of this kind yet.");
    const way = picker.getByRole("link", { name: /Saved things/ });
    await expect(way).toHaveAttribute("href", "/settings/saved");
    await expect(way).toHaveAttribute("target", "_blank");
    await expect(way).toHaveAttribute("rel", "noreferrer");
    await axeBoth(page, "the Saved list with nothing in it", width);
    await picker.getByRole("button", { name: "Close" }).click();

    // A phone: Add staff offers saved people's and places' phones.
    await lists(page, [], [BISI, SHOP, NOTE]);
    await page.goto("/staff");
    await page.getByRole("button", { name: "Add staff" }).click();
    const add = page.getByRole("dialog", { name: "Add staff" });
    await add.getByRole("button", { name: "Saved phones for their mobile number" }).click();
    const phones = page.getByRole("dialog", { name: "Saved phones" });
    await expect(phones.getByTestId("saved-fill-row")).toHaveCount(2);
    await expect(phones).not.toContainText("Thanks");
    await axeBoth(page, "the Saved phones picker over Add staff", width);
    await phones.getByRole("button", { name: "Use The shop" }).click();
    await expect(phones).toHaveCount(0);
    await expect(add).toBeVisible();
    await expect(add.getByLabel("Their mobile number")).toHaveValue("08037654321");
    await expect(add.getByRole("button", { name: "Saved phones for their mobile number" })).toBeFocused();
    await add.getByRole("button", { name: "Cancel" }).click();

    // A date: dates saved on things, a sensitive one left out.
    const car = { id: "k1", name: "Car", count: 1, fields: [
      { key: "insurance_due", label: "Insurance due", type: "date" },
      { key: "bought", label: "Bought", type: "date", sensitive: true },
      { key: "plate", label: "Plate", type: "text" },
    ] };
    await lists(page, [thing({ id: "m1", owner: "mine", kind: "custom", kind_id: "k1", kind_name: "Car", name: "car", title: "The car", hint: "", details: {}, values: { insurance_due: "2027-03-03", bought: "••••", plate: "KJA 123 AB" } })], [BISI, SHOP]);
    await page.route("**/v1/me/saved-kinds", (r) => r.fulfill(json({ kinds: [car], starters: [], limit: 20, to_change: false })));
    await page.route("**/v1/saved-kinds", (r) => r.fulfill(json({ kinds: [], starters: [], limit: 20, to_change: false })));
    await page.goto("/money/entries");
    await page.getByRole("button", { name: "Saved dates for the date" }).click();
    const dates = page.getByRole("dialog", { name: "Saved dates" });
    await expect(dates.getByTestId("saved-fill-row")).toHaveCount(2);
    await expect(dates).toContainText("@car · Insurance due · 3 Mar 2027");
    await expect(dates).toContainText(/@bisi · Birthday · 4 Jul 20\d\d/);
    await expect(dates).not.toContainText("Bought");
    await axeBoth(page, "the Saved dates picker", width);
    await dates.getByRole("button", { name: "Use The car: Insurance due" }).click();
    await expect(page.getByLabel("Date", { exact: true })).toHaveValue("2027-03-03");
  });

  test(`a purchase's own delivery place, from a saved place, until the supplier has it, at ${width} px`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 844 });
    await owner(page);
    const purchase = {
      id: "p1", reference: "P-K2M9PA", supplier_id: "s1", supplier: "Dangote Depot", lines: [{ item_id: "1", item: "Indomie", unit: "carton", quantity: 4, cost_kobo: 850_000, received: 0 }],
      total_kobo: 3_400_000, status: "drafted", drafted_by: "user:ada", approved_by: null, sent_at: null, confirmed_at: null, confirmed_how: null, counter: null, counter_at: null,
      received_at: null, cancelled_at: null, received_kobo: 0, paid_kobo: 0, owed_kobo: 0, created_at: now,
    };
    await page.route("**/v1/stock/purchases/p1", (r) => r.fulfill(json({ purchase })));
    let place = { address: "12 Admiralty Way, Lekki", phone: "+2348030000000", own: false };
    let reached = false;
    const put: unknown[] = [];
    await page.route("**/v1/stock/purchases/p1/deliver-to", (r) => {
      if (r.request().method() === "GET") return r.fulfill(json(place));
      put.push(r.request().postDataJSON());
      if (reached) return r.fulfill(json({ error: "P-K2M9PA has reached the supplier already: tell them in your conversation with them" }, 409));
      const b = r.request().postDataJSON() as { address: string; phone: string };
      place = { ...b, own: true };
      return r.fulfill(json(place));
    });
    await lists(page, [], [SHOP, thing({ id: "s4", owner: "business", kind: "place", name: "farm", title: "The farm", hint: "Epe", details: { line: "Plot 7, Epe road", area: "Epe", phone: "08035551234" } })]);

    await page.goto("/purchases/p1");
    const card = page.getByTestId("purchase-deliver-to");
    await expect(card.getByRole("heading", { name: "Delivered to" })).toBeVisible();
    await expect(card).toContainText("12 Admiralty Way, Lekki");
    await expect(card).toContainText("Where your orders usually go");
    await axeBoth(page, "a drafted purchase's Delivered to", width);

    // Change, from a saved place.
    await card.getByRole("button", { name: "Change where it is delivered" }).click();
    const modal = page.getByRole("dialog", { name: "Where P-K2M9PA is delivered" });
    await modal.getByRole("button", { name: "Saved places for where it is delivered" }).click();
    const places = page.getByRole("dialog", { name: "Saved places" });
    await expect(places.getByTestId("saved-fill-row")).toHaveCount(2);
    await axeBoth(page, "the Saved places picker over a purchase's place", width);
    await places.getByRole("button", { name: "Use The farm" }).click();
    await expect(modal.getByLabel("Address")).toHaveValue("Plot 7, Epe road, Epe");
    await expect(modal.getByLabel("Number the rider calls at the door")).toHaveValue("08035551234");
    await axeBoth(page, "a purchase's place, filled", width);
    await modal.getByRole("button", { name: "Save", exact: true }).click();
    await expect(modal).toHaveCount(0);
    expect(put).toEqual([{ address: "Plot 7, Epe road, Epe", phone: "08035551234" }]);
    await expect(card).toContainText("Plot 7, Epe road, Epe");
    await expect(card).toContainText("This order's own place");
    // A saved place picked is not offered to save (saved objects S3).
    await expect(card.getByTestId("save-place")).toHaveCount(0);

    // A typed one is.
    await card.getByRole("button", { name: "Change where it is delivered" }).click();
    await modal.getByLabel("Address").fill("5 Akin Adesola Street, Victoria Island");
    await modal.getByRole("button", { name: "Save", exact: true }).click();
    await expect(modal).toHaveCount(0);
    await expect(card).toContainText("5 Akin Adesola Street, Victoria Island");
    await expect(card.getByTestId("save-place")).toContainText("Save this place for next time?");
    await expect(card.getByTestId("save-place")).toContainText("As @5-akin-adesola-street, among your saved places");
    await axeBoth(page, "a purchase's typed place offered", width);

    // Once the supplier has it, the server says so and it is only shown.
    reached = true;
    await card.getByRole("button", { name: "Change where it is delivered" }).click();
    await modal.getByLabel("Address").fill("3 Bode Thomas, Surulere");
    await modal.getByRole("button", { name: "Save", exact: true }).click();
    await expect(modal).toHaveCount(0);
    await expect(card.getByTestId("purchase-deliver-closed")).toHaveText("P-K2M9PA has reached the supplier already: tell them in your conversation with them");
    await expect(card.getByRole("button", { name: "Change where it is delivered" })).toHaveCount(0);
    await expect(card).toContainText("5 Akin Adesola Street, Victoria Island");
    await axeBoth(page, "a purchase's place the supplier has", width);
  });

  test(`Saved fills a rider's bank account, its number shown with a passkey, at ${width} px`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 844 });
    await owner(page);
    // A passkey, as far as the page can tell.
    await page.addInitScript(() => {
      const buf = () => new Uint8Array([1, 2, 3]).buffer;
      Object.defineProperty(navigator, "credentials", {
        configurable: true,
        value: {
          get: async () => ({ id: "AQID", rawId: buf(), type: "public-key", response: { clientDataJSON: buf(), authenticatorData: buf(), signature: buf(), userHandle: null } }),
        },
      });
    });
    await page.route("**/v1/riders/owed", (r) => r.fulfill(json({ owed: [] })));
    await page.route("**/v1/riders/pay", (r) =>
      r.fulfill(json({ lines: [{ rider_id: "r1", rider: "Musa", deliveries: 3, fees_kobo: 300_000, owed_kobo: 0, net_kobo: 300_000, bank: "" }], payouts: [], workflow_id: null })),
    );
    await page.route("**/v1/receive-money/banks", (r) =>
      r.fulfill(json({ banks: [{ code: "000014", name: "Access Bank" }, { code: "000013", name: "Guaranty Trust Bank" }] })),
    );
    await lists(page, [], [
      thing({ id: "b1", owner: "business", kind: "bank_account", name: "musa", title: "Musa's account", hint: "GTBank ••6789", details: { bank_name: "Guaranty Trust Bank", bank_code: "058", account_number: "••6789" } }),
      BISI,
    ]);
    // First no passkey yet: only the bank is filled, and said why.
    let passkeys = false;
    const revealed: string[] = [];
    await page.route("**/v1/saved/step-up/options", (r) =>
      passkeys
        ? r.fulfill(json({ publicKey: { challenge: "AAAA", allowCredentials: [] } }))
        : r.fulfill(json({ error: "Add a passkey first: changing a saved account, or seeing its number in full, needs one.", no_passkey: true }, 409)),
    );
    await page.route("**/v1/saved/b1/reveal", (r) => {
      revealed.push(String((r.request().postDataJSON() as { passkey?: { id?: string } }).passkey?.id));
      return r.fulfill(json({ account_number: "0123456789" }));
    });

    await page.goto("/storefront/riders/pay");
    await page.getByRole("button", { name: "Add bank account for Musa" }).click();
    const modal = page.getByRole("dialog", { name: "Musa's bank account" });
    await modal.getByRole("button", { name: "Saved accounts for Musa's bank account" }).click();
    const accounts = page.getByRole("dialog", { name: "Saved accounts" });
    await expect(accounts.getByTestId("saved-fill-row")).toHaveCount(1);
    await expect(accounts).toContainText("@musa · GTBank ••6789");
    await expect(accounts).not.toContainText("0123456789");
    await axeBoth(page, "the Saved accounts picker", width);
    await accounts.getByRole("button", { name: "Use Musa's account" }).click();
    await expect(modal.getByRole("combobox", { name: "Bank" })).toHaveValue("000013");
    await expect(modal.getByLabel("Account number")).toHaveValue("");
    await expect(modal.getByTestId("saved-fill-note")).toHaveText("Add a passkey to use a saved account here.");
    expect(revealed).toEqual([]);

    // With a passkey: the number is shown for this pick and filled.
    passkeys = true;
    await modal.getByRole("button", { name: "Saved accounts for Musa's bank account" }).click();
    await accounts.getByRole("button", { name: "Use Musa's account" }).click();
    await expect(modal.getByLabel("Account number")).toHaveValue("0123456789");
    await expect(modal.getByRole("combobox", { name: "Bank" })).toHaveValue("000013");
    await expect(modal.getByTestId("saved-fill-note")).toHaveCount(0);
    expect(revealed).toEqual(["AQID"]);
    await axeBoth(page, "a rider's bank account filled from Saved", width);
  });
}
