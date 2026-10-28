import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// A business's own riders (dispatch D1; docs/dispatch.md): the owner adds
// a rider by name and phone, picking the areas from the order form's,
// sees where Taskiem reaches them and whether they are free, changes and
// retires them. Riders are reached by text message, which serve.sh's
// TASKIEM_MESSAGE_HASH_KEY turns on.
test.use({ bypassCSP: true }); // axe is injected into the page

const H = { "X-Taskiem-Request": "1" };

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant: "Ada Stores", email: `ada-${Date.now()}@riders.test`, name: "Ada", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

test("an owner adds, changes and retires a rider", async ({ page }) => {
  await signUp(page);
  // The storefront and its order form's delivery areas.
  let r = await page.request.put("/v1/storefront", { headers: H, data: { slug: `ada-${Date.now()}`, draft: { name: "Ada Stores" } } });
  expect(r.status(), await r.text()).toBe(200);
  r = await page.request.put("/v1/storefront/actions/order", {
    headers: H,
    data: {
      payment: "after_accept",
      options: { delivery: true, zones: [{ name: "Lekki", fee_kobo: 150000 }, { name: "Ajah", fee_kobo: 200000 }] },
    },
  });
  expect(r.status(), await r.text()).toBe(200);

  await page.goto("/storefront/riders");
  await expect(page.getByRole("heading", { name: "Riders", exact: true })).toBeVisible();
  await expect(page.getByText("No riders yet")).toBeVisible();
  await expect(page.getByText("The link for riders")).toBeVisible();
  await axeBoth(page, "/storefront/riders, empty");

  // Add Musa, covering Lekki.
  await page.getByRole("button", { name: "Add a rider" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Name").fill("Musa Bello");
  await dialog.getByLabel("Mobile number").fill("0803 123 4567");
  await dialog.getByLabel("Lekki, ₦1,500 delivery").check();
  await dialog.getByLabel("Fee per delivery (₦)").fill("1,000");
  await axeBoth(page, "add a rider");
  await dialog.getByRole("button", { name: "Add the rider" }).click();
  await expect(dialog).toBeHidden();
  const row = page.getByTestId("rider").filter({ hasText: "Musa Bello" });
  await expect(row).toContainText("Free");
  await expect(row).toContainText("Lekki · ₦1,000 a delivery · By SMS until they tap the link");
  await axeBoth(page, "/storefront/riders, one rider");

  // The same phone again is refused.
  await page.getByRole("button", { name: "Add a rider" }).click();
  await dialog.getByLabel("Name").fill("Musa again");
  await dialog.getByLabel("Mobile number").fill("08031234567");
  await dialog.getByRole("button", { name: "Add the rider" }).click();
  await expect(dialog).toContainText("already rides for you");
  await dialog.getByRole("button", { name: "Cancel" }).click();

  // Change: both areas, on leave.
  await row.getByRole("button", { name: "Change Musa Bello" }).click();
  await dialog.getByLabel("Ajah, ₦2,000 delivery").check();
  await dialog.getByLabel("On leave: keep them, but offer them nothing").check();
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
  await expect(row).toContainText("On leave");
  await expect(row).toContainText("Lekki, Ajah");

  // Retire: gone from the list, kept under retired riders.
  page.once("dialog", (d) => void d.accept());
  await row.getByRole("button", { name: "Retire Musa Bello" }).click();
  await expect(page.getByText("No riders yet")).toBeVisible();
  await page.getByRole("button", { name: "Show retired riders" }).click();
  await expect(page.getByTestId("rider").filter({ hasText: "Musa Bello" })).toBeVisible();
});

// Deliveries and paying riders (dispatch D2 to D5): the owner sets how
// orders are offered, and sees what riders owe and are due.
test("an owner sets how orders are offered and opens riders' pay", async ({ page }) => {
  await signUp(page);
  await page.goto("/storefront/deliveries");
  await expect(page.getByRole("heading", { name: "Deliveries", exact: true })).toBeVisible();
  await expect(page.getByText("No deliveries that day.")).toBeVisible();
  await axeBoth(page, "/storefront/deliveries");

  // Without delivery on the order form, deliveries cannot be turned on.
  await page.getByRole("button", { name: "Turn on deliveries" }).click();
  await expect(page.getByText("turn on delivery on your order form first")).toBeVisible();

  await page.getByRole("button", { name: "How orders are offered" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Ask me in chat who takes it").check();
  await dialog.getByLabel("Minutes a rider has to accept").fill("0");
  await expect(dialog.getByText("Riders have 1 to 60 minutes to accept an offer.")).toBeVisible();
  await dialog.getByLabel("Minutes a rider has to accept").fill("10");
  await dialog.getByLabel("Ask riders for a photo at the door").check();
  await axeBoth(page, "how orders are offered");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
  const s = await (await page.request.get("/v1/dispatch/settings", { headers: H })).json();
  expect(s).toMatchObject({ mode: "ask", offer_minutes: 10, proof: true });

  await page.goto("/storefront/riders");
  await page.getByRole("link", { name: "Pay riders" }).click();
  await expect(page.getByRole("heading", { name: "Pay riders", exact: true })).toBeVisible();
  await expect(page.getByText("No rider owes you cash.")).toBeVisible();
  await expect(page.getByText("Weekly pay is not set up yet")).toBeVisible();
  await expect(page.getByRole("button", { name: "Pay riders now" })).toBeDisabled();
  await axeBoth(page, "/storefront/riders/pay");
  // Without a Taskiem wallet or another payments account, setting it up says so.
  await page.getByRole("button", { name: "Set up weekly pay" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("dialog")).toContainText("turn on receiving money with Taskiem");
});
