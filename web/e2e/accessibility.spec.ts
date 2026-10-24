import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Automated accessibility checks (axe: WCAG 2.1 A/AA and best practices)
// on the console's pages, in the light and the dark theme, plus keyboard
// checks for the dialogs and the narrow-screen menu. axe is injected into
// the page, which the console's Content Security Policy would refuse.
test.use({ bypassCSP: true });

const PAGES = [
  "/workflows",
  "/runs",
  "/approvals",
  "/connections",
  "/catalogue",
  "/settings",
  "/policies",
  "/alerts",
  "/audit",
  "/reports",
  "/members",
  "/account",
  "/dashboard",
  "/templates",
  "/settings/keys",
  "/settings/notifications",
  "/settings/billing",
  "/storefront",
  "/storefront/look",
  "/storefront/orders",
  "/storefront/bookings",
  "/invoices",
  "/invoices/match",
  "/invoices/settings",
  "/money",
  "/money?period=custom&from=2026-10-01&to=2026-10-31",
  "/money/leaks",
  "/money/statements",
  "/money/entries",
  "/money/fees",
  "/schools",
  "/schools/families",
  "/schools/classes",
  "/schools/import",
  "/schools/students/new",
  "/settings/schools",
];

async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

/** A planned term of a session of its own (made once, whichever theme runs first): its id. */
async function plannedTerm(page: Page): Promise<string> {
  const h = { headers: { "X-Taskiem-Request": "1" } };
  type Sessions = { sessions: { id: string; label: string; terms: { id: string }[] }[] };
  const find = async () => ((await (await page.request.get("/v1/schools/sessions")).json()) as Sessions).sessions.find((s) => s.label === "2031/2032");
  let s = await find();
  if (!s) {
    await page.request.post("/v1/schools/sessions", { ...h, data: { label: "2031/2032" } });
    s = await find();
  }
  if (s && s.terms.length === 0) {
    await page.request.post(`/v1/schools/sessions/${s.id}/terms`, { ...h, data: { name: "First term", starts_on: "2031-09-15", ends_on: "2031-12-12", fees_due_on: "2031-09-08" } });
    s = await find();
  }
  const id = s?.terms[0]?.id;
  expect(id, "the accessibility term").toBeTruthy();
  return id ?? "";
}

/** Opens a page and waits until it has drawn its data. */
async function open(page: Page, path: string) {
  await page.goto(path);
  await expect(page.locator("main h1").first()).toBeVisible();
  await expect(page.locator(".skeleton")).toHaveCount(0);
}

for (const scheme of ["light", "dark"] as const) {
  test(`pages pass axe in the ${scheme} theme`, async ({ page }) => {
    test.setTimeout(180_000);
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "Sign in to Taskiem" })).toBeVisible();
    const found: string[] = [];
    const check = async (where: string) => {
      const vs = await axe(page);
      if (vs.length > 0) found.push(describe(where, vs));
    };
    await check("/login");
    await signIn(page);
    for (const path of PAGES) {
      await open(page, path);
      await check(path);
    }
    // A term's fees and invoices: a term of its own, not started, so the
    // school's current term (schools.spec.ts) is left alone.
    const term = await plannedTerm(page);
    for (const path of [`/schools/terms/${term}/fees`, `/schools/terms/${term}/invoices`]) {
      await open(page, path);
      await check(path.replace(term, ":id"));
    }
    // A dialog, and the workflow editor.
    await open(page, "/workflows");
    await page.getByRole("button", { name: "New workflow" }).click();
    await expect(page.getByRole("dialog")).toBeVisible();
    await check("/workflows (new workflow dialog)");
    await page.getByLabel("Name").fill(`Accessible ${scheme} flow`);
    await page.getByRole("button", { name: "Create" }).click();
    await expect(page.getByTestId("node-start")).toBeVisible();
    await check("/workflows/:id");
    expect(found, found.join("\n\n")).toEqual([]);
  });
}

test("dialogs keep focus inside and give it back when they close", async ({ page }) => {
  await signIn(page);
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Workflows" }).click();
  const opener = page.getByRole("button", { name: "New workflow" });
  await opener.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  // Focus starts on the first field and Tab stays inside.
  await expect(dialog.getByLabel("Name")).toBeFocused();
  for (let i = 0; i < 6; i++) {
    await page.keyboard.press("Tab");
    expect(await dialog.evaluate((d) => d.contains(document.activeElement))).toBe(true);
  }
  await page.keyboard.press("Shift+Tab");
  expect(await dialog.evaluate((d) => d.contains(document.activeElement))).toBe(true);
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
});

test.describe("on a phone-sized screen", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the menu opens as a drawer, works by keyboard, and pages fit the width", async ({ page }) => {
    await signIn(page);
    const menu = page.getByRole("button", { name: "Open menu" });
    const nav = page.getByRole("navigation", { name: "Main" });
    await expect(menu).toBeVisible();
    await expect(nav).toBeHidden();
    await expect(menu).toHaveAttribute("aria-expanded", "false");

    // Opens by keyboard; Tab stays in the drawer; Escape closes it and focus returns.
    await menu.focus();
    await page.keyboard.press("Enter");
    await expect(nav).toBeVisible();
    await expect(menu).toHaveAttribute("aria-expanded", "true");
    for (let i = 0; i < 25; i++) {
      await page.keyboard.press("Tab");
      expect(await nav.evaluate((n) => n.contains(document.activeElement))).toBe(true);
    }
    const found: string[] = [];
    let vs = await axe(page);
    if (vs.length > 0) found.push(describe("drawer open", vs));
    await page.keyboard.press("Escape");
    await expect(nav).toBeHidden();
    await expect(menu).toBeFocused();

    // Following a link closes the drawer.
    await menu.click();
    await nav.getByRole("link", { name: "Activity" }).click();
    await expect(page.getByRole("heading", { name: "Activity", exact: true })).toBeVisible();
    await expect(nav).toBeHidden();

    // No page scrolls sideways; wide tables scroll inside themselves.
    for (const path of ["/workflows", "/runs", "/connections", "/audit", "/members", "/alerts", "/settings", "/dashboard", "/storefront", "/storefront/orders", "/storefront/bookings", "/schools", "/schools/classes"]) {
      await open(page, path);
      const wide = await page.evaluate(() => document.documentElement.scrollWidth);
      expect(wide, `${path} is ${wide}px wide`).toBeLessThanOrEqual(390);
      vs = await axe(page);
      if (vs.length > 0) found.push(describe(`${path} (390px)`, vs));
    }
    expect(found, found.join("\n\n")).toEqual([]);
  });
});
