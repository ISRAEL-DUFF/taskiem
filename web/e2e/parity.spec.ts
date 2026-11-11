import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test, type Page } from "@playwright/test";

// The web half of the phone app's parity test (docs/spec/mobile-app.md
// §4.1): each screen the app copies, as the web draws it, light and dark,
// at two phone sizes, kept in mobile/test/parity/web/. The app's own test
// (mobile/test/parity_test.dart) compares its screens with these files.
//
// Here the web is compared with the kept files, so a change to how the web
// chat looks fails until they are written again (PARITY_UPDATE=1), and then
// the app's test fails until the app looks the same.

const here = dirname(fileURLToPath(import.meta.url));
const KEPT = resolve(here, "../../mobile/test/parity/web");
const UPDATE = process.env.PARITY_UPDATE === "1";

/** The screens the app copies: its name in the app's test, and where the web shows it. */
const SCREENS: { name: string; path: string; ready: (p: Page) => Promise<void> }[] = [
  {
    name: "sign-in",
    path: "/login",
    ready: async (p) => {
      await expect(p.getByRole("heading", { name: "Sign in to Taskiem" })).toBeVisible();
      await expect(p.getByLabel("Email")).toBeFocused();
    },
  },
];
const SIZES = [
  { width: 390, height: 844 },
  { width: 360, height: 640 },
];

/** The share of pixels that differ, a pixel counting only when nothing within
 * one pixel of it in the other picture is close (two engines never draw a
 * letter's edge alike). Worked out by Chromium itself: no image library. */
export async function differs(page: Page, a: Buffer, b: Buffer): Promise<number> {
  return page.evaluate(
    async ([x, y]) => {
      const load = async (b64: string) => {
        // An <img>, not fetch: the app's content policy allows data: images only.
        const img = new Image();
        img.src = `data:image/png;base64,${b64}`;
        await img.decode();
        const c = new OffscreenCanvas(img.width, img.height);
        const g = c.getContext("2d") as OffscreenCanvasRenderingContext2D;
        g.drawImage(img, 0, 0);
        return { w: img.width, h: img.height, d: g.getImageData(0, 0, img.width, img.height).data };
      };
      const [p, q] = [await load(x), await load(y)];
      if (p.w !== q.w || p.h !== q.h) return 1;
      const near = (s: typeof p, t: typeof p, i: number, j: number) => {
        const o = (j * s.w + i) * 4;
        for (let dy = -1; dy <= 1; dy++)
          for (let dx = -1; dx <= 1; dx++) {
            const ii = i + dx;
            const jj = j + dy;
            if (ii < 0 || jj < 0 || ii >= t.w || jj >= t.h) continue;
            const k = (jj * t.w + ii) * 4;
            if (
              Math.abs(s.d[o] - t.d[k]) <= 24 &&
              Math.abs(s.d[o + 1] - t.d[k + 1]) <= 24 &&
              Math.abs(s.d[o + 2] - t.d[k + 2]) <= 24
            )
              return true;
          }
        return false;
      };
      let n = 0;
      for (let j = 0; j < p.h; j++)
        for (let i = 0; i < p.w; i++) if (!near(p, q, i, j) && !near(q, p, i, j)) n++;
      return n / (p.w * p.h);
    },
    [a.toString("base64"), b.toString("base64")],
  );
}

for (const s of SCREENS)
  for (const theme of ["light", "dark"] as const)
    for (const size of SIZES) {
      const file = `${s.name}-${theme}-${size.width}x${size.height}.png`;
      test(`parity: ${file}`, async ({ browser }) => {
        const ctx = await browser.newContext({ viewport: size, deviceScaleFactor: 1, reducedMotion: "reduce" });
        const page = await ctx.newPage();
        // Dark is the person's choice on the web (theme.ts).
        await page.addInitScript((t) => {
          if (t === "dark") localStorage.setItem("taskiem-theme", "dark");
        }, theme);
        await page.goto(s.path);
        await s.ready(page);
        const shot = await page.screenshot({ animations: "disabled", caret: "hide" });
        const kept = resolve(KEPT, file);
        if (UPDATE || !existsSync(kept)) {
          mkdirSync(KEPT, { recursive: true });
          writeFileSync(kept, shot);
        } else {
          const share = await differs(page, shot, readFileSync(kept));
          expect(share, `${file}: the web looks different from what the app copies; if meant, run with PARITY_UPDATE=1 and make the app the same`).toBeLessThan(0.002);
        }
        await ctx.close();
      });
    }
