import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Saving after an action (saved objects S3, docs/spec/saved-objects.md
// §9.3: "after a delivery to a new address, after a new customer's first
// order"; suggest, never surprise), as the app does it. A new customer's
// first order offers to save them, one click, another name asked for when
// theirs is taken; the order is answered here, since an order's customer
// is sealed by the public site (api/saved_import_test.go reads the real
// one). A typed address saved as where linked suppliers deliver offers to
// save the place; a saved place picked does not. Against the real server.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const json = (body: unknown, status = 201) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
const now = new Date().toISOString();

// SCREENSHOT_DIR=... saves screenshots for review.
const shot = async (page: Page, name: string) => {
  if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: `${process.env.SCREENSHOT_DIR}/${name}.png`, fullPage: true });
};

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
    await shot(page, `saved-after-${where.replace(/\W+/g, "-")}-${width}-${scheme}`);
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
  expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(width);
}

const savedPerson = (name: string) => ({
  id: `s-${name}`,
  owner: "business",
  kind: "person",
  name,
  title: "Bola Ade",
  hint: "•••0099",
  details: { phone: "+2348031120099", address: "12 Admiralty Way" },
  check: { state: "none", account_name: null, via: null, at: null, problem: null },
  to_change: false,
  created_at: now,
  updated_at: now,
});

for (const width of [390, 360]) {
  test(`a new customer's first order: saved for next time, another name when taken, at ${width} px`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 844 });
    await owner(page);
    const order = (name: string, phone: string) => ({
      id: "o1",
      reference: "AK-SAVE1",
      kind: "order",
      status: "accepted",
      customer: { name, phone, address: "12 Admiralty Way" },
      basket: [{ id: "1", name: "Jollof rice", quantity: 2, price_kobo: 250_000, line_kobo: 500_000 }],
      total_kobo: 500_000,
      fulfilment: "pickup",
      payment: "none",
      created_at: now,
    });
    let savedAs = "";
    const sent: unknown[] = [];
    await page.route(/\/v1\/storefront\/orders\/AK-SAVE1(\?.*)?$/, (r) => {
      const reveal = new URL(r.request().url()).searchParams.get("reveal") === "true";
      const o = reveal ? order("Bola Ade", "+2348031120099") : order("B•••", "•••0099");
      return r.fulfill(json({ order: o, revealed: reveal, customer_orders: 1, customer_saved: savedAs }, 200));
    });
    await page.route("**/v1/storefront/orders/AK-SAVE1/save-customer", (r) => {
      const b = r.request().postDataJSON() as { name?: string };
      sent.push(b);
      // The customer's own name is taken: another is asked for.
      if (b.name !== "bola-yaba") return r.fulfill(json({ error: "That name is taken: choose another.", name_taken: true }, 409));
      savedAs = b.name;
      return r.fulfill(json({ saved: savedPerson(b.name) }));
    });

    await page.goto("/storefront/orders/AK-SAVE1");
    const card = page.getByRole("region", { name: "Customer" });
    // Hidden, the server makes the name from theirs.
    const offer = card.getByTestId("save-customer");
    await expect(offer).toContainText("Save this customer for next time");
    await expect(offer).toContainText("Among your saved people, with their phone and address");
    await axeBoth(page, "the first order, customer hidden", width);

    // Shown: by their name.
    await card.getByRole("button", { name: "Show customer's details" }).click();
    await expect(offer).toContainText("Save Bola Ade for next time");
    await expect(offer).toContainText("As @bola-ade, with their phone and address");
    await offer.getByRole("button", { name: "Save as @bola-ade" }).click();

    // Taken: another name, filled in to change.
    const list = card.getByRole("list", { name: "Save Bola Ade for next time" });
    await expect(list).toContainText("@bola-ade is taken: choose another.");
    const field = list.getByLabel("Name to call it up by");
    await expect(field).toHaveValue("@bola-ade");
    await field.fill("@bola eze");
    await list.getByRole("button", { name: "Save", exact: true }).click();
    await expect(list.getByRole("alert")).toHaveText("That name is taken too: choose another.");
    await axeBoth(page, "the first order, another name", width);
    await field.fill("@bola-yaba");
    await list.getByRole("button", { name: "Save", exact: true }).click();
    await expect(card.getByTestId("save-customer-done")).toContainText("Saved as @bola-yaba");
    await expect(card.getByTestId("save-customer")).toHaveCount(0);
    expect(sent).toEqual([{ name: "bola-ade" }, { name: "bola-eze" }, { name: "bola-yaba" }]);
    await axeBoth(page, "the first order, saved", width);

    // Again: the server says they are saved now, so nothing is offered.
    await page.reload();
    await expect(card.getByRole("heading", { name: "Customer" })).toBeVisible();
    await expect(card).toContainText("B•••");
    await expect(card.getByTestId("save-customer")).toHaveCount(0);
    await expect(card.getByTestId("save-customer-done")).toHaveCount(0);
  });

  test(`a typed address where linked suppliers deliver: offered as a place, a picked one not, at ${width} px`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 844 });
    await owner(page);
    for (const data of [
      { name: "warehouse", title: "Warehouse", kind: "place", details: { line: "Plot 7, Epe road", area: "Epe" } },
      { name: "shop-4", title: "Shop 4", kind: "place", details: { line: "Shop 4, Balogun Market" } },
    ]) {
      const r = await page.request.post("/v1/saved", { headers: H, data });
      expect(r.status(), await r.text()).toBe(201);
    }

    await page.goto("/storefront/suppliers");
    const card = page.getByTestId("deliver-to");
    await expect(card.getByRole("heading", { name: "Where linked suppliers deliver" })).toBeVisible();
    await card.getByLabel("Address").fill("12 Admiralty Way, Lekki");
    await card.getByLabel("Number the rider calls at the door").fill("08031120099");
    await card.getByRole("button", { name: "Save", exact: true }).click();
    const offer = card.getByTestId("save-place");
    await expect(offer).toContainText("Save this place for next time?");
    await expect(offer).toContainText("As @12-admiralty-way, among your saved places");
    await axeBoth(page, "a typed place offered", width);
    await offer.getByRole("button", { name: "Save as @12-admiralty-way" }).click();
    await expect(card.getByTestId("save-place-done")).toContainText("Saved as @12-admiralty-way");
    const got = await (await page.request.get("/v1/saved/resolve?name=12-admiralty-way")).text();
    expect(got).toContain("12 Admiralty Way, Lekki");
    expect(got).toContain('"kind":"place"');
    await axeBoth(page, "a typed place saved", width);

    // A saved place picked: nothing offered.
    await card.getByRole("button", { name: "Saved places for the address" }).click();
    await page.getByRole("dialog", { name: "Saved places" }).getByRole("button", { name: "Use Warehouse" }).click();
    await expect(card.getByLabel("Address")).toHaveValue("Plot 7, Epe road, Epe");
    const saved = page.waitForResponse((r) => r.url().endsWith("/v1/stock/deliver-to") && r.request().method() === "PUT");
    await card.getByRole("button", { name: "Save", exact: true }).click();
    expect((await saved).status()).toBe(200);
    await expect(card.getByLabel("Address")).toBeEnabled();
    await expect(card.getByTestId("save-place")).toHaveCount(0);
    await expect(card.getByTestId("save-place-done")).toHaveCount(0);

    // Its name taken: another, and saved with it.
    await card.getByLabel("Address").fill("Shop 4, Ikota Market");
    await card.getByRole("button", { name: "Save", exact: true }).click();
    await offer.getByRole("button", { name: "Save as @shop-4" }).click();
    const list = card.getByRole("list", { name: "Save this place for next time?" });
    await expect(list).toContainText("@shop-4 is taken: choose another.");
    await list.getByLabel("Name to call it up by").fill("@ikota-shop");
    await axeBoth(page, "a typed place, another name", width);
    await list.getByRole("button", { name: "Save", exact: true }).click();
    await expect(card.getByTestId("save-place-done")).toContainText("Saved as @ikota-shop");
    expect(await (await page.request.get("/v1/saved/resolve?name=ikota-shop")).text()).toContain("Shop 4, Ikota Market");
  });
}
