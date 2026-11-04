import { execFileSync } from "node:child_process";
import { expect, test, type Page } from "@playwright/test";

// The run inspector: personal data revealed on a live run is fetched once
// (each reveal is audited) and again only when asked; a step parked with
// an unknown outcome is resolved from its card.

const H = { "X-Taskiem-Request": "1" };

function sql(text: string): string {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  return execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-tAqc", text], { encoding: "utf8" }).trim();
}

async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

/** A published workflow that waits an hour, then changes the data; a run of it, waiting. */
async function waitingRun(page: Page, id: string, input: Record<string, unknown> = {}): Promise<string> {
  const api = async (path: string, data?: unknown) => {
    const r = await page.request.post(path, { headers: H, data });
    expect(r.ok(), `POST ${path}: ${r.status()} ${await r.text()}`).toBeTruthy();
    return (await r.json()) as Record<string, unknown>;
  };
  const def = {
    schema: "wd/v1", id, version: 1, name: id, trigger: { type: "manual" },
    inputs: { schema: { type: "object", properties: { phone: { type: "string", "x-pii": "phone" } } } },
    steps: [
      { id: "w", type: "wait", config: { duration: "1h" } },
      { id: "after", type: "transform", needs: ["w"], config: { output: "done" } },
    ],
  };
  const wf = await api("/v1/workflows", { name: id, definition: def });
  await api(`/v1/workflows/${String(wf.id)}/versions/1/publish`);
  const run = await api(`/v1/workflows/${String(wf.id)}/runs`, { input });
  return String(run.run_id);
}

test("revealing personal data on a live run fetches it once, and again on Refresh", async ({ page }) => {
  await signIn(page);
  const run = await waitingRun(page, "wf_reveal", { phone: "08031234567" });
  const reveals: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes(`/v1/runs/${run}?reveal=true`)) reveals.push(r.url());
  });
  await page.goto(`/runs/${run}`);
  await expect(page.getByTestId("step-w").or(page.getByTestId("node-w")).first()).toBeVisible();
  await expect(page.getByText("live", { exact: true })).toBeVisible();

  await page.getByLabel("Reveal personal data").check();
  // Nothing polls while revealed: one fetch over several seconds.
  await page.waitForTimeout(5_000);
  expect(reveals).toHaveLength(1);
  await expect(page.getByTestId("reveal-paused")).toBeVisible();
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect.poll(() => reveals.length).toBe(2);

  // Each fetch was one reveal, audited once for this person and run.
  const audit = (await (await page.request.get("/v1/audit?action=pii.reveal&limit=500")).json()) as { entries: { target: string }[] };
  expect(audit.entries.filter((e) => e.target === `run:${run}`)).toHaveLength(1);

  // Sealed again, the run is live again.
  await page.getByLabel("Reveal personal data").uncheck();
  await expect(page.getByTestId("reveal-paused")).toHaveCount(0);
  await expect(page.getByText("live", { exact: true })).toBeVisible();
});

test("a parked step is resolved from its card", async ({ page }) => {
  await signIn(page);
  const run = await waitingRun(page, "wf_resolve");
  // Park the waiting step as an unknown outcome would (the e2e server
  // reaches no provider that could leave one).
  sql(`SELECT taskiem_append_event('${run}', 'StepFailed', 'w',
         (SELECT max(attempt) FROM run_events WHERE run_id = '${run}' AND step_id = 'w'),
         '{"error":{"kind":"unknown_outcome","message":"the provider did not answer","next":"park","maybe_applied":true}}', 'worker')`);
  sql(`UPDATE runs SET status = 'needs_reconciliation' WHERE id = '${run}'`);

  await page.goto(`/runs/${run}`);
  await page.getByRole("tab", { name: "Timeline" }).click();
  const card = page.getByTestId("step-w");
  await card.getByRole("button", { name: "Resolve" }).click();
  const dialog = page.getByRole("dialog", { name: "Resolve w" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Resolve" })).toBeDisabled();
  await dialog.getByLabel("Note (required, audited)").fill("Provider dashboard shows it done at 10:42");
  await dialog.getByRole("button", { name: "Resolve" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator("h1 + .badge").first()).toHaveText("completed", { timeout: 30_000 });
  await expect(card.getByRole("button", { name: "Resolve" })).toHaveCount(0);

  const audit = (await (await page.request.get("/v1/audit?action=run.resolve&limit=500")).json()) as { entries: { target: string }[] };
  expect(audit.entries.map((e) => e.target)).toContain(`${run}/w`);
});
