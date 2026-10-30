import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Business links, L0 (docs/spec/business-links.md §5.1): Dangote lets
// other businesses find it; Ada finds it on the Chat page and asks to
// link, as a supplier; Dangote accepts under Asks to link; both see the
// other linked, and Ada ends it from Linked businesses. At a phone's width
// nothing scrolls sideways, and both pages pass axe in both themes.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${(tenant.split(" ")[0] ?? "x").toLowerCase()}-${Date.now()}@links.test`, name: tenant, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
}

async function narrow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
}

test("two businesses find each other and link", async ({ browser }) => {
  // Letters, not digits: a search with six digits in a row is never run.
  const stamp = String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
  const dangoteName = `Dangote Depot ${stamp}`;
  const dangote = await (await browser.newContext()).newPage();
  await signUp(dangote, dangoteName);
  const ada = await (await browser.newContext()).newPage();
  await signUp(ada, `Ada Kitchen ${stamp}`);

  // Dangote turns on being found.
  await dangote.goto("/settings/links");
  await expect(dangote.getByRole("heading", { name: "Linked businesses", exact: true })).toBeVisible();
  const findable = dangote.getByLabel("Let other businesses find us");
  await expect(findable).not.toBeChecked();
  await findable.click();
  await expect(findable).toBeChecked();
  await expect(dangote.getByTestId("link-findable")).toContainText("On: businesses find you");
  await axeBoth(dangote, "/settings/links");
  await narrow(dangote);

  // Ada finds it and asks, as a supplier.
  await ada.goto("/chat");
  await expect(ada.getByText("No linked businesses yet")).toBeVisible();
  await axeBoth(ada, "/chat, empty");
  await ada.getByTestId("link-search").fill(`dangote depot ${stamp}`);
  const found = ada.getByTestId("link-found").filter({ hasText: dangoteName });
  await expect(found).toBeVisible();
  await narrow(ada);
  await found.getByRole("button", { name: `Request link with ${dangoteName}` }).click();
  const sheet = ada.getByRole("dialog");
  await sheet.getByLabel("They supply us").check();
  await axeBoth(ada, "the request sheet");
  await sheet.getByRole("button", { name: "Send request" }).click();
  await expect(sheet).toBeHidden();
  await expect(ada.getByTestId("link-waiting").filter({ hasText: dangoteName })).toContainText("Asked");

  // A phone number is never searched: one row that sends a request.
  await ada.getByTestId("link-search").fill("0803 123 4567");
  await expect(ada.getByTestId("link-blind")).toContainText("Send a link request to 0803…4567");
  await ada.getByTestId("link-search").fill("");

  // Dangote accepts.
  await dangote.goto("/chat");
  const asked = dangote.getByTestId("link-asked");
  await expect(asked).toContainText("They buy from you");
  await axeBoth(dangote, "/chat, a request to answer");
  await narrow(dangote);
  await asked.getByRole("button", { name: /^Accept/ }).click();
  await expect(dangote.getByTestId("link-linked")).toContainText("Linked");

  // Ada sees it linked, and ends it.
  await ada.goto("/chat");
  await expect(ada.getByTestId("link-linked").filter({ hasText: dangoteName })).toContainText("They supply you");
  await ada.goto("/settings/links");
  const row = ada.getByTestId("link-row").filter({ hasText: dangoteName });
  await expect(row).toHaveAttribute("data-state", "active");
  ada.once("dialog", (d) => void d.accept());
  await row.getByRole("button", { name: `End link ${dangoteName}` }).click();
  await expect(row).toHaveAttribute("data-state", "ended");
  await axeBoth(ada, "/settings/links, a link ended");
});

test("a link code opens a request to a business that is not found by search", async ({ browser }) => {
  const stamp = String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
  const cherryName = `Cherry Blocks ${stamp}`;
  const cherry = await (await browser.newContext()).newPage();
  await signUp(cherry, cherryName);
  await cherry.goto("/settings/links");
  const url = (await cherry.getByTestId("link-code-url").textContent())?.trim() ?? "";
  expect(url).toMatch(/\/l\/[a-z2-9]{8}$/);
  await expect(cherry.getByTestId("link-code").getByRole("img")).toBeVisible();
  const path = new URL(url).pathname;

  // Signed out, the code offers to sign in or sign up.
  const stranger = await (await browser.newContext()).newPage();
  await stranger.goto(path);
  await expect(stranger.getByRole("heading", { name: "Link with a business on Taskiem" })).toBeVisible();
  await expect(stranger.getByRole("link", { name: "Sign up your business" })).toBeVisible();
  await axeBoth(stranger, "a link code, signed out");

  // Signed in, it names the business and asks.
  const ada = await (await browser.newContext()).newPage();
  await signUp(ada, `Ada Kitchen ${stamp}`);
  await ada.goto(path);
  await expect(ada.getByRole("heading", { level: 1, name: cherryName })).toBeVisible();
  await axeBoth(ada, "a link code, signed in");
  await narrow(ada);
  await ada.getByRole("button", { name: "Request link" }).click();
  const sheet = ada.getByRole("dialog");
  await sheet.getByLabel("They supply us").check();
  await sheet.getByRole("button", { name: "Send request" }).click();
  await expect(ada).toHaveURL(/\/chat$/);
  await expect(ada.getByTestId("link-waiting").filter({ hasText: cherryName })).toContainText("Asked");
});
