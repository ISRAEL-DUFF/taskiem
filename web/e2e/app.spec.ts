import { expect, test, type Page } from "@playwright/test";

// The console on a phone, like an app (docs/guide/on-your-phone.md):
// installable, tabs on one line, page descriptions folded away, and an
// approval decided from Home in a confirm sheet.
test.use({ viewport: { width: 390, height: 844 } });

async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

test("the console is installable", async ({ page }) => {
  await page.goto("/login");
  const href = await page.locator('link[rel="manifest"]').getAttribute("href");
  expect(href).toBe("/manifest.json");
  const res = await page.request.get(href as string);
  expect(res.ok()).toBeTruthy();
  const m = (await res.json()) as { name: string; display: string; start_url: string; icons: { src: string; sizes: string; purpose: string }[] };
  expect(m.name).toBe("Taskiem");
  expect(m.display).toBe("standalone");
  expect(m.start_url).toBe("/home");
  // A 192 and a 512 icon for each purpose, every one an image the server has.
  for (const purpose of ["any", "maskable"]) {
    expect(m.icons.filter((i) => i.purpose === purpose).map((i) => i.sizes).sort()).toEqual(["192x192", "512x512"]);
  }
  for (const icon of m.icons) {
    const r = await page.request.get(icon.src);
    expect(r.ok(), icon.src).toBeTruthy();
    expect(r.headers()["content-type"], icon.src).toBe("image/png");
  }
});

test("the tab bar, tabs on one line and folded descriptions", async ({ page }) => {
  await signIn(page);
  const quick = page.getByRole("navigation", { name: "Quick" });
  await expect(quick.getByRole("link")).toHaveText(["Home", "Money", "Orders", "Workflows"]);
  await expect(quick.getByRole("button", { name: "Open menu" })).toBeVisible();

  // Money's tabs stay on one row and scroll inside it; the page does not.
  await quick.getByRole("link", { name: "Money" }).click();
  const tabs = page.getByRole("navigation", { name: "Money" });
  await expect(tabs).toBeVisible();
  const rows = await tabs.evaluate((n) => new Set(Array.from(n.children).map((c) => Math.round(c.getBoundingClientRect().top))).size);
  expect(rows).toBe(1);
  expect(await tabs.evaluate((n) => n.scrollWidth > n.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);

  // The open tab is scrolled into view.
  await tabs.getByRole("link", { name: "Fees" }).click();
  await expect(page).toHaveURL(/\/money\/fees$/);
  const fees = page.getByRole("navigation", { name: "Money" }).getByRole("link", { name: "Fees" });
  const box = await page.getByRole("navigation", { name: "Money" }).boundingBox();
  const tab = await fees.boundingBox();
  expect(tab && box && tab.x >= box.x - 1 && tab.x + tab.width <= box.x + box.width + 1).toBe(true);

  // A page's description is behind "About this page".
  await quick.getByRole("link", { name: "Workflows" }).click();
  const about = page.getByRole("button", { name: "About this page" });
  const desc = page.locator(".page-desc");
  await expect(desc).toBeHidden();
  await about.click();
  await expect(about).toHaveAttribute("aria-expanded", "true");
  await expect(desc).toBeVisible();
});

test("an approval is decided from Home in a confirm sheet", async ({ page }) => {
  await signIn(page);
  const api = async (method: string, path: string, data?: unknown) => {
    const r = await page.request.fetch(path, { method, data, headers: { "X-Taskiem-Request": "1" } });
    expect(r.ok(), `${method} ${path}: ${r.status()} ${await r.text()}`).toBeTruthy();
    return (await r.json().catch(() => ({}))) as Record<string, unknown>;
  };
  const wf = await api("POST", "/v1/workflows", {
    name: "Pay a supplier",
    definition: {
      schema: "wd/v1", id: "wf_paySupplierPhone", version: 1, name: "Pay a supplier", trigger: { type: "manual" },
      steps: [{ id: "ok", name: "Pay Kunle Supplies", type: "approval", config: { role: "owner", subject: { amount: "=trigger.body.amount", to: "Kunle Supplies" } } }],
    },
  });
  await api("POST", `/v1/workflows/${String(wf.id)}/versions/1/publish`);
  const run = await api("POST", `/v1/workflows/${String(wf.id)}/runs`, { input: { amount: 4500000 } });

  await page.goto("/home");
  const needs = page.getByRole("region", { name: "Needs you" });
  await needs.getByRole("button", { name: "Approve Pay Kunle Supplies" }).click();
  const sheet = page.getByRole("dialog", { name: "Approve" });
  await expect(sheet).toBeVisible();
  // The sheet shows what is being approved: the decision records it.
  await expect(sheet).toContainText("Kunle Supplies");
  await sheet.getByRole("button", { name: "Confirm" }).click();
  await expect(sheet).toHaveCount(0);
  await expect(needs.getByRole("button", { name: "Approve Pay Kunle Supplies" })).toHaveCount(0);
  await expect.poll(async () => ((await api("GET", `/v1/runs/${String(run.run_id)}`)).run as { status: string }).status).toBe("completed");
});
