import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";
import { PUBLIC, familyLink, familySchool } from "./familyschool";

// Automated accessibility checks (axe: WCAG 2.1 A/AA and best practices)
// on the console's pages, in the light and the dark theme, plus keyboard
// checks for the dialogs and the narrow-screen menu. axe is injected into
// the page, which the console's Content Security Policy would refuse.
test.use({ bypassCSP: true });

const PAGES = [
  "/home",
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
  "/link-chat",
  "/dashboard",
  "/templates",
  "/settings/keys",
  "/settings/notifications",
  "/settings/payments",
  "/settings/billing",
  "/storefront",
  "/storefront/look",
  "/storefront/orders",
  "/storefront/bookings",
  "/storefront/riders",
  "/storefront/riders/pay",
  "/storefront/deliveries",
  "/storefront/stock",
  "/storefront/suppliers",
  "/storefront/price-lists",
  "/chat",
  "/settings/links",
  "/bills",
  "/trade-orders",
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
  "/schools/accounts",
  "/schools/payments",
  "/schools/receipts",
  "/schools/debtors",
  "/schools/clearance",
  "/schools/gate",
  "/settings/schools",
];

async function signIn(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", {
      level: 1,
      name: /^Good (morning|afternoon|evening)/,
    }),
  ).toBeVisible();
}

/** A planned term of a session of its own (made once, whichever theme runs first): its id. */
async function plannedTerm(page: Page): Promise<string> {
  const h = { headers: { "X-Taskiem-Request": "1" } };
  type Sessions = {
    sessions: { id: string; label: string; terms: { id: string }[] }[];
  };
  const find = async () =>
    (
      (await (
        await page.request.get("/v1/schools/sessions")
      ).json()) as Sessions
    ).sessions.find((s) => s.label === "2031/2032");
  let s = await find();
  if (!s) {
    await page.request.post("/v1/schools/sessions", {
      ...h,
      data: { label: "2031/2032" },
    });
    s = await find();
  }
  if (s && s.terms.length === 0) {
    await page.request.post(`/v1/schools/sessions/${s.id}/terms`, {
      ...h,
      data: {
        name: "First term",
        starts_on: "2031-09-15",
        ends_on: "2031-12-12",
        fees_due_on: "2031-09-08",
      },
    });
    s = await find();
  }
  const id = s?.terms[0]?.id;
  expect(id, "the accessibility term").toBeTruthy();
  return id ?? "";
}

/** A run of a workflow that waits (made once, whichever theme runs first): its id. */
async function aRun(page: Page): Promise<string> {
  const h = { headers: { "X-Taskiem-Request": "1" } };
  type List = { workflows: { id: string; name: string }[] };
  let wf = (
    (await (await page.request.get("/v1/workflows")).json()) as List
  ).workflows.find((w) => w.name === "Accessible run");
  if (!wf) {
    const definition = {
      schema: "wd/v1",
      id: "wf_a11yrun",
      version: 1,
      name: "Accessible run",
      trigger: { type: "manual" },
      steps: [
        { id: "change", type: "transform", config: { output: { ok: true } } },
        {
          id: "wait",
          type: "wait",
          needs: ["change"],
          config: { duration: "1h" },
        },
      ],
    };
    const made = (await (
      await page.request.post("/v1/workflows", {
        ...h,
        data: { name: "Accessible run", definition },
      })
    ).json()) as { id: string };
    expect(
      (
        await page.request.post(
          `/v1/workflows/${made.id}/versions/1/publish`,
          h,
        )
      ).ok(),
      "publish the accessibility workflow",
    ).toBeTruthy();
    wf = { id: made.id, name: "Accessible run" };
  }
  const run = (await (
    await page.request.post(`/v1/workflows/${wf.id}/runs`, {
      ...h,
      data: { input: {} },
    })
  ).json()) as { run_id?: string };
  expect(run.run_id, "the accessibility run").toBeTruthy();
  return run.run_id ?? "";
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
    await expect(
      page.getByRole("heading", { name: "Sign in to Taskiem" }),
    ).toBeVisible();
    const found: string[] = [];
    const check = async (where: string) => {
      const vs = await axe(page);
      if (vs.length > 0) found.push(describe(where, vs));
    };
    await check("/login");
    // The connect page a chat's link opens, signed out (connect_links.go):
    // a link that has ended, and how a consent from one ended.
    // And how Add to Slack ended, when Slack did not open the bot.
    for (const path of [
      `/c/${"A".repeat(32)}`,
      "/c/done?service=Google&error=You+did+not+approve",
      "/c/done?service=Google&connected=1",
      "/slack/added",
      "/slack/added?error=You+did+not+add+Taskiem+to+Slack.",
      "/confirm-email",
    ]) {
      await page.goto(path);
      await expect(page.locator("main h1").first()).toBeVisible();
      await check(path);
    }
    await signIn(page);
    for (const path of PAGES) {
      await open(page, path);
      await check(path);
    }
    // A term's fees and invoices: a term of its own, not started, so the
    // school's current term (schools.spec.ts) is left alone.
    const term = await plannedTerm(page);
    for (const path of [
      `/schools/terms/${term}/fees`,
      `/schools/terms/${term}/invoices`,
    ]) {
      await open(page, path);
      await check(path.replace(term, ":id"));
    }
    // A run, live, on its canvas and its timeline.
    const run = await aRun(page);
    await open(page, `/runs/${run}`);
    await expect(
      page.getByLabel("Run canvas").getByTestId("node-wait"),
    ).toHaveAttribute("data-status", "waiting", { timeout: 30_000 });
    await check("/runs/:id");
    await page.getByRole("tab", { name: "Timeline" }).click();
    await check("/runs/:id (timeline)");
    // A dialog, and the workflow editor.
    await open(page, "/workflows");
    await page.getByRole("link", { name: "New workflow" }).click();
    await expect(
      page.getByRole("heading", { level: 1, name: "When should it run?" }),
    ).toBeVisible();
    await check("/workflows/new");
    await page.getByLabel("Its name").fill(`Accessible ${scheme} flow`);
    await page.getByRole("button", { name: /^A payment comes in/ }).click();
    await expect(page.getByTestId("node-trigger")).toBeVisible();
    await check("/workflows/:id (empty)");
    // The picker, a step's window, and the canvas with an If after a test.
    await page.getByRole("button", { name: "Add the first step" }).click();
    await expect(
      page.getByRole("dialog", { name: "What happens next?" }),
    ).toBeVisible();
    await check("/workflows/:id (what happens next)");
    await page
      .getByRole("dialog", { name: "What happens next?" })
      .getByLabel("Search")
      .fill("change the data");
    await page.getByRole("button", { name: /^Change the data/ }).click();
    await expect(page.getByTestId("step-window")).toBeVisible();
    await check("/workflows/:id (a step's window)");
    await page
      .getByTestId("step-window")
      .getByRole("button", { name: "Done" })
      .click();
    await page
      .getByRole("button", { name: "Add a step after Change the data" })
      .click();
    await page
      .getByRole("dialog", { name: "What happens next?" })
      .getByLabel("Search")
      .fill("if");
    await page.getByRole("button", { name: /^If Go one way/ }).click();
    await page
      .getByTestId("step-window")
      .getByRole("button", { name: "Done" })
      .click();
    await page
      .getByRole("button", { name: "Add a step to this path" })
      .first()
      .click();
    await page
      .getByRole("dialog", { name: "What happens next?" })
      .getByLabel("Search")
      .fill("wait");
    await page.getByRole("button", { name: /^Wait An hour/ }).click();
    await page
      .getByTestId("step-window")
      .getByRole("button", { name: "Done" })
      .click();
    await page.getByRole("button", { name: "Test workflow" }).click();
    await expect(page.getByText("Tested with sample data")).toBeVisible();
    await check("/workflows/:id");
    expect(found, found.join("\n\n")).toEqual([]);
  });
}

// The family page on the public host (docs/schools.md "The family page")
// is drawn in the school's theme: every theme passes, as does the page an
// expired or unknown link answers.
test("the family page passes axe in every public theme", async ({
  page,
  browser,
}) => {
  test.setTimeout(240_000);
  const parent = await (await browser.newContext()).newPage();
  for (const theme of ["clean", "bold", "editorial"]) {
    await familySchool(page, `Axe ${theme} Academy`, theme);
    const link = await familyLink(page);
    for (const path of [
      link,
      `${link}?done=paid`,
      `${link}?err=amount&for=paid`,
    ]) {
      await parent.goto(path);
      await expect(
        parent.getByRole("heading", { level: 1, name: "School fees" }),
      ).toBeVisible();
      await expect(parent.locator(`link[href*="theme-${theme}."]`)).toHaveCount(
        1,
      );
      const vs = await axe(parent);
      expect(vs, describe(`${path} (${theme})`, vs)).toEqual([]);
    }
    await parent.goto(`${PUBLIC}/s/${"B".repeat(32)}`);
    await expect(
      parent.getByRole("heading", {
        level: 1,
        name: "Ask the school for a new link",
      }),
    ).toBeVisible();
    const vs = await axe(parent);
    expect(vs, describe(`an unknown link (${theme})`, vs)).toEqual([]);
  }
});

test("dialogs keep focus inside and give it back when they close", async ({
  page,
}) => {
  await signIn(page);
  await page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name: "Workflows" })
    .click();
  await page.getByRole("link", { name: "Forms", exact: true }).click();
  const opener = page
    .getByRole("button", { name: /^(New form|Make your first form)$/ })
    .first();
  await opener.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  // Focus starts on the first field and Tab stays inside.
  await expect(dialog.getByLabel("What the form is called")).toBeFocused();
  for (let i = 0; i < 6; i++) {
    await page.keyboard.press("Tab");
    expect(
      await dialog.evaluate((d) => d.contains(document.activeElement)),
    ).toBe(true);
  }
  await page.keyboard.press("Shift+Tab");
  expect(await dialog.evaluate((d) => d.contains(document.activeElement))).toBe(
    true,
  );
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
});

test.describe("on a phone-sized screen", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the menu opens as a drawer, works by keyboard, and pages fit the width", async ({
    page,
  }) => {
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
      expect(
        await nav.evaluate((n) => n.contains(document.activeElement)),
      ).toBe(true);
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
    await expect(
      page.getByRole("heading", { name: "Activity", exact: true }),
    ).toBeVisible();
    await expect(nav).toBeHidden();

    // No page scrolls sideways; wide tables scroll inside themselves.
    for (const path of [
      "/workflows",
      "/workflows/new",
      "/runs",
      "/connections",
      "/audit",
      "/members",
      "/alerts",
      "/settings",
      "/dashboard",
      "/storefront",
      "/storefront/orders",
      "/storefront/bookings",
      "/storefront/riders",
      "/storefront/deliveries",
      "/storefront/stock",
      "/storefront/suppliers",
      "/storefront/price-lists",
      "/chat",
      "/settings/links",
      "/trade-orders",
      "/schools",
      "/schools/classes",
    ]) {
      await open(page, path);
      const wide = await page.evaluate(
        () => document.documentElement.scrollWidth,
      );
      expect(wide, `${path} is ${wide}px wide`).toBeLessThanOrEqual(390);
      vs = await axe(page);
      if (vs.length > 0) found.push(describe(`${path} (390px)`, vs));
    }
    expect(found, found.join("\n\n")).toEqual([]);
  });
});
