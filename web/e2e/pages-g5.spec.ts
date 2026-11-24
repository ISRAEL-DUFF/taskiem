import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Pages G5 (build-plan-gaps §11, docs/pages.md): starters, Describe it, and
// saved things of one kind as a part's source. The e2e server has no
// Xenibase and no model, so making a starter's tables and a drafted page
// (install, the build, make) are stand-ins of the server's own shapes;
// TestPageStartersInstallAndWork and TestDescribeItMakesAWorkingPage make
// them for real. The starters' list is the real server's, and so is the
// list of saved things, made in the editor and drawn on the page: saved
// things need no Xenibase. At a phone's width, axe in both themes.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });
test.describe.configure({ mode: "serial" });

const H = { "X-Taskiem-Request": "1" };

async function signIn(page: Page) {
  await page.goto("/login?with=email");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

async function check(page: Page, where: string) {
  const found: string[] = [];
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await page.waitForTimeout(150);
    const vs = await axe(page);
    if (vs.length) found.push(describe(`${where} (${scheme})`, vs));
  }
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  expect(found, found.join("\n\n")).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth), where).toBeLessThanOrEqual(390);
}

const made = {
  page: "tickets",
  title: "Tickets",
  tables: [{ name: "tickets", title: "Tickets", asked: "tickets", changed: false }],
  tools: [
    { command: "new-ticket", asked: "new-ticket", changed: false, name: "New ticket", workflow: "00000000-0000-7000-8000-000000000001", state: "published" },
    { command: "move-ticket-2", asked: "move-ticket", changed: true, name: "Move a ticket", workflow: "00000000-0000-7000-8000-000000000002", state: "published" },
  ],
  published: true,
  problems: [],
};

test("a starter says what it brings, and is made with its table and tools", async ({ page }) => {
  await signIn(page);
  let sent: unknown;
  await page.route("**/v1/page-starters/*/install", async (r) => {
    sent = r.request().postDataJSON();
    await r.fulfill({ status: 201, json: made });
  });
  await page.goto("/pages");
  await page.getByRole("button", { name: "From a starter" }).click();
  const dialog = page.getByRole("dialog", { name: "Start from a starter" });
  await expect(dialog.getByTestId("page-starter")).toHaveCount(7);
  for (const s of ["Tickets desk", "Expenses this month", "Leave", "Stock count", "Deliveries today", "Waiting list", "Service log"]) {
    await expect(dialog.getByRole("radio", { name: new RegExp(`^${s} ·`) })).toBeVisible();
  }
  await dialog.getByRole("radio", { name: /^Tickets desk ·/ }).check();
  const brings = dialog.getByLabel("What Tickets desk brings");
  await expect(brings).toContainText("The page has figures, buttons and a board.");
  await expect(brings).toContainText("The table Tickets: Title, Customer, Email, Priority, Status");
  await expect(brings).toContainText("/move-ticket (Move a ticket): changes a row of Tickets: Status");
  await dialog.getByRole("radio", { name: /^Expenses this month ·/ }).check();
  await expect(dialog.getByLabel("What Expenses this month brings")).toContainText("a chart");
  await check(page, "starters");
  await dialog.getByRole("radio", { name: /^Tickets desk ·/ }).check();
  await dialog.getByRole("button", { name: "Add Tickets desk" }).click();
  await expect(dialog.getByRole("status")).toContainText("/move-ticket is taken, so it is called with /move-ticket-2.");
  await expect(dialog.getByRole("status")).toContainText("Tickets is ready, with its tools.");
  expect(sent).toEqual({ publish: true });
  await expect(dialog.getByRole("link", { name: "Open Tickets" })).toHaveAttribute("href", "/pages/tickets");
  await check(page, "a starter made");
});

test("Describe it drafts a page, names what it would make, and makes it", async ({ page }) => {
  await signIn(page);
  const id = "00000000-0000-7000-8000-0000000000d5";
  let reads = 0;
  let goal = "";
  await page.route("**/v1/page-builds", async (r) => {
    goal = (r.request().postDataJSON() as { goal: string }).goal;
    await r.fulfill({ status: 202, json: { id, status: "running" } });
  });
  await page.route(`**/v1/page-builds/${id}`, (r) => {
    reads++;
    const base = { id, stage: "done", error: null, goal, created_at: "2026-11-24T10:00:00Z", finished_at: null, page_name: null };
    if (reads === 1) return r.fulfill({ json: { ...base, status: "running", stage: "correct 1", proposal: null } });
    return r.fulfill({
      json: {
        ...base,
        status: "proposed",
        proposal: { summary: "Open tickets: how many are open and waiting, the open ones as a list, and New ticket to add one.", problems: [], rounds: 2 },
        review: {
          title: "Open tickets",
          parts: ["figure", "figure", "buttons", "list"],
          makes: ["The table Tickets: Title, Customer, Status", "/new-ticket (New ticket): adds a row to Tickets, asking for Title and Customer"],
          uses: [],
          shows: ["Tickets: Title, Customer"],
        },
      },
    });
  });
  let made2 = false;
  await page.route(`**/v1/page-builds/${id}/make`, async (r) => {
    made2 = true;
    await r.fulfill({ status: 201, json: { ...made, page: "open-tickets", title: "Open tickets", tools: [made.tools[0]] } });
  });
  await page.goto("/pages");
  await page.getByRole("button", { name: "Describe it" }).click();
  const dialog = page.getByRole("dialog", { name: "Describe it" });
  await dialog.getByLabel("What should the page show?").fill("A page of open tickets with how many are waiting, and a button to add one");
  await dialog.getByRole("button", { name: "Draft the page" }).click();
  await expect(dialog.getByText("Putting the draft right…")).toBeVisible();
  const draft = dialog.getByLabel("The draft");
  await expect(draft).toContainText("Open tickets");
  await expect(draft).toContainText("It has figures, buttons and a list.");
  await expect(draft).toContainText("It will make");
  await expect(draft).toContainText("/new-ticket (New ticket): adds a row to Tickets, asking for Title and Customer");
  expect(made2).toBe(false);
  expect(goal).toContain("open tickets");
  await check(page, "Describe it, a draft");
  await draft.getByRole("button", { name: "Make it" }).click();
  await expect(dialog.getByRole("status")).toContainText("Open tickets is ready, with its tools.");
  expect(made2).toBe(true);
  await expect(dialog.getByRole("link", { name: "Open Open tickets" })).toHaveAttribute("href", "/pages/open-tickets");
});

test("a list of saved things, made in the editor, in the order their date comes", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page);
  const kinds = (await (await page.request.get("/v1/saved-kinds", { headers: H })).json()) as { kinds: { id: string; name: string }[] };
  let gen = kinds.kinds.find((k) => k.name === "Generator");
  if (!gen) {
    const r = await page.request.post("/v1/saved-kinds", {
      headers: H,
      data: { name: "Generator", fields: [{ label: "Size", type: "text" }, { label: "Next service", type: "date" }, { label: "Serial", type: "text", sensitive: true }] },
    });
    expect(r.ok(), await r.text()).toBeTruthy();
    gen = ((await r.json()) as { kind: { id: string; name: string } }).kind;
    const day = (n: number) => new Date(Date.now() + n * 86_400_000).toISOString().slice(0, 10);
    for (const [name, title, next] of [
      ["yard-gen", "Yard gen", day(3)],
      ["office-gen", "Office gen", day(-2)],
    ]) {
      const s = await page.request.post("/v1/saved", {
        headers: H,
        data: { name, title, kind: "custom", kind_id: gen.id, values: { size: "60kVA", next_service: next, serial: "SN-11223344" } },
      });
      expect(s.ok(), await s.text()).toBeTruthy();
    }
  }
  const doc = { page: "page/v1", name: "generators", title: "Generators", blocks: [{ id: "head", type: "heading", title: "Generators" }] };
  const c = await page.request.post("/v1/pages", { headers: H, data: { document: doc } });
  expect([201, 400]).toContain(c.status());

  await page.goto("/pages/generators/edit");
  await page.getByRole("button", { name: "Add a part" }).click();
  const add = page.getByRole("dialog", { name: "Add a part" });
  await add.getByRole("radio", { name: /^A list of saved things ·/ }).check();
  await add.getByRole("button", { name: "Add" }).click();
  const form = page.locator(".pg-part-form").last();
  await expect(form.getByLabel("Which saved things")).toHaveValue("Generator");
  await expect(form.getByLabel("In the order a date comes")).toHaveValue("next_service");
  // A private field is never offered.
  await expect(form.getByLabel("In the order a date comes").locator("option", { hasText: "Serial" })).toHaveCount(0);
  await page.getByRole("button", { name: "Done", exact: false }).first().click();
  await expect(page.getByText("List of saved Generator, in the order next service comes")).toBeVisible();
  await check(page, "the editor, a list of saved things");
  const saved = page.waitForResponse((r) => r.request().method() === "PUT" && r.url().endsWith("/v1/pages/generators/draft"));
  await page.getByRole("button", { name: "Save draft" }).click();
  const body = (await (await saved).json()) as { problems: unknown[] };
  expect(body.problems).toEqual([]);
  const pub = await page.request.post("/v1/pages/generators/publish", { headers: H });
  expect(pub.ok(), await pub.text()).toBeTruthy();

  await page.goto("/pages/generators");
  const rows = page.locator("main").getByText(/Office gen|Yard gen/);
  await expect(rows.first()).toHaveText("Office gen");
  await expect(page.getByText("2 days ago")).toBeVisible();
  await expect(page.getByText("In 3 days")).toBeVisible();
  expect(await page.locator("main").innerText()).not.toContain("11223344");
  await check(page, "a page of saved things");
});
