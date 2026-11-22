import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

test.use({ bypassCSP: true });

// Your PIN on the Account page (GET/PUT/DELETE /v1/me/pin): one PIN for
// approving on WhatsApp and paying small amounts. Signed in moments ago
// with no passkey, a first PIN is set and can pay a day on; it is removed.
// Against the real server.

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant: `Pin Kitchen ${letters()}`,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@pin.test`,
      name: "Ada Owner",
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

test("Your PIN: a first PIN set without a passkey pays a day on, and is removed", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await signUp(page);
  await page.goto("/account");
  const card = page.getByTestId("your-pin");
  await expect(card).toBeVisible();
  await expect(card.getByTestId("your-pin-hold")).toContainText("can pay from 24 hours on");
  await page.evaluate(() => localStorage.setItem("taskiem-theme", "system"));
  await page.reload();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const vs = await axe(page);
    expect(vs, describe(`Your PIN (${scheme})`, vs)).toEqual([]);
  }
  await page.emulateMedia({ colorScheme: "light" });

  // Two different PINs are refused here; the same twice is saved.
  await card.getByRole("textbox", { name: /^PIN / }).fill("482913");
  await card.getByLabel("The same PIN again").fill("482914");
  await card.getByRole("button", { name: "Set PIN" }).click();
  await expect(card.getByText("The two PINs are not the same.")).toBeVisible();
  await card.getByLabel("The same PIN again").fill("482913");
  await card.getByRole("button", { name: "Set PIN" }).click();
  await expect(card.getByTestId("your-pin-state")).toContainText("can pay from");

  page.once("dialog", (d) => void d.accept());
  await card.getByRole("button", { name: "Remove" }).click();
  await expect(card.getByTestId("your-pin-state")).toBeHidden();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});
