import { execFileSync } from "node:child_process";
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
const FONTS = resolve(here, "fonts/fonts.conf");

// Text as a phone draws it, on any machine: glyphs at their exact, fractional
// places and edges in grey, not hinted, not in colour. Without these Chromium
// on Linux rounds each glyph's advance to a whole pixel and follows the
// machine's fontconfig (an Ubuntu runner's: sub-pixel colour, hinted), so a
// title can wrap on one machine and not on another.
test.use({
  launchOptions: {
    executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined,
    args: ["--font-render-hinting=none"],
    env: { ...process.env, FONTCONFIG_FILE: FONTS },
  },
});

/** What the server says when a code is asked for, whatever the number. */
const CODE_SENT = "If this number is on a Taskiem account, we have sent a code to it on WhatsApp or Telegram.";

/** The screens the app copies: its name in the app's test, where the web
 * shows it, and, for a signed-in screen, the fixed answers it is shown with
 * (mobile/test/parity/fixtures/<fixture>, recorded by mobile/tool/capture.mjs
 * from the demo world): every API answer comes from there and the clock is
 * stopped at the moment they were recorded, so the picture never changes
 * with the data or the time. */
const SCREENS: {
  name: string;
  path: string;
  fixture?: string;
  /** Seen as a touch phone sees it (no hover: a computer's Keyboard entry hidden). */
  touch?: boolean;
  ready: (p: Page) => Promise<void>;
}[] = [
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
    // The Starred chip: the messages this person starred, by business.
    // (The chats' recording with two starred messages added.)
    name: "chats-starred",
    path: "/chat",
    fixture: "chats-starred",
    ready: async (p) => {
      await expect(p.getByText("Bayo Rice Mills").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByRole("button", { name: "Starred", exact: true }).click();
      await expect(p.getByTestId("starred-message").first()).toBeVisible();
      await p.mouse.move(0, 0);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // The list's search, two letters on: the chats found, the messages,
    // and businesses on Taskiem to ask (the chats' recording, with answers
    // for "rice" added).
    name: "chats-search",
    path: "/chat",
    fixture: "chats-search",
    ready: async (p) => {
      await expect(p.getByText("Bayo Rice Mills").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("link-search").fill("rice");
      await expect(p.getByTestId("link-found")).toBeVisible();
      await expect(p.getByTestId("chat-found-message")).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.mouse.move(0, 0);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // Find a business (Explore), over the chats: near you, delivery, new on
    // Taskiem, each card with its photos. (Recorded from the demo world.)
    name: "explore",
    path: "/chat",
    fixture: "explore",
    touch: true,
    ready: async (p) => {
      await expect(p.getByTestId("new-chat")).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("new-chat").click();
      await expect(p.getByTestId("explore-card").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.waitForTimeout(500);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // Explore searched: "rice".
    name: "explore-search",
    path: "/chat",
    fixture: "explore",
    touch: true,
    ready: async (p) => {
      await expect(p.getByTestId("new-chat")).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("new-chat").click();
      await p.getByTestId("explore-search").fill("rice");
      await expect(p.getByLabel("Businesses found")).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.waitForTimeout(500);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
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
    // A message with tags: an item with its price, a linked business.
    // (Bayo's recording with the message added.)
    name: "chat-tagged",
    path: "/chat?with=ef99249d-9322-40ca-91c2-e24fdd450c15",
    fixture: "chat-tag",
    touch: true,
    ready: async (p) => {
      await expect(p.getByTestId("chat-card").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      // To its end, where the message is.
      await p.getByTestId("chat-stream").evaluate((e) => (e.scrollTop = e.scrollHeight));
      await p.waitForTimeout(200);

      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // "@" typed: things and businesses to tag, in groups, Taskiem last.
    name: "chat-tag",
    path: "/chat?with=ef99249d-9322-40ca-91c2-e24fdd450c15",
    fixture: "chat-tag",
    touch: true,
    ready: async (p) => {
      await expect(p.getByTestId("chat-card").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.locator(".composer textarea").fill("@");
      await expect(p.getByTestId("mention-list")).toBeVisible();
      await p.waitForTimeout(400);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // "#" typed: the items of the price list, with their prices.
    name: "chat-tag-hash",
    path: "/chat?with=ef99249d-9322-40ca-91c2-e24fdd450c15",
    fixture: "chat-tag",
    touch: true,
    ready: async (p) => {
      await expect(p.getByTestId("chat-card").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.locator(".composer textarea").fill("#");
      await expect(p.getByTestId("mention-list")).toBeVisible();
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // A deal: Bayo's counter on our order, its history and when it lapses,
    // Accept, Counter, Cancel. (Bayo's recording with the counter added.)
    name: "chat-deal",
    path: "/chat?with=ef99249d-9322-40ca-91c2-e24fdd450c15",
    fixture: "chat-deal",
    touch: true,
    ready: async (p) => {
      await expect(p.getByTestId("deal-actions")).toBeAttached();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("chat-stream").evaluate((e) => (e.scrollTop = e.scrollHeight));
      await p.waitForTimeout(200);

      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // Countering it: our price and a word with it.
    name: "chat-deal-counter",
    path: "/chat?with=ef99249d-9322-40ca-91c2-e24fdd450c15",
    fixture: "chat-deal",
    touch: true,
    ready: async (p) => {
      await expect(p.getByTestId("deal-actions")).toBeAttached();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("chat-stream").evaluate((e) => (e.scrollTop = e.scrollHeight));
      await p.waitForTimeout(200);
      await p.getByTestId("deal-actions").getByRole("button", { name: /^Counter/ }).click();
      await p.getByTestId("deal-actions").locator("input").first().fill("755k");
      await p.getByTestId("deal-actions").locator("input").nth(1).fill("Meet me halfway");
      await p.getByTestId("chat-stream").evaluate((e) => (e.scrollTop = e.scrollHeight));
      await p.waitForTimeout(200);
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
    // Contact info, the whole screen on a phone: the logo, name, role, Search
    // and Pin, About (its card), Starred, Media and files, Mute, Archive, End
    // link. (The files' recording with its card and a starred message added.)
    name: "chat-info",
    path: "/chat?with=828e255f-dc0f-4dd3-bb19-0ba2764778d3",
    fixture: "chat-info",
    ready: async (p) => {
      await expect(p.locator(".chat-photo img")).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByRole("button", { name: "About Mama Tunde's Market" }).click();
      await expect(p.getByTestId("chat-info")).toBeVisible();
      await expect(p.locator(".info-count").first()).toHaveText("1");
      await p.waitForLoadState("networkidle");
      await p.waitForTimeout(300);
      await p.mouse.move(0, 0);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // Its Media and files opened: every photo, voice note and file.
    name: "chat-info-media",
    path: "/chat?with=828e255f-dc0f-4dd3-bb19-0ba2764778d3",
    fixture: "chat-info",
    ready: async (p) => {
      await expect(p.locator(".chat-photo img")).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByRole("button", { name: "About Mama Tunde's Market" }).click();
      await expect(p.getByTestId("chat-info")).toBeVisible();
      await expect(p.locator(".info-count").first()).toHaveText("1");
      await p.getByTestId("info-media").click();
      await expect(p.locator(".info-media-all")).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.waitForTimeout(300);
      await p.mouse.move(0, 0);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // Its Starred messages.
    name: "chat-info-starred",
    path: "/chat?with=828e255f-dc0f-4dd3-bb19-0ba2764778d3",
    fixture: "chat-info",
    ready: async (p) => {
      await expect(p.locator(".chat-photo img")).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByRole("button", { name: "About Mama Tunde's Market" }).click();
      await expect(p.getByTestId("chat-info")).toBeVisible();
      await expect(p.locator(".info-count").first()).toHaveText("1");
      await p.getByTestId("info-starred").click();
      await expect(p.getByTestId("chat-starred")).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.waitForTimeout(300);
      await p.mouse.move(0, 0);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // The head's menu: Contact info, Pin, Mute, Archive.
    name: "chat-head-menu",
    path: "/chat?with=828e255f-dc0f-4dd3-bb19-0ba2764778d3",
    fixture: "chat-info",
    touch: true,
    ready: async (p) => {
      await expect(p.locator(".chat-photo img")).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByRole("button", { name: "More about this conversation" }).click();
      await expect(p.getByRole("button", { name: "Contact info" })).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.waitForTimeout(300);
      await p.mouse.move(0, 0);
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
    touch: true,
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
    touch: true,
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
    touch: true,
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
    touch: true,
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
    // A long message from them, folded: its first ten lines, "… Read more".
    // (Mama Tunde's conversation with a price list added to the recording.)
    name: "chat-long",
    path: "/chat?with=3af349e2-b776-4306-bdb8-10df59687c55",
    fixture: "chat-long",
    ready: async (p) => {
      await expect(p.getByRole("button", { name: "Read more" })).toBeVisible();
      await p.waitForLoadState("networkidle");
    },
  },
  {
    // Search in a conversation (the whole screen on a phone): "tomato", the
    // messages found with the words marked. (Mama Tunde's recording with
    // the search's answer added.)
    name: "chat-search",
    path: "/chat?with=3af349e2-b776-4306-bdb8-10df59687c55",
    fixture: "chat-search",
    ready: async (p) => {
      await expect(p.getByTestId("chat-message").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("chat-search-open").click();
      await p.getByTestId("chat-search").fill("tomato");
      await expect(p.getByTestId("chat-result").first()).toBeVisible();
      await p.mouse.move(0, 0);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // Its calendar, to go to a day: this month, today filled in.
    name: "chat-calendar",
    path: "/chat?with=3af349e2-b776-4306-bdb8-10df59687c55",
    fixture: "chat-search",
    ready: async (p) => {
      await expect(p.getByTestId("chat-message").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("chat-search-open").click();
      await p.getByTestId("chat-date-open").click();
      await expect(p.getByTestId("chat-calendar")).toBeVisible();
      await p.mouse.move(0, 0);
      await p.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    },
  },
  {
    // The menu's "+": every emoji in the menu's place, over the bubble.
    // Its search box takes the focus on the web; the picture is taken
    // without it (a phone would raise its keyboard).
    name: "chat-pick",
    path: "/chat?with=3af349e2-b776-4306-bdb8-10df59687c55",
    fixture: "chat-tunde",
    ready: async (p) => {
      await expect(p.getByTestId("chat-message").first()).toBeVisible();
      await p.waitForLoadState("networkidle");
      await p.getByTestId("chat-message").first().click({ button: "right", position: { x: 40, y: 20 } });
      await p.getByRole("button", { name: "More reactions" }).click();
      await expect(p.getByTestId("emoji-panel")).toBeVisible();
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
      // A reply to write puts the cursor in the message box a frame later
      // (composerFocus.ts): wait for it, or the blur below can come first
      // and the picture keeps the box's focus ring.
      await expect(p.locator("textarea:focus")).toHaveCount(1);
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
 * letter's edge alike), and the box round them (null when none). Worked out
 * by Chromium itself: no image library. */
export async function differs(page: Page, a: Buffer, b: Buffer): Promise<{ share: number; box: string | null }> {
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
      if (p.w !== q.w || p.h !== q.h) return { share: 1, box: `sizes ${p.w}x${p.h} and ${q.w}x${q.h}` };
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
      let [x0, y0, x1, y1] = [p.w, p.h, -1, -1];
      for (let j = 0; j < p.h; j++)
        for (let i = 0; i < p.w; i++)
          if (!near(p, q, i, j) && !near(q, p, i, j)) {
            n++;
            [x0, y0, x1, y1] = [Math.min(x0, i), Math.min(y0, j), Math.max(x1, i), Math.max(y1, j)];
          }
      return { share: n / (p.w * p.h), box: n ? `x ${x0}–${x1}, y ${y0}–${y1}` : null };
    },
    { x: a.toString("base64"), y: b.toString("base64") },
  );
}

for (const s of SCREENS)
  for (const theme of ["light", "dark"] as const)
    for (const size of SIZES) {
      const file = `${s.name}-${theme}-${size.width}x${size.height}.png`;
      test(`parity: ${file}`, async ({ browser }) => {
        const ctx = await browser.newContext({
          viewport: size,
          deviceScaleFactor: 1,
          reducedMotion: "reduce",
          timezoneId: "UTC",
          ...(s.touch ? { hasTouch: true, isMobile: true } : {}),
        });
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
          const { share, box } = await differs(page, shot, readFileSync(kept));
          if (share >= 0.002) {
            // What a machine that draws it differently needs looked at: the
            // picture it took (in the test's results), where it differs and
            // the fonts the page has.
            await test.info().attach(file, { body: shot, contentType: "image/png" });
            const fonts = await page.evaluate(() =>
              [...document.fonts].map((f) => `${f.family} ${f.weight} ${f.status}`).join("; "),
            );
            // And the colour fonts the machine offers for emoji.
            let emoji = "?";
            try {
              emoji = execFileSync("fc-list", [":color=true", "family", "file"], {
                env: { ...process.env, FONTCONFIG_FILE: FONTS },
                encoding: "utf8",
              }).replace(/\n/g, "; ");
            } catch {
              // fc-list is not on every machine.
            }
            console.log(`${file}: ${(share * 100).toFixed(2)}% differs, ${box}; fonts: ${fonts}; emoji: ${emoji}`);
          }
          expect(share, `${file}: the web looks different from what the app copies (${box}); if meant, run with PARITY_UPDATE=1 and make the app the same`).toBeLessThan(0.002);
        }
        await ctx.close();
      });
    }
