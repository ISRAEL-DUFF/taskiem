import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Offline (docs/spec/offline.md): the app starts its service worker for
// everyone, and a page of the app opened with no network is a plain page that
// says so, not the browser's error; back online, the app opens again. The
// worker is the real one, from the built app. bypassCSP lets axe run on the
// offline page, whose own policy allows no script.
// serviceWorkers: the config blocks them for every other spec.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 }, serviceWorkers: "allow" });

async function workerInControl(page: Page) {
  await page.goto("/login");
  await page.evaluate(() => navigator.serviceWorker.ready);
  // The first page was loaded before the worker: it controls the next one.
  await page.reload();
  await expect.poll(() => page.evaluate(() => !!navigator.serviceWorker.controller)).toBe(true);
}

test("the worker starts for everyone, keeps nothing, and leaves pages online as they were", async ({ page }) => {
  await workerInControl(page);
  const scope = await page.evaluate(async () => (await navigator.serviceWorker.getRegistration("/"))?.scope);
  expect(scope).toBe(new URL("/", page.url()).href);
  // Nothing is kept: every page and every answer comes from the server.
  expect(await page.evaluate(() => caches.keys())).toEqual([]);
  // The app itself, through the worker, as without it.
  const r = await page.goto("/chat");
  expect(r?.status()).toBe(200);
  await expect(page.locator("#root")).not.toBeEmpty();
  // The API is not the worker's: an answer comes straight from the server.
  const me = await page.evaluate(async () => (await fetch("/v1/me", { headers: { "X-Taskiem-Request": "1" } })).status);
  expect(me).toBe(401);
});

test("offline, a page of the app says so in plain words, and opens again once back online", async ({ page, context }) => {
  await workerInControl(page);
  await context.setOffline(true);
  const r = await page.goto("/chat?with=x").catch(() => null);
  expect(r?.status()).toBe(503);
  await expect(page.getByRole("heading", { name: "You’re offline" })).toBeVisible();
  await expect(page.getByText("It will open by itself when you’re back online.")).toBeVisible();
  // Nothing scrolls sideways on a phone.
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
    const vs = await axe(page);
    expect(vs, describe(`the offline page (${scheme})`, vs)).toEqual([]);
  }
  // Back online, the page opens by itself: the one that was asked for (signed
  // out here, so the app sends it to sign in, and on to it after).
  await context.setOffline(false);
  await expect(page.getByRole("heading", { name: "You’re offline" })).toHaveCount(0);
  await expect(page).toHaveURL(/\/login\?next=%2Fchat%3Fwith%3Dx$/);
  await expect(page.locator("#root")).not.toBeEmpty();
  // Try again while still offline: the same page again, never the browser's error.
  await context.setOffline(true);
  await page.goto("/login").catch(() => null);
  await expect(page.getByRole("heading", { name: "You’re offline" })).toBeVisible();
  await page.evaluate(() => ((window as unknown as { marked: boolean }).marked = true));
  await page.getByRole("link", { name: "Try again" }).click();
  await expect.poll(() => page.evaluate(() => (window as unknown as { marked?: boolean }).marked ?? false)).toBe(false);
  await expect(page.getByRole("heading", { name: "You’re offline" })).toBeVisible();
  await expect(page).toHaveURL(/\/login$/);
  await context.setOffline(false);
  await expect(page.locator("#root")).not.toBeEmpty();
});
