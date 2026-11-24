import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Pages H5 (build-plan-gaps §7, g-pages-editor), done when: the deliveries
// fixture page (today's deliveries from Taskiem's records, what is owed to
// riders as a total, Confirm on each row telling the run waiting for that
// delivery, shown only while it is delivered and not confirmed, each row's
// run, the customers and staff) is made from the editor alone, without a
// file, in plain words, and publishes. Real server: the page is checked by
// the API as it is saved and published (api/pages_h5_test.go holds the
// same document against real deliveries).
test.use({ bypassCSP: true });
test.describe.configure({ mode: "serial" });

const H = { "X-Taskiem-Request": "1" };
const PASSWORD = "correct horse battery";

async function signIn(page: Page) {
  await page.goto("/login?with=email");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      document.documentElement.dataset.theme = t;
    }, scheme);
    await page.waitForTimeout(300);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

// What the editor saves: the fixture, as made by pressing and picking.
export const fixture = {
  page: "page/v1",
  name: "deliveries-today",
  title: "Deliveries today",
  icon: "tables",
  filters: [{ id: "dates", type: "dates", column: "offered_at", label: "When" }],
  blocks: [
    { id: "total", type: "figure", label: "Owed to riders", value: "sum", of: "owed", source: { records: "deliveries" }, naira: true },
    {
      id: "list",
      type: "list",
      title: "Today's deliveries",
      source: { order: [{ column: "offered_at", desc: true }], uses: ["dates"], records: "deliveries" },
      item: { title: "=row.order + ' · ' + row.rider", sub: "=row.customer", state: { text: "=row.status" } },
      // Opening a row: its card, as the editor starts it (eleven lines).
      detail: {
        title: "=row.order + ' · ' + row.rider",
        lines: [
          ["Order", "order"],
          ["Customer", "customer"],
          ["Phone", "phone"],
          ["Rider", "rider"],
          ["Status", "status"],
          ["Delivery area", "zone"],
          ["Rider's fee", "fee"],
          ["Owed to the rider", "owed"],
          ["Paid at the door", "paid_how"],
          ["Offered", "offered_at"],
          ["Delivered", "delivered_at"],
        ].map(([label, c]) => ({ label, value: `=row.${c}` })),
      },
      actions: [
        {
          label: "Confirm",
          when: "=row.status == 'delivered' && row.confirmed_at == ''",
          signal: { workflow: "Confirm deliveries", name: "confirm", to: "=row.id" },
        },
      ],
      status: { workflow: "Confirm deliveries", to: "=row.id" },
    },
    { id: "table", type: "table", title: "Customers (Taskiem's)", source: { records: "customers" }, columns: [{ column: "customer" }, { column: "owed", naira: true }] },
    { id: "table_2", type: "table", title: "Staff (Taskiem's)", source: { records: "staff" }, columns: [{ column: "name" }, { column: "roles" }] },
  ],
};

test("the deliveries page is made from the editor alone, in plain words, and publishes", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await signIn(page);
  // A workflow that waits for each delivery's Confirm, which a page may send.
  const def = {
    schema: "wd/v1",
    id: "wf_e2edeliveryok",
    version: 1,
    name: "Confirm deliveries",
    trigger: { type: "manual" },
    inputs: { schema: { type: "object", properties: { delivery: { type: "string" } } } },
    steps: [{ id: "wait", type: "signal", config: { event: "confirm", correlation: "=trigger.body.delivery", pages: true } }],
  };
  const made = await page.request.post("/v1/workflows", { headers: H, data: { name: "Confirm deliveries", definition: def } });
  expect(made.status(), await made.text()).toBe(201);
  const wf = ((await made.json()) as { id: string }).id;
  const pub = await page.request.post(`/v1/workflows/${wf}/versions/1/publish`, { headers: H });
  expect(pub.status(), await pub.text()).toBe(200);

  // A new page with nothing on it yet.
  await page.goto("/pages");
  await page.getByRole("button", { name: "New page" }).click();
  await page.getByLabel("What should it show?").selectOption({ label: "Nothing yet: I will add each part" });
  await page.getByLabel("Its name").fill("Deliveries today");
  await page.getByRole("button", { name: "Make the page" }).click();
  await expect(page).toHaveURL(/\/pages\/deliveries-today\/edit$/);
  await expect(page.getByRole("heading", { level: 1, name: "Change Deliveries today" })).toBeVisible();

  const addPart = async (kind: string, from?: string) => {
    await page.getByRole("button", { name: "Add a part" }).click();
    const dialog = page.getByRole("dialog", { name: "Add a part" });
    await dialog.getByRole("radio", { name: new RegExp(`^${kind} ·`) }).check();
    if (from) await dialog.getByLabel(/From which table/).selectOption({ label: from });
    await dialog.getByRole("button", { name: "Add" }).click();
  };
  const form = () => page.locator(".pg-part-form").last();

  // What is owed to riders, in naira.
  await addPart("A total", "Deliveries (Taskiem's)");
  await form().getByLabel("Label").fill("Owed to riders");
  await form().getByLabel("Shows").selectOption({ label: "Owed to the rider added up" });
  await form().getByText("It is money (shown in naira)").click();
  await page.getByRole("button", { name: "Done", exact: false }).first().click();

  // Today's deliveries, each with Confirm and its run.
  await addPart("A list", "Deliveries (Taskiem's)");
  const list = form();
  await list.getByLabel("Title", { exact: true }).fill("Today's deliveries");
  await list.getByLabel("Each row's title").selectOption({ label: "Order" });
  await list.getByLabel("Then, after a dot").selectOption({ label: "Rider" });
  await list.getByLabel("The line under it").selectOption({ label: "Customer" });
  await list.getByLabel("Its state, at the end").selectOption({ label: "Status" });
  await list.getByLabel("In order of").selectOption({ label: "Offered, newest or largest first" });
  await list.getByRole("button", { name: "Add a button" }).click();
  const button = list.getByTestId("editor-button");
  await button.getByLabel("It says").fill("Confirm");
  await button.getByLabel("Pressing it").selectOption({ label: "Tells a run something, after a Yes" });
  await expect(button.getByLabel("Which workflow")).toHaveValue("Confirm deliveries");
  await expect(button.getByLabel("Tells it")).toHaveValue("confirm");
  await expect(button.getByLabel("Which run")).toHaveValue("to");
  await button.getByLabel("The row is known by").selectOption({ label: "Its number" });
  await button.getByRole("button", { name: "Add a condition for Confirm" }).click();
  await button.getByLabel("When 1: column").selectOption({ label: "Status" });
  await button.getByLabel("When 1: words").fill("delivered");
  await button.getByRole("button", { name: "Add a condition for Confirm" }).click();
  await button.getByLabel("When 2: column").selectOption({ label: "Confirmed" });
  await button.getByLabel("When 2: how").selectOption({ label: "is empty" });
  const run = list.locator("fieldset", { hasText: "Where each row's run is" });
  await run.getByLabel("A run of").selectOption({ label: "Confirm deliveries" });
  await run.getByLabel("The row is known by").selectOption({ label: "Its number" });
  await page.getByRole("button", { name: "Done", exact: false }).first().click();

  // The customers, and the staff.
  await addPart("A table", "Customers (Taskiem's)");
  const customers = form();
  for (const c of ["Phone", "Email", "Invoices", "Added"]) {
    const box = customers.locator("fieldset", { hasText: "Columns it shows" }).getByRole("checkbox", { name: c, exact: true });
    if (await box.isChecked()) await box.uncheck();
  }
  await customers.locator("fieldset", { hasText: "Columns it shows" }).getByRole("checkbox", { name: "Owed", exact: true }).check();
  await customers.getByText("Owed is money (shown in naira)").click();
  await page.getByRole("button", { name: "Done", exact: false }).first().click();
  await addPart("A table", "Staff (Taskiem's)");
  const staff = form();
  await staff.locator("fieldset", { hasText: "Columns it shows" }).getByRole("checkbox", { name: "Email", exact: true }).uncheck();
  await staff.locator("fieldset", { hasText: "Columns it shows" }).getByRole("checkbox", { name: "Roles", exact: true }).check();
  await page.getByRole("button", { name: "Done", exact: false }).first().click();

  // The heading the page started with goes; dates at the top.
  await page.getByRole("button", { name: "Remove Deliveries today" }).click();
  await page.getByRole("button", { name: "Dates" }).click();

  // No technical word anywhere the owner looks.
  const words = await page.locator("main").innerText();
  for (const w of [/\bblock\b/i, /\bsource\b/i, /\bschema\b/i, /\bquery\b/i, /\bexpression\b/i, /\bCEL\b/, /=row\./, /\bsignal\b/i, /\bJSON\b/]) expect(words).not.toMatch(w);
  await axeBoth(page, "/pages/deliveries-today/edit (G2 parts)");
  // Saved as the fixture, with no problems; then published.
  const saved = page.waitForRequest((r) => r.method() === "PUT" && r.url().endsWith("/v1/pages/deliveries-today/draft"));
  await page.getByRole("button", { name: "Save draft" }).click();
  expect((await saved).postDataJSON()).toEqual({ document: fixture });
  await expect(page.getByText("Draft saved")).toBeVisible();
  await expect(page.locator(".pg-problem")).toHaveCount(0);
  await page.getByRole("button", { name: "Publish" }).click();
  const dialog = page.getByRole("dialog", { name: "Publish Deliveries today" });
  await expect(dialog.getByText("Put these right first:")).toHaveCount(0);
  await dialog.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByTestId("page-state")).toHaveText("Published.");

  // The editor at a phone's width: nothing scrolls sideways.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(200);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

test("a page sits as a tab under Linked businesses", async ({ page }) => {
  await signIn(page);
  const doc = {
    page: "page/v1",
    name: "trade-today",
    title: "Trade today",
    place: { area: "links" },
    blocks: [{ id: "h", type: "heading", title: "Orders to and from linked businesses" }],
  };
  const made = await page.request.post("/v1/pages", { headers: H, data: { document: doc } });
  expect(made.status(), await made.text()).toBe(201);
  expect((await page.request.post("/v1/pages/trade-today/publish", { headers: H })).status()).toBe(200);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/settings/links");
  const tabs = page.getByRole("navigation", { name: "Linked businesses" });
  await expect(tabs.getByRole("link", { name: "Linked businesses" })).toBeVisible();
  await expect(tabs.getByTestId("area-page-tab")).toHaveText("Trade today");
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await axeBoth(page, "/settings/links (tabs)");
  await tabs.getByTestId("area-page-tab").click();
  await expect(page).toHaveURL(/\/pages\/trade-today$/);
  await expect(page.getByText("Orders to and from linked businesses")).toBeVisible();
});
