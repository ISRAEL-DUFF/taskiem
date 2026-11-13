import { expect, test } from "@playwright/test";
import { axe, describe } from "./a11y";

// Show and Hide on a password box: sign-up (where a typo costs most) and
// sign-in, on a phone's width, in both themes. The box keeps its name, the
// field's label alone, so "Password" still finds one thing.
test.use({ bypassCSP: true });

for (const scheme of ["light", "dark"] as const)
  for (const width of [390, 360])
    test(`show and hide the password at ${width} px, ${scheme} theme`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: "reduce" });
      for (const path of ["/signup", "/login"]) {
        await page.goto(path);
        const box = page.getByLabel("Password", { exact: true });
        await expect(box).toHaveAttribute("type", "password");
        await box.fill("correct horse battery");
        await page.getByRole("button", { name: "Show", exact: true }).click();
        await expect(box).toHaveAttribute("type", "text");
        await expect(box).toHaveValue("correct horse battery");
        // The press stays on the button, not the box.
        await expect(page.getByRole("button", { name: "Hide", exact: true })).toBeFocused();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
        const vs = await axe(page);
        expect(vs, describe(path, vs)).toEqual([]);
        await page.getByRole("button", { name: "Hide", exact: true }).click();
        await expect(box).toHaveAttribute("type", "password");
      }
    });

test("a shown password is hidden again when the form is sent", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("nobody@e2e.test");
  await page.getByLabel("Password").fill("not the password at all");
  await page.getByRole("button", { name: "Show", exact: true }).click();
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByLabel("Password")).toHaveAttribute("type", "password");
});
