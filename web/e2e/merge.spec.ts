import { expect, test } from "@playwright/test";

// Two people edit the same step from the same version: the second save
// reports the conflict, and the editor keeps the choice made.
test("conflicting edits are resolved in the editor", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();

  const api = async (method: string, path: string, data?: unknown) => {
    const r = await page.request.fetch(path, { method, data, headers: { "X-Taskiem-Request": "1" } });
    expect(r.ok(), `${method} ${path}: ${r.status()} ${await r.text()}`).toBeTruthy();
    return (await r.json()) as Record<string, unknown>;
  };
  const def = (output: string) => ({
    schema: "wd/v1", id: "wf_merge", version: 1, name: "Merge", trigger: { type: "manual" },
    steps: [{ id: "start", type: "transform", config: { output } }],
  });
  const wf = await api("POST", "/v1/workflows", { name: "Merge flow", definition: def("first") });

  await page.goto(`/workflows/${String(wf.id)}`);
  await expect(page.getByTestId("node-start")).toBeVisible();
  // Meanwhile someone else saves version 2 from version 1.
  await api("POST", `/v1/workflows/${String(wf.id)}/versions`, { definition: def("theirs"), parent_digest: wf.digest });

  // This editor changes the same step from version 1, and saves.
  await page.getByRole("tab", { name: "JSON" }).click();
  const json = page.locator("textarea.mono");
  await json.fill((await json.inputValue()).replace('"first"', '"mine"'));
  await page.getByRole("button", { name: "Save draft" }).click();
  const dialog = page.getByRole("dialog", { name: "Resolve conflicting edits" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId("conflict-steps/start")).toContainText('"theirs"');
  await dialog.getByRole("radio", { name: "Yours" }).check();
  await dialog.getByRole("button", { name: "Save merged version" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByLabel("Version")).toHaveValue("3");

  const v3 = await api("GET", `/v1/workflows/${String(wf.id)}/versions/3`);
  expect(JSON.stringify(v3.definition)).toContain('"output":"mine"');
});
