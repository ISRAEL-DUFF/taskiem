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

/** What the server says when a code is asked for, whatever the number. */
const CODE_SENT = "If this number is on a Taskiem account, we have sent a code to it on WhatsApp or Telegram.";

/** The screens the app copies: its name in the app's test, where the web
 * shows it, and, for a signed-in screen, the fixed answers it is shown with
 * (mobile/test/parity/fixtures/<fixture>, recorded by mobile/tool/capture.mjs
 * from the demo world): every API answer comes from there and the clock is
 * stopped at the moment they were recorded, so the picture never changes
 * with the data or the time. */
const SCREENS: { name: string; path: string; fixture?: string; ready: (p: Page) => Promise<void> }[] = [
  {
    name: "sign-in",
    path: "/login",
    ready: async (p) => {
      await expect(p.getByRole("heading", { name: "Sign in to Taskiem" })).toBeVisible();
      await expect(p.getByLabel("Email")).toBeFocused();
    },
  },
  {
    // Signing in with the chat: the phone number.
    name: "sign-in-chat",
    path: "/login",
    ready: async (p) => {
      await p.getByRole("button", { name: "Sign in with WhatsApp or Telegram" }).click();
      await expect(p.getByLabel("Phone number")).toBeFocused();
    },
  },
  {
    // The code, after the server's answer (the same for any number).
    name: "sign-in-chat-code",
    path: "/login",
    ready: async (p) => {
      // The server's answer, word for word (api/chat_signin.go), stood in
      // for: codes are limited per address, and every browser test asks
      // from 127.0.0.1. The app's half stands in with the same.
      await p.route("**/v1/auth/chat/start", (r) =>
        r.fulfill({
          status: 202,
          contentType: "application/json",
          body: JSON.stringify({ expires_in: 600, status: CODE_SENT }),
        }),
      );
      await p.getByRole("button", { name: "Sign in with WhatsApp or Telegram" }).click();
      await p.getByLabel("Phone number").fill("08031234567");
      await p.getByRole("button", { name: "Send me a code" }).click();
      await expect(p.getByLabel("Code")).toBeFocused();
    },
  },
  {
    // A wrong password: the server's words in the error box.
    name: "sign-in-wrong",
    path: "/login",
    ready: async (p) => {
      await p.getByLabel("Email").fill("ada@parity.test");
      await p.getByLabel("Password").fill("not the password");
      await p.getByRole("button", { name: "Sign in", exact: true }).click();
      await expect(p.getByRole("alert")).toHaveText("invalid email or password");
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // Signing up: a business type chosen.
    name: "sign-up",
    path: "/signup",
    ready: async (p) => {
      await expect(p.getByRole("heading", { name: "Start with Taskiem" })).toBeVisible();
      await p.getByText("I bill customers for a service").click();
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // The chats: the demo world's, signed in as Ada.
    name: "chats",
    path: "/chat",
    fixture: "chats",
    ready: async (p) => {
      await expect(p.getByRole("heading", { name: "Chats" }).first()).toBeVisible();
      await expect(p.getByText("Bayo Rice Mills").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
    },
  },
  {
    // A conversation with a linked business: Mama Tunde's Market's, as Ada.
    name: "chat-tunde",
    path: "/chat?with=3af349e2-b776-4306-bdb8-10df59687c55",
    fixture: "chat-tunde",
    ready: async (p) => {
      await expect(p.getByTestId("chat-message").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // A conversation with cards: Bayo Rice Mills', an order and an invoice.
    name: "chat-bayo",
    path: "/chat?with=ef99249d-9322-40ca-91c2-e24fdd450c15",
    fixture: "chat-bayo",
    ready: async (p) => {
      await expect(p.getByTestId("chat-card").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // A photo with its caption, a voice note and a PDF: Mama Tunde's again.
    name: "chat-files",
    path: "/chat?with=828e255f-dc0f-4dd3-bb19-0ba2764778d3",
    fixture: "chat-files",
    ready: async (p) => {
      await expect(p.locator(".chat-photo img")).toBeVisible();
      await p.locator(".chat-photo img").evaluate((i: HTMLImageElement) => i.decode());
      await p.waitForLoadState("networkidle");
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // The same conversation scrolled to its end: the voice note and our PDF.
    name: "chat-files-end",
    path: "/chat?with=828e255f-dc0f-4dd3-bb19-0ba2764778d3",
    fixture: "chat-files",
    ready: async (p) => {
      await expect(p.locator(".chat-photo img")).toBeVisible();
      await p.locator(".chat-photo img").evaluate((i: HTMLImageElement) => i.decode());
      await p.getByTestId("chat-stream").evaluate((s) => (s.scrollTop = s.scrollHeight));
      await p.waitForLoadState("networkidle");
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // The clip's menu, open: Photo, Document, Camera.
    name: "chat-attach",
    path: "/chat?with=828e255f-dc0f-4dd3-bb19-0ba2764778d3",
    fixture: "chat-files",
    ready: async (p) => {
      await expect(p.locator(".chat-photo img")).toBeVisible();
      await p.locator(".chat-photo img").evaluate((i: HTMLImageElement) => i.decode());
      await p.getByTestId("attach").click();
      await expect(p.getByTestId("attach-menu")).toBeVisible();
      // A phone has no pointer left hovering over what was tapped.
      await p.mouse.move(0, 0);
      await p.waitForLoadState("networkidle");
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // A photo chosen, waiting for its caption (the fixture's own photo).
    name: "chat-caption",
    path: "/chat?with=828e255f-dc0f-4dd3-bb19-0ba2764778d3",
    fixture: "chat-files",
    ready: async (p) => {
      await expect(p.locator(".chat-photo img")).toBeVisible();
      await p.locator(".chat-photo img").evaluate((i: HTMLImageElement) => i.decode());
      await p.locator('input[type="file"]').first().setInputFiles({
        name: "tomatoes.jpg",
        mimeType: "image/jpeg",
        buffer: readFileSync(resolve(FIXTURES, "chat-files", "025.img")),
      });
      await expect(p.locator(".pending-photo")).toBeVisible();
      await p.locator(".pending-photo").evaluate((i: HTMLImageElement) => i.decode());
      await p.waitForLoadState("networkidle");
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // The person's own conversation with Taskiem: the conversation, then
    // the briefing and the cards of what needs them, as Ada.
    name: "chat-taskiem",
    path: "/chat?with=taskiem",
    fixture: "chat-taskiem",
    ready: async (p) => {
      await expect(p.getByTestId("taskiem-stream")).toBeVisible();
      await expect(p.getByTestId("chat-alerts")).toBeAttached();
      await p.waitForLoadState("networkidle");
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // The same, scrolled to its end: the briefing and the cards.
    name: "chat-taskiem-cards",
    path: "/chat?with=taskiem",
    fixture: "chat-taskiem",
    ready: async (p) => {
      await expect(p.getByTestId("chat-alerts")).toBeAttached();
      await p.waitForLoadState("networkidle");
      // The thread alone scrolled, the briefing at its top.
      await p.getByTestId("briefing-card").evaluate((c) => {
        const s = c.closest(".chat-stream") as HTMLElement;
        s.scrollTop += c.getBoundingClientRect().top - s.getBoundingClientRect().top;
      });
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // The same, the approval at the thread's top: Decline and Approve.
    name: "chat-taskiem-approval",
    path: "/chat?with=taskiem",
    fixture: "chat-taskiem",
    ready: async (p) => {
      await expect(p.getByTestId("chat-approval")).toBeAttached();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("chat-approval").evaluate((c) => {
        const s = c.closest(".chat-stream") as HTMLElement;
        s.scrollTop += c.getBoundingClientRect().top - s.getBoundingClientRect().top;
      });
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // Approve pressed on the approval: its sheet, asking to confirm.
    name: "chat-taskiem-approve",
    path: "/chat?with=taskiem",
    fixture: "chat-taskiem",
    ready: async (p) => {
      await expect(p.getByTestId("chat-approval")).toBeAttached();
      await p.waitForLoadState("networkidle");
      // The approval at the thread's top, then Approve.
      await p.getByTestId("chat-approval").evaluate((c) => {
        const s = c.closest(".chat-stream") as HTMLElement;
        s.scrollTop += c.getBoundingClientRect().top - s.getBoundingClientRect().top;
      });
      await p.getByTestId("chat-approval").getByRole("button", { name: /^Approve/ }).click();
      await expect(p.getByTestId("approve-sheet")).toBeVisible();
      await p.mouse.move(0, 0);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // The chats of a shop whose customers wrote to its WhatsApp number.
    name: "chats-customers",
    path: "/chat",
    fixture: "customers",
    ready: async (p) => {
      await expect(p.getByTestId("customer-chats")).toBeVisible();
      await p.waitForLoadState("networkidle");
    },
  },
  {
    // A customer's conversation: their messages and the shop's reply.
    name: "customer-chidi",
    path: "/chat?with=c-01a1caf1-bffd-7bf6-b932-a1e10d4b7bab",
    fixture: "customer-chidi",
    ready: async (p) => {
      await expect(p.getByTestId("customer-message").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // A message's menu (held on a phone, right-clicked here): the reactions
    // above it, what can be done with it below.
    name: "chat-menu",
    path: "/chat?with=3af349e2-b776-4306-bdb8-10df59687c55",
    fixture: "chat-tunde",
    ready: async (p) => {
      await expect(p.getByTestId("chat-message").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("chat-message").first().click({ button: "right", position: { x: 40, y: 20 } });
      await expect(p.getByTestId("chat-more")).toBeVisible();
      await p.mouse.move(0, 0);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // Reply chosen from the menu: what is being answered, over the box.
    name: "chat-reply",
    path: "/chat?with=3af349e2-b776-4306-bdb8-10df59687c55",
    fixture: "chat-tunde",
    ready: async (p) => {
      await expect(p.getByTestId("chat-message").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("chat-message").first().click({ button: "right", position: { x: 40, y: 20 } });
      await p.getByTestId("chat-more").getByRole("button", { name: "Reply" }).click();
      await p.mouse.move(0, 0);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // Delete for me chosen: the confirmation, asked first.
    name: "chat-delete",
    path: "/chat?with=3af349e2-b776-4306-bdb8-10df59687c55",
    fixture: "chat-tunde",
    ready: async (p) => {
      await expect(p.getByTestId("chat-message").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("chat-message").first().click({ button: "right", position: { x: 40, y: 20 } });
      await p.getByTestId("chat-more").getByRole("button", { name: "Delete for me" }).click();
      await expect(p.getByRole("dialog")).toBeVisible();
      await p.mouse.move(0, 0);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
];
const FIXTURES = resolve(here, "../../mobile/test/parity/fixtures");

/** The web app around the chat that the phone app does not have: the
 * notifications' and passkeys' asks, and the tab bar to Home, Money, Orders
 * and the menu. Hidden in the picture; the chat itself is compared. */
const NOT_THE_CHAT = ".notify-ask, nav.tabbar { display: none !important; } .chat-mode { --tabbar-h: 0px !important; }";

/** Answers every API request from a fixture's recorded answers. */
async function fixed(page: Page, name: string) {
  const dir = resolve(FIXTURES, name);
  const { at, answers } = JSON.parse(readFileSync(resolve(dir, "answers.json"), "utf8")) as {
    at: string;
    answers: Record<string, { file: string; status: number; type: string }>;
  };
  await page.clock.setFixedTime(new Date(at));
  await page.route("**/v1/**", (route) => {
    const u = new URL(route.request().url());
    if (u.pathname === "/v1/chat/stream") {
      return route.fulfill({ status: 200, contentType: "text/event-stream", body: "retry: 600000\n\nevent: ready\ndata: {}\n\n" });
    }
    // What the page sends (a read mark) is taken, as the server takes it.
    if (route.request().method() !== "GET") return route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
    const a = answers[u.pathname + u.search.replace(/after=[^&]*/, "after=*")];
    if (!a) {
      return route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: "not in the fixture" }) });
    }
    return route.fulfill({ status: a.status, contentType: a.type, body: readFileSync(resolve(dir, a.file)) });
  });
  await page.addInitScript((css) => {
    addEventListener("DOMContentLoaded", () => {
      const st = document.createElement("style");
      st.textContent = css;
      document.head.append(st);
    });
  }, NOT_THE_CHAT);
}

const SIZES = [
  { width: 390, height: 844 },
  { width: 360, height: 640 },
];

/** The share of pixels that differ, a pixel counting only when nothing within
 * one pixel of it in the other picture is close (two engines never draw a
 * letter's edge alike). Worked out by Chromium itself: no image library. */
export async function differs(page: Page, a: Buffer, b: Buffer): Promise<number> {
  return page.evaluate(
    async ({ x, y }) => {
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
            const close = (c: number) => Math.abs((s.d[o + c] ?? 0) - (t.d[k + c] ?? 0)) <= 24;
            if (close(0) && close(1) && close(2)) return true;
          }
        return false;
      };
      let n = 0;
      for (let j = 0; j < p.h; j++)
        for (let i = 0; i < p.w; i++) if (!near(p, q, i, j) && !near(q, p, i, j)) n++;
      return n / (p.w * p.h);
    },
    { x: a.toString("base64"), y: b.toString("base64") },
  );
}

for (const s of SCREENS)
  for (const theme of ["light", "dark"] as const)
    for (const size of SIZES) {
      const file = `${s.name}-${theme}-${size.width}x${size.height}.png`;
      test(`parity: ${file}`, async ({ browser }) => {
        const ctx = await browser.newContext({ viewport: size, deviceScaleFactor: 1, reducedMotion: "reduce", timezoneId: "UTC" });
        const page = await ctx.newPage();
        // Dark is the person's choice on the web (theme.ts).
        await page.addInitScript((t) => {
          if (t === "dark") localStorage.setItem("taskiem-theme", "dark");
        }, theme);
        if (s.fixture) await fixed(page, s.fixture);
        await page.goto(s.path);
        await s.ready(page);
        const shot = await page.screenshot({ animations: "disabled", caret: "hide" });
        const kept = resolve(KEPT, file);
        // In CI a screen with no kept picture fails: writing one there would
        // pass unseen, and the app's half would have nothing to match.
        if (!UPDATE && !existsSync(kept) && process.env.CI) {
          throw new Error(`${file} is not kept: run the parity spec with PARITY_UPDATE=1 and commit mobile/test/parity/web/${file}`);
        }
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
