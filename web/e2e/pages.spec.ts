import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Pages G1 (decision 0072, docs/pages.md), done when: an owner turns the
// tickets table into a page with a count and a list in under two minutes,
// and staff open it on a phone. The e2e server has no Xenibase, so the
// table is its record only (as tables.spec.ts's kept table), and the
// staff member's page is drawn from a stand-in for what the server works
// out (GET /v1/pages/tickets/view): the drawing itself is the API's
// (api/pages_test.go). axe is injected, which the CSP would refuse.
test.use({ bypassCSP: true });
test.describe.configure({ mode: "serial" });

const H = { "X-Taskiem-Request": "1" };
const PASSWORD = "correct horse battery";
const TICKETS = "0192e2e0-0000-7000-8000-0000000000a1";
const phone = { width: 390, height: 844 };

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

async function signIn(page: Page, email = "owner@e2e.test") {
  await page.goto("/login?with=email");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else document.documentElement.dataset.theme = "light";
    }, scheme);
    await page.waitForTimeout(300);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

const noSideways = async (page: Page) => expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);

// What the server draws for the staff member: the count, the open tickets
// grouped by status, each opening as a card with the phone masked.
const drawn = {
  page: { name: "tickets", title: "Tickets", icon: "tables", about: "", roles: [], pinned: true, published: true, has_draft: false, version: 1, updated_at: "2026-11-22T10:00:00Z" },
  draft: false,
  can_edit: false,
  drawn_at: "2026-11-22T10:00:00Z",
  filters: [{ id: "q", type: "search", label: "Search", value: "" }],
  blocks: [
    { id: "all", type: "figure", label: "All", value: 3, shown: "3", tone: "plain", lead: true, naira: false },
    { id: "this_week", type: "figure", label: "Added this week", value: 2, shown: "2", tone: "plain", lead: false, naira: false, change: { before: 1, words: "up 100% on last week", up: true, down: false } },
    {
      id: "rows",
      type: "list",
      title: "Tickets",
      size: "full",
      empty: "Nothing here yet",
      has_detail: true,
      count: 3,
      more: null,
      groups: [
        { key: "open", label: "Open" },
        { key: "done", label: "Done" },
      ],
      rows: [
        { id: "1", title: "Printer jam", sub: "Bayo", state: { text: "open", tone: "warn" }, group: "open", detail: { title: "Printer jam", lines: [{ label: "Customer", value: "Bayo" }, { label: "Phone", value: "+234 ••• ••• 5678" }] } },
        { id: "3", title: "Screen dark", sub: "Chi", state: { text: "open", tone: "warn" }, group: "open", detail: { title: "Screen dark", lines: [{ label: "Customer", value: "Chi" }] } },
        { id: "2", title: "No ink", sub: "Ada", state: { text: "done", tone: "good" }, group: "done", detail: { title: "No ink", lines: [{ label: "Customer", value: "Ada" }] } },
      ],
    },
  ],
};

async function staffMember(browser: Browser, owner: Page): Promise<Page> {
  sql(`UPDATE users SET email_verified_at = now() WHERE lower(email) = 'owner@e2e.test'`);
  const email = `support-${Math.random().toString(36).slice(2, 8)}@staff.test`;
  const added = await owner.request.post("/v1/members", { headers: H, data: { email, password: PASSWORD, roles: ["support"] } });
  expect(added.status(), await added.text()).toBe(201);
  const staff = await (await browser.newContext({ viewport: phone, reducedMotion: "reduce" })).newPage();
  const login = await staff.request.post("/v1/auth/login", { headers: H, data: { email, password: PASSWORD } });
  expect(login.status(), await login.text()).toBe(200);
  return staff;
}

test("an owner turns the tickets table into a page, and staff open it on a phone", async ({ page, browser }) => {
  const tenant = sql("SELECT id FROM tenants WHERE name = 'E2E'");
  sql(`DELETE FROM pages WHERE tenant_id = '${tenant}'`);
  sql(
    `INSERT INTO data_tables (id, tenant_id, name, title, created_by) VALUES ('${TICKETS}', '${tenant}', 'tickets', 'Tickets', 'e2e') ON CONFLICT DO NOTHING`,
  );
  sql(
    `INSERT INTO data_table_columns (tenant_id, table_id, name, title, type, required, personal, position) VALUES ('${tenant}', '${TICKETS}', 'title', 'Title', 'text', true, false, 0), ('${tenant}', '${TICKETS}', 'customer', 'Customer', 'text', false, false, 1), ('${tenant}', '${TICKETS}', 'status', 'Status', 'text', false, false, 2), ('${tenant}', '${TICKETS}', 'phone', 'Phone', 'phone', false, true, 3) ON CONFLICT DO NOTHING`,
  );
  await page.setViewportSize(phone);
  await signIn(page);
  const started = Date.now();
  // One tap on the table makes the page and opens its editor.
  await page.goto(`/tables/${TICKETS}`);
  await page.getByRole("button", { name: "Show as a page" }).click();
  await expect(page).toHaveURL(/\/pages\/tickets\/edit$/);
  await expect(page.getByRole("heading", { level: 1, name: "Change Tickets" })).toBeVisible();
  const parts = page.getByTestId("editor-part");
  await expect(parts).toHaveCount(3);
  await expect(parts.nth(0)).toContainText("How many in Tickets");
  await expect(parts.nth(2)).toContainText("List of Tickets, in groups by Status");
  // Publishing says who will be able to open it and what it shows.
  await page.getByRole("button", { name: "Publish", exact: true }).click();
  const sheet = page.getByRole("dialog", { name: "Publish Tickets" });
  await expect(sheet).toContainText("Everyone in the business will be able to open Tickets.");
  await expect(sheet).toContainText("It shows Tickets: Title, Customer, Status");
  await sheet.getByRole("button", { name: "Publish", exact: true }).click();
  await expect(page.getByTestId("page-state")).toHaveText("Published.");
  await page.getByLabel("Pin to the menu (up to five pages)").click();
  await expect(page.getByLabel("Pin to the menu (up to five pages)")).toBeChecked();
  expect(Date.now() - started).toBeLessThan(120_000);
  await noSideways(page);
  await axeBoth(page, "/pages/tickets/edit");

  // The editor's words: moving a part by the keyboard.
  await parts.nth(1).getByRole("button", { name: /^Move up/ }).click();
  await expect(parts.nth(0)).toContainText("this week");
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByTestId("page-state")).toContainText("changes not published");

  // Staff open it on a phone: in the list (pinned), then the page.
  const staff = await staffMember(browser, page);
  await staff.route("**/v1/pages/tickets/view*", (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(drawn) }));
  await staff.goto("/pages");
  await expect(staff.getByRole("heading", { level: 1, name: "Pages" })).toBeVisible();
  const tile = staff.getByTestId("page-tile");
  await expect(tile).toHaveCount(1);
  await expect(tile).toContainText("Pinned");
  await axeBoth(staff, "/pages (staff)");
  await tile.click();
  await expect(staff.getByRole("heading", { level: 1, name: "Tickets" })).toBeVisible();
  await expect(staff.getByRole("region", { name: "All" })).toContainText("3");
  await expect(staff.getByTestId("page-figure").filter({ hasText: "Added this week" })).toContainText("up 100% on last week");
  await expect(staff.getByRole("heading", { level: 3, name: /Open/ })).toBeVisible();
  await expect(staff.getByTestId("page-row")).toHaveCount(3);
  await expect(staff.getByRole("link", { name: "Change" })).toHaveCount(0);
  await noSideways(staff);
  await axeBoth(staff, "/pages/tickets");
  await staff.getByRole("button", { name: /Printer jam/ }).click();
  const card = staff.getByRole("dialog", { name: "Printer jam" });
  await expect(card.getByTestId("page-detail")).toContainText("+234 ••• ••• 5678");
  await axeBoth(staff, "/pages/tickets (a row's card)");
  await card.getByRole("button", { name: "Close" }).click();
  // A search is sent to the server as the page's filters.
  const asked = staff.waitForRequest((r) => r.url().includes("/v1/pages/tickets/view") && decodeURIComponent(r.url()).includes('"q":"ink"'));
  await staff.getByLabel("Search", { exact: true }).fill("ink");
  await staff.getByRole("button", { name: "Search: go" }).click();
  await asked;
});

// Pages' follow-ups (docs/spec/pages.md §18): the versions with Bring back,
// the page saved as a file and made again from it, and a table's columns
// offered for ordering only where the server says the viewer may.
test("an owner brings back a version, and saves a page as a file and makes it again", async ({ page }) => {
  await page.setViewportSize(phone);
  await signIn(page);
  await page.goto("/pages/tickets/edit");
  await expect(page.getByRole("heading", { level: 1, name: "Change Tickets" })).toBeVisible();
  // One version published; the draft moved a part since.
  const versions = page.getByTestId("page-version");
  await expect(versions).toHaveCount(1);
  await expect(versions.first()).toContainText("Version 1 (what people see now)");
  await expect(versions.first()).toContainText("First published, with 3 parts. Opened by everyone in the business.");
  await noSideways(page);
  await axeBoth(page, "/pages/tickets/edit (versions)");
  page.once("dialog", (d) => void d.accept());
  await versions.first().getByRole("button", { name: "Bring back version 1" }).click();
  await expect(page.getByText("Version 1 is your draft now")).toBeVisible();
  const parts = page.getByTestId("editor-part");
  await expect(parts.nth(0)).toContainText("How many in Tickets");

  // Saved as a file: the page's document, no rows.
  const saving = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save as a file" }).click();
  const file = await saving;
  expect(file.suggestedFilename()).toBe("tickets.page.json");
  const doc = JSON.parse(readFileSync(await file.path(), "utf8")) as { page: string; name: string };
  expect(doc.page).toBe("page/v1");
  await expect(page.getByTestId("page-exported")).toHaveText("Saved.");

  // Made again from the file: its name is taken, so tickets-2, a draft.
  await page.goto("/pages");
  await page.getByLabel("Make from a file").setInputFiles({ name: "tickets.page.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify(doc)) });
  await expect(page).toHaveURL(/\/pages\/tickets-2\/edit$/);
  await expect(page.getByTestId("page-imported")).toContainText("Another page was called tickets, so this one is tickets-2.");
  await expect(page.getByTestId("page-state")).toContainText("Not published yet");
  // A file that is no page is refused in plain words.
  await page.goto("/pages");
  await page.getByLabel("Make from a file").setInputFiles({ name: "notes.json", mimeType: "application/json", buffer: Buffer.from('{"hello":1}') });
  await expect(page.getByRole("alert")).toHaveText("This file is not a page saved from Taskiem.");
  await noSideways(page);
  await axeBoth(page, "/pages (a file refused)");
});

test("a table's columns are pressed to order it only where the viewer may", async ({ page }) => {
  // A computer's width: on a phone the table's rows are cards.
  await signIn(page);
  const part = {
    id: "people",
    type: "table",
    title: "People",
    size: "full",
    empty: "Nothing here yet",
    has_detail: false,
    count: 1,
    more: null,
    columns: [
      { column: "name", label: "Name", naira: false, sortable: false },
      { column: "team", label: "Team", naira: false, sortable: true },
    ],
    rows: [{ id: "1", cells: ["•••", "Desk"] }],
  };
  await page.route("**/v1/pages/tickets/view*", (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ...drawn, blocks: [part] }) }));
  await page.route("**/v1/pages/tickets/blocks/people*", (r) =>
    r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ...part, order: { column: "team", desc: false } }) }),
  );
  const ordered = page.waitForRequest((r) => r.url().includes("/v1/pages/tickets/blocks/people") && r.url().includes("order=team"));
  await page.goto("/pages/tickets");
  await expect(page.getByRole("columnheader", { name: "Name" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Name", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Team", exact: true }).click();
  await ordered;
  await expect(page.getByRole("columnheader", { name: "Team" })).toHaveAttribute("aria-sort", "ascending");
});
