import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Made tools and saved things, the rest (build plan H6), on the real
// server, at a phone's width: a saved thing's page lists the runs that
// read it and a run's page the saved things it read; a tool's version
// says what changed in plain words; an invoice shows its customer's
// address and opens as a PDF. axe in both themes, nothing wider than the
// page.
test.use({ viewport: { width: 390, height: 844 }, bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

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

async function must<T>(page: Page, method: string, path: string, data?: unknown): Promise<T> {
  const r = await page.request.fetch(path, { method, data, headers: H });
  expect(r.ok(), `${method} ${path}: ${r.status()} ${await r.text()}`).toBeTruthy();
  return (await r.json().catch(() => ({}))) as T;
}

test("saved things and the runs that read them, a tool's changes in words, an invoice's address and PDF", async ({ page }) => {
  test.setTimeout(180_000);
  await page.goto("/login?with=email");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();

  // A saved text a workflow reads, and a run of it.
  const name = `intro-${letters()}`;
  const thing = await must<{ saved: { id: string } }>(page, "POST", "/v1/saved", { name, title: "Intro", kind: "text", details: { body: "Here are this week's prices." } });
  const key = name.replaceAll("-", "_");
  const def = {
    schema: "wd/v1",
    id: "wf_h6",
    version: 1,
    name: "Weekly note",
    trigger: { type: "manual" },
    steps: [{ id: "note", type: "transform", config: { output: { text: `=saved.${key}.body` } } }],
  };
  const wf = await must<{ id: string }>(page, "POST", "/v1/workflows", { name: `Weekly note ${letters()}`, definition: def });
  await must(page, "POST", `/v1/workflows/${wf.id}/versions/1/publish`);
  const run = await must<{ run_id: string }>(page, "POST", `/v1/workflows/${wf.id}/runs`, { input: {} });
  await expect.poll(async () => (await must<{ run: { status: string } }>(page, "GET", `/v1/runs/${run.run_id}`)).run.status, { timeout: 30_000 }).toBe("completed");

  // The run's page: what it read.
  await page.goto(`/runs/${run.run_id}`);
  await expect(page.getByTestId("run-saved")).toContainText(`@${name}`);
  await expect(page.getByTestId("run-saved")).toContainText("Read by step note");
  await check(page, "a run's saved things");

  // The saved thing's page: the runs that read it.
  await page.goto("/settings/saved");
  await page.getByRole("button", { name: `Change @${name}` }).click();
  const runs = page.getByTestId("saved-runs");
  await expect(runs).toContainText("Runs that read it");
  await expect(runs.getByRole("link")).toHaveCount(1);
  await expect(runs).toContainText("Read by step note");
  await check(page, "a saved thing's runs");
  await page.screenshot({ path: "test-results/h6-saved-runs.png" });
  void thing;

  // A tool's new version says what changed, in plain words.
  const tool = {
    ...def,
    id: "wf_h6tool",
    inputs: { schema: { type: "object", properties: {} } },
    outputs: [{ name: "text", type: "string", title: "Text", description: "The note", value: "=steps.note.output.text" }],
    tool: { name: "Weekly note", command: `note-${letters()}`.slice(0, 20), about: "This week's note", says: ["fuel"], answer: { type: "text", text: "=outputs.text" } },
  };
  const tw = await must<{ id: string }>(page, "POST", "/v1/workflows", { name: `Note tool ${letters()}`, definition: tool });
  await must(page, "POST", `/v1/workflows/${tw.id}/versions/1/publish`);
  await must(page, "POST", `/v1/workflows/${tw.id}/versions`, { definition: { ...tool, tool: { ...tool.tool, says: ["fuel", "diesel"] } } });
  await page.goto(`/workflows/${tw.id}?v=2`);
  const changes = page.getByTestId("version-changes");
  await changes.getByText(/What changed in version 2/).click();
  await expect(changes).toContainText("It now also says 'diesel'");
  await check(page, "a version's changes");

  // An invoice with its customer's address, and its PDF.
  const inv = await must<{ id: string; number: string }>(page, "POST", "/v1/invoices", {
    customer: { name: "Acme Ltd", email: "ap@acme.ng", address: "12 Marina Road, Lagos Island" },
    lines: [{ description: "The October job", quantity: 1, unit_kobo: 25_000_000 }],
  });
  await page.goto(`/invoices/${inv.id}`);
  await expect(page.getByTestId("invoice-customer-address")).toHaveText("••• Lagos Island");
  await page.getByRole("button", { name: "Show customer details" }).click();
  await expect(page.getByTestId("invoice-customer-address")).toHaveText("12 Marina Road, Lagos Island");
  await check(page, "an invoice's address");
  const pdf = await page.request.get(await page.getByTestId("invoice-pdf").getAttribute("href") ?? "");
  expect(pdf.status()).toBe(200);
  expect(pdf.headers()["content-type"]).toBe("application/pdf");
  expect((await pdf.body()).subarray(0, 5).toString()).toBe("%PDF-");
});
