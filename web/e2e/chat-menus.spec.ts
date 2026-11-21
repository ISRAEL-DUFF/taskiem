import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Menus in the Taskiem chat (docs/spec/chat-menus.md, M1): help shows the
// topics as tiles; a tile turns the card into that topic's menu, five rows
// at most and "All N" for the rest; a row that asks starts its questions,
// and the card folds; a newer menu folds the older; something Taskiem did
// not understand shows the topics too. Axe in both themes, at 390 and
// 360 px, nothing wider than the screen.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Menu Kitchen ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@menu.test`,
      name: "Ada Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

const pane = (p: Page) => p.getByTestId("taskiem-pane");
const menus = (p: Page) => pane(p).getByTestId("taskiem-menu");
const send = async (p: Page, text: string) => {
  await pane(p).locator("#taskiem-text").fill(text);
  await pane(p).locator("#taskiem-text").press("Enter");
};

for (const width of [390, 360])
  test(`menus in the Taskiem chat, ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await signUp(page);
    await page.goto("/chat?with=taskiem");

    // help: the topics, as tiles.
    await send(page, "help");
    const topics = menus(page).last();
    await expect(topics).toHaveAttribute("data-topic", "");
    await expect(topics.getByRole("group", { name: "Topics" }).getByRole("button", { name: "Staff and tasks" })).toBeVisible();
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      const vs = await axe(page);
      expect(vs, describe(`topics (${scheme})`, vs)).toEqual([]);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);

    // A tile: that topic's menu, in place.
    await topics.getByRole("button", { name: "Staff and tasks" }).click();
    await expect(topics).toHaveAttribute("data-topic", "staff");
    await expect(topics.getByRole("button", { name: /Give someone a task/ })).toBeVisible();
    // "All topics" turns it back.
    await topics.getByRole("button", { name: "All topics" }).click();
    await expect(topics).toHaveAttribute("data-topic", "");

    // A topic with more than five: five rows, and All N in a sheet.
    await topics.getByRole("button", { name: "Products" }).click();
    const rows = topics.locator(".tk-menu-row");
    const n = await rows.count();
    expect(n).toBeLessThanOrEqual(5);

    // help staff: a newer menu; the older folds.
    await send(page, "help staff");
    await expect(menus(page)).toHaveCount(2);
    await expect(menus(page).first()).toHaveAttribute("data-folded", "1");
    const staff = menus(page).last();
    await expect(staff).toHaveAttribute("data-topic", "staff");

    // A row that asks: its first question; the card folds to its line.
    await staff.getByRole("button", { name: /Give someone a task/ }).click();
    await expect(pane(page).getByText("Who is it for? Their name as on the team.", { exact: true })).toBeVisible();
    await expect(menus(page).last()).toHaveAttribute("data-folded", "1");
    await expect(menus(page).last()).toContainText("Give someone a task");
    await send(page, "cancel");

    // Not understood: the short answer, and the topics.
    await send(page, "zzq blorp");
    await expect(pane(page).getByText("Sorry, I did not understand that.", { exact: true }).last()).toBeVisible();
    await expect(menus(page).last()).toHaveAttribute("data-topic", "");
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
  });
