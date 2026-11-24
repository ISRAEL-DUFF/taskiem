import { execFileSync } from "node:child_process";
import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Pages G4 (build-plan-gaps §11, docs/pages.md), done when: a ticket
// dragged from Open to Doing runs its tool, and expenses show by week. The
// e2e server has no Xenibase, so the page is drawn from a stand-in for
// what the server works out (view) and its press (act); the tool's form,
// its Yes and its answer are the real sheet's over stand-ins for start,
// confirm and the answer. The API's own test moves the card for real and
// works the weeks out (api/pages_g4_test.go). By drag, and by the keyboard;
// at a phone's width, axe in both themes. Then the editor adds a board and
// a chart in plain words, saved and checked by the real server.
test.use({ bypassCSP: true });
test.describe.configure({ mode: "serial" });

const H = { "X-Taskiem-Request": "1" };
const PASSWORD = "correct horse battery";
const phone = { width: 390, height: 844 };
const JOBS = "0192e2e0-0000-7000-8000-0000000000c4";

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, `/${process.env.TASKIEM_E2E_DB ?? "taskiem_e2e"}$1`), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

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

const noSideways = async (page: Page) => expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);

async function publish(page: Page, definition: Record<string, unknown>) {
  const r = await page.request.post("/v1/workflows", { headers: H, data: { name: definition.name, definition } });
  expect(r.ok(), await r.text()).toBeTruthy();
  const id = ((await r.json()) as { id: string }).id;
  const p = await page.request.post(`/v1/workflows/${id}/versions/1/publish`, { headers: H });
  expect(p.ok(), await p.text()).toBeTruthy();
}

// The tool a card moved opens: a ticket and the column it goes to.
const moveTool = {
  schema: "wd/v1",
  id: "wf_e2emoveticket",
  version: 1,
  name: "Move ticket",
  trigger: { type: "manual" },
  inputs: { schema: { type: "object", required: ["ref", "status"], properties: { ref: { type: "string", title: "Ticket" }, status: { type: "string", title: "Status" } } } },
  outputs: [{ name: "status", type: "string", title: "Status", description: "Where it went", value: "=steps.move.output.status" }],
  tool: { name: "Move ticket", command: "move-ticket", about: "Move a ticket on", answer: { type: "text", text: "='Moved to ' + outputs.status" } },
  steps: [{ id: "move", type: "transform", config: { output: { status: "=trigger.body.status" } } }],
};

const card = (id: string, title: string, sub: string) => ({ id, title, sub, detail: { title, lines: [{ label: "Customer", value: sub }] } });
const weeks = [
  { label: "3 Nov", long: "Week of 3 November", value: 350000, shown: "₦3,500", share: 0.5833, tick: true },
  { label: "10 Nov", long: "Week of 10 November", value: 0, shown: "₦0", share: 0, tick: true },
  { label: "17 Nov", long: "Week of 17 November", value: 450000, shown: "₦4,500", share: 0.75, tick: true },
  { label: "24 Nov", long: "Week of 24 November", value: 120000, shown: "₦1,200", share: 0.2, tick: true },
];

// What the server draws: the tickets board (its move this person's to use)
// and the expenses by week, every figure worked out.
function drawn(moved: string[]) {
  const all = [card("1", "Printer jam", "Bayo"), card("2", "No ink", "Ada"), card("3", "Paper", "Musa")];
  const at = (id: string) => (moved.includes(id) ? (id === "2" ? "done" : "doing") : id === "3" ? "done" : "open");
  const lane = (value: string, label: string) => {
    const rows = all.filter((r) => at(r.id) === value);
    return { value, label, count: rows.length, rows, more: null };
  };
  return {
    page: { name: "desk", title: "Desk", icon: "tables", about: "", roles: [], pinned: false, published: true, has_draft: false, version: 1, updated_at: "2026-11-24T10:00:00Z" },
    draft: false,
    can_edit: false,
    drawn_at: `2026-11-24T10:00:0${moved.length}Z`,
    filters: [],
    blocks: [
      {
        id: "board",
        type: "board",
        title: "Tickets",
        size: "full",
        empty: "Nothing here",
        count: 3,
        has_detail: true,
        lanes: [lane("open", "Open"), lane("doing", "Doing"), lane("done", "Done")],
        move: { label: "Move", tool: "move-ticket" },
      },
      {
        id: "spend",
        type: "chart",
        title: "Spent by week",
        size: "full",
        naira: true,
        empty: "Nothing to show yet",
        kind: "bars",
        points: weeks,
        ticks: [
          { value: 0, shown: "₦0", share: 0 },
          { value: 200000, shown: "₦2k", share: 0.3333 },
          { value: 400000, shown: "₦4k", share: 0.6667 },
          { value: 600000, shown: "₦6k", share: 1 },
        ],
        zero: 0,
        label_head: "Week",
        value_head: "Amount",
        summary: "Highest ₦4,500 (Week of 17 November); latest ₦1,200 (Week of 24 November), 4 in all.",
      },
    ],
  };
}

test("a ticket dragged from Open to Doing runs its tool; one moved by the keyboard too; expenses by week", async ({ browser }) => {
  test.setTimeout(120_000);
  const page = await (await browser.newContext({ viewport: phone, reducedMotion: "reduce" })).newPage();
  await signIn(page);
  await publish(page, moveTool);
  const moved: string[] = [];
  const acts: Record<string, unknown>[] = [];
  const starts: Record<string, unknown>[] = [];
  const confirms: Record<string, unknown>[] = [];
  const BUTTON = "0192e2e0-0000-7000-8000-0000000000c1";
  const RUN = "0192e2e0-0000-7000-8000-0000000000c2";
  let to = "";
  await page.route("**/v1/pages/desk/view*", (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(drawn(moved)) }));
  await page.route("**/v1/pages/desk/live*", (r) => r.fulfill({ status: 404, body: "" }));
  await page.route("**/v1/pages/desk/act*", (r) => {
    const body = r.request().postDataJSON() as { row: string; to: string };
    acts.push(body);
    to = body.to;
    return r.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ kind: "tool", tool: { button: BUTTON, command: "move-ticket", name: "Move ticket", inputs: { ref: body.row, status: body.to }, once: false } }),
    });
  });
  await page.route("**/v1/tools/move-ticket/start", (r) => {
    starts.push(r.request().postDataJSON() as Record<string, unknown>);
    return r.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ care: "yes", confirm: "0192e2e0-0000-7000-8000-0000000000c3", expires_at: "2030-01-01T00:00:00Z", values: [{ name: "status", label: "Status", value: to }] }),
    });
  });
  await page.route("**/v1/tools/move-ticket/confirm", (r) => {
    const body = r.request().postDataJSON() as { input: { ref: string } };
    confirms.push(body);
    moved.push(body.input.ref);
    return r.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ run_id: RUN }) });
  });
  await page.route(`**/v1/runs/${RUN}/answer`, (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ status: "completed", type: "text", text: `Moved to ${to}` }) }));

  await page.goto("/pages/desk");
  const board = page.getByTestId("page-board");
  const lane = (v: string) => board.locator(`[data-lane="${v}"]`);
  await expect(lane("open").getByTestId("page-card")).toHaveText([/Printer jam/, /No ink/]);
  await expect(lane("doing")).toContainText("Nothing here");
  await expect(lane("done").getByTestId("page-card")).toHaveText([/Paper/]);

  // Expenses by week: bars drawn from the server's places, the figures in a
  // table behind them, and the chart's words for a screen reader.
  const chart = page.getByTestId("page-chart");
  await expect(chart.getByRole("img", { name: /^Spent by week\. Highest ₦4,500 \(Week of 17 November\)/ })).toBeVisible();
  await expect(chart.locator(".pg-chart-bar")).toHaveCount(4);
  await expect(chart.locator(".pg-chart-label")).toHaveText(["3 Nov", "10 Nov", "17 Nov", "24 Nov"]);
  await chart.getByText("The figures").click();
  await expect(chart.getByRole("row")).toHaveText(["WeekAmount", "Week of 3 November₦3,500", "Week of 10 November₦0", "Week of 17 November₦4,500", "Week of 24 November₦1,200"]);
  // A value on hover.
  await chart.locator(".pg-chart-hit").nth(2).hover();
  await expect(chart.locator(".pg-chart-tip")).toHaveText("₦4,500Week of 17 November");
  await noSideways(page);
  await axeBoth(page, "/pages/desk (a board and a chart)");
  await page.evaluate(() => (document.documentElement.dataset.theme = "light"));

  // Printer jam dragged from Open to Doing: the server is asked to move
  // that card there; the move tool's form opens filled in, asks Yes, runs.
  await lane("open").getByTestId("page-card").filter({ hasText: "Printer jam" }).dragTo(lane("doing"));
  await expect.poll(() => acts.length).toBe(1);
  expect(acts[0]).toEqual({ block: "board", row: "1", action: 0, to: "doing" });
  const sheet = page.getByTestId("made-tool-sheet");
  await expect(sheet.getByRole("heading", { name: "Move ticket" })).toBeVisible();
  await expect(sheet.getByLabel("Ticket", { exact: true })).toHaveValue("1");
  await expect(sheet.getByLabel("Status", { exact: true })).toHaveValue("doing");
  await sheet.getByRole("button", { name: "Go" }).click();
  await expect(sheet.getByText("Check what will run, then say yes.")).toBeVisible();
  expect(starts[0]).toMatchObject({ input: { ref: "1", status: "doing" }, button: BUTTON });
  await sheet.getByRole("button", { name: "Yes", exact: true }).click();
  await expect(sheet.getByTestId("tool-answer")).toContainText("Moved to doing");
  expect(confirms).toHaveLength(1);
  await sheet.getByRole("button", { name: "Close" }).click();
  // The page read again: Printer jam is in Doing.
  await expect(lane("doing").getByTestId("page-card")).toHaveText([/Printer jam/]);
  await expect(lane("open").getByTestId("page-card")).toHaveText([/No ink/]);

  // No ink moved by the keyboard alone: Move picks it up, the right arrow
  // twice chooses Done, Enter moves it; each step is said.
  const said = board.getByTestId("page-board-said");
  const handle = lane("open").getByRole("button", { name: "Move No ink" });
  await handle.focus();
  await page.keyboard.press("Enter");
  await expect(said).toHaveText("No ink picked up, in Open. Left and right arrows choose a column, Enter moves it there, Escape leaves it.");
  await expect(handle).toHaveAttribute("aria-pressed", "true");
  await page.keyboard.press("ArrowRight");
  await expect(said).toHaveText("Move No ink to Doing?");
  await expect(lane("doing")).toHaveClass(/target/);
  await page.keyboard.press("ArrowRight");
  await expect(said).toHaveText("Move No ink to Done?");
  await page.keyboard.press("Escape");
  await expect(said).toHaveText("No ink left in Open.");
  expect(acts).toHaveLength(1);
  await handle.focus();
  await page.keyboard.press("Enter");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("Enter");
  await expect(said).toHaveText("Moving No ink to Done.");
  await expect.poll(() => acts.length).toBe(2);
  expect(acts[1]).toEqual({ block: "board", row: "2", action: 0, to: "done" });
  await expect(sheet.getByLabel("Status", { exact: true })).toHaveValue("done");
  await sheet.getByRole("button", { name: "Go" }).click();
  await sheet.getByRole("button", { name: "Yes", exact: true }).click();
  await expect(sheet.getByTestId("tool-answer")).toContainText("Moved to done");
  await sheet.getByRole("button", { name: "Close" }).click();
  await expect(lane("done").getByTestId("page-card")).toHaveText([/No ink/, /Paper/]);
  await noSideways(page);
  await axeBoth(page, "/pages/desk (after moving)");
});

test("a board and a chart are added in the editor, in plain words, and checked by the server", async ({ page }) => {
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 1280, height: 900 });
  await signIn(page);
  const tenant = sql("SELECT id FROM tenants WHERE name = 'E2E'");
  sql(`INSERT INTO data_tables (id, tenant_id, name, title, created_by) VALUES ('${JOBS}', '${tenant}', 'jobs', 'Jobs', 'e2e') ON CONFLICT DO NOTHING`);
  sql(
    `INSERT INTO data_table_columns (tenant_id, table_id, name, title, type, required, personal, position) VALUES ('${tenant}', '${JOBS}', 'title', 'Title', 'text', true, false, 0), ('${tenant}', '${JOBS}', 'status', 'Status', 'text', false, false, 1), ('${tenant}', '${JOBS}', 'amount_kobo', 'Amount', 'integer', false, false, 2), ('${tenant}', '${JOBS}', 'done_on', 'Done on', 'date', false, false, 3) ON CONFLICT DO NOTHING`,
  );
  // The move tool exists from the test before; a fresh run makes it.
  const tools = await (await page.request.get("/v1/page-choices", { headers: H })).json();
  if (!(tools as { tools: { command: string }[] }).tools.some((t) => t.command === "move-ticket")) await publish(page, moveTool);
  const doc = { page: "page/v1", name: "jobs-board", title: "Jobs board", blocks: [{ id: "head", type: "heading", title: "Jobs" }] };
  expect((await page.request.post("/v1/pages", { headers: H, data: { document: doc } })).status()).toBe(201);

  await page.goto("/pages/jobs-board/edit");
  const addPart = async (kind: string, from?: string) => {
    await page.getByRole("button", { name: "Add a part" }).click();
    const dialog = page.getByRole("dialog", { name: "Add a part" });
    await dialog.getByRole("radio", { name: new RegExp(`^${kind} ·`) }).check();
    if (from) {
      // Taskiem's own records are not offered for a board or a chart.
      await expect(dialog.getByLabel(/From which table/).locator("option", { hasText: "(Taskiem's)" })).toHaveCount(0);
      await dialog.getByLabel(/From which table/).selectOption({ label: from });
    }
    await dialog.getByRole("button", { name: "Add" }).click();
  };
  const form = () => page.locator(".pg-part-form").last();

  // A board over Jobs, in columns by Status, moving a card opening Move ticket.
  await addPart("A board", "Jobs");
  const b = form();
  await expect(b.getByLabel("In columns by")).toHaveValue("status");
  await expect(b.getByLabel("Column 1: its heading")).toHaveValue("Open");
  await b.getByLabel("Column 3: the value its cards hold").fill("finished");
  await b.getByLabel("Column 3: its heading").fill("Finished");
  await b.getByLabel("Opens which tool").selectOption({ label: "Move ticket" });
  await expect(b.getByRole("combobox", { name: "Ticket", exact: true })).toHaveValue("column");
  await expect(b.getByLabel("Ticket: which column")).toHaveValue("id");
  await expect(b.getByRole("combobox", { name: "Status", exact: true })).toHaveValue("to");
  await b.getByRole("checkbox", { name: "Amount", exact: true }).check();
  await page.getByRole("button", { name: "Done", exact: false }).first().click();

  // A chart of the amounts by week of Done on, as a line.
  await addPart("A chart", "Jobs");
  const c = form();
  await expect(c.getByLabel("Shows")).toHaveValue("amount_kobo");
  await c.getByLabel("For each").selectOption({ label: "week by done on" });
  await c.getByText("It is money (shown in naira)").click();
  await c.getByLabel("How many weeks, up to now").fill("6");
  await c.getByLabel("Drawn as").selectOption({ label: "A line" });
  await page.getByRole("button", { name: "Done", exact: false }).first().click();
  await expect(page.getByText("Board of Jobs in columns by Status: Open, Doing, Finished; moving a card opens /move-ticket")).toBeVisible();
  await expect(page.getByText("A line of Amount added up in Jobs, by week")).toBeVisible();

  const words = await page.locator("main").innerText();
  for (const w of [/\bblock\b/i, /\bsource\b/i, /\bschema\b/i, /\bquery\b/i, /\bexpression\b/i, /=row\./, /=to\b/, /\bJSON\b/]) expect(words).not.toMatch(w);
  await axeBoth(page, "/pages/jobs-board/edit (a board and a chart)");

  const saved = page.waitForRequest((r) => r.method() === "PUT" && r.url().endsWith("/v1/pages/jobs-board/draft"));
  await page.getByRole("button", { name: "Save draft" }).click();
  const sent = (await saved).postDataJSON() as { document: { blocks: Record<string, unknown>[] } };
  expect(sent.document.blocks[1]).toMatchObject({
    type: "board",
    source: { table: "jobs" },
    item: { title: "=row.title" },
    board: {
      column: "status",
      columns: [
        { value: "open", label: "Open" },
        { value: "doing", label: "Doing" },
        { value: "finished", label: "Finished" },
      ],
      move: { label: "Move", tool: "move-ticket", inputs: { ref: "=row.id", status: "=to" } },
    },
  });
  expect(sent.document.blocks[2]).toMatchObject({ type: "chart", naira: true, source: { table: "jobs" }, chart: { kind: "line", of: "amount_kobo", by: "done_on", period: "week", last: 6 } });
  await expect(page.getByText("Draft saved")).toBeVisible();
  await expect(page.locator(".pg-problem")).toHaveCount(0);
  // The server checked it: no problems to put right before publishing.
  const detail = (await (await page.request.get("/v1/pages/jobs-board", { headers: H })).json()) as { problems?: unknown[] };
  expect(detail.problems ?? []).toEqual([]);

  await page.setViewportSize(phone);
  await page.waitForTimeout(200);
  await noSideways(page);
});
