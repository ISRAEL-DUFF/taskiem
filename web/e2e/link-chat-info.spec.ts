import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Contact info, as WhatsApp's: round actions under the name, rows with an
// icon each, a switch for muting, starred messages and media as rows, and
// the link's end in red, confirmed in the app's own sheet (never the
// browser's grey box).
test.use({ bypassCSP: true, viewport: { width: 1280, height: 800 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: {
      tenant,
      email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@chat.test`,
      name: `${tenant.split(" ")[0]} Owner`,
      password: "correct horse battery",
    },
  });
  expect(r.status(), await r.text()).toBe(201);
}

type Ctx = Parameters<Browser["newContext"]>[0];
async function linked(browser: Browser, buyerOptions: Ctx = {}) {
  const stamp = letters();
  const seller = await (
    await browser.newContext({ viewport: { width: 1280, height: 800 } })
  ).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const buyer = await (
    await browser.newContext({
      viewport: { width: 1280, height: 800 },
      ...buyerOptions,
    })
  ).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  await seller.request.put("/v1/link-settings", {
    headers: H,
    data: { findable: true },
  });
  const found = await (
    await buyer.request.get(
      `/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`,
      { headers: H },
    )
  ).json();
  await buyer.request.post("/v1/links", {
    headers: H,
    data: { business: found.results[0].business, role: "supplier" },
  });
  const link = (
    await (await seller.request.get("/v1/links", { headers: H })).json()
  ).links[0].id as string;
  await seller.request.post(`/v1/links/${link}/accept`, { headers: H });
  return { seller, buyer, link, name: `Dangote Depot ${stamp}` };
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(300);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

const say = (p: Page, link: string, text: string, extra = {}) =>
  p.request.post(`/v1/link-chats/${link}/messages`, {
    headers: H,
    data: { text, ...extra },
  });

const must = <T>(v: T | null | undefined): T => {
  if (v == null) throw new Error("nothing there");
  return v;
};

test("contact info: round actions, rows, a mute switch, starred messages, and the link ended from a sheet of the app's own", async ({
  browser,
}) => {
  const { seller, buyer, link, name } = await linked(browser);
  const dialogs: string[] = [];
  buyer.on("dialog", (d) => {
    dialogs.push(d.message());
    void d.dismiss();
  });
  const sent = await say(seller, link, "Rice is 45k a bag");
  expect(sent.status()).toBe(201);
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const msg = pane
    .getByTestId("chat-message")
    .filter({ hasText: "Rice is 45k" });
  await expect(msg).toBeVisible();
  const id = (
    (await (
      await buyer.request.get(`/v1/link-chats/${link}/messages`, { headers: H })
    ).json()) as { messages: { id: string }[] }
  ).messages[0]?.id;
  expect(
    (
      await buyer.request.put(`/v1/link-chats/${link}/messages/${id}/star`, {
        headers: H,
      })
    ).ok(),
  ).toBe(true);

  await pane.getByRole("button", { name: `About ${name}` }).click();
  const info = pane.getByTestId("chat-info");
  await expect(info).toBeVisible();

  // No outlined text buttons: rows and round actions.
  await expect(info.locator(".chat-info-actions")).toHaveCount(0);
  await expect(info.getByRole("button", { name: "Search" })).toBeVisible();
  const pin = info.getByRole("button", { name: "Pin", exact: true });
  await expect(pin).toHaveAttribute("aria-pressed", "false");
  await pin.click();
  await expect(
    info.getByRole("button", { name: "Unpin", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");

  // Mute is a switch.
  const mute = info.getByRole("switch", { name: "Mute notifications" });
  await expect(mute).toHaveAttribute("aria-checked", "false");
  await mute.click();
  await expect(mute).toHaveAttribute("aria-checked", "true");
  await expect
    .poll(async () => {
      const c = (await (
        await buyer.request.get("/v1/link-chats", { headers: H })
      ).json()) as {
        conversations: { muted: boolean; pinned: boolean }[];
      };
      return `${c.conversations[0]?.muted}/${c.conversations[0]?.pinned}`;
    })
    .toBe("true/true");
  await axeBoth(buyer, "the contact info");

  // Starred messages: a row with its count, a list, and back.
  await expect(info.getByTestId("info-starred")).toContainText("1");
  await info.getByTestId("info-starred").click();
  const list = pane.getByTestId("chat-starred");
  await expect(list).toContainText("Rice is 45k");
  await pane.getByRole("button", { name: "Back to contact info" }).click();
  await expect(pane.getByTestId("chat-info")).toContainText("Contact info");

  // Search closes the panel and opens the search of the chat.
  await pane
    .getByTestId("chat-info")
    .getByRole("button", { name: "Search" })
    .click();
  await expect(pane.getByTestId("chat-info")).toHaveCount(0);
  await expect(pane.getByTestId("chat-search-panel")).toBeVisible();
  await buyer.keyboard.press("Escape");

  // Ending the link: red row, the app's own sheet, Cancel has the focus and keeps the link.
  await pane.getByRole("button", { name: `About ${name}` }).click();
  await pane.getByTestId("info-end-link").click();
  const sheet = buyer.getByRole("dialog");
  await expect(sheet).toContainText(`End the link with ${name}?`);
  await expect(sheet.getByRole("button", { name: "Cancel" })).toBeFocused();
  await axeBoth(buyer, "the end-link sheet");
  await sheet.getByRole("button", { name: "Cancel" }).click();
  await expect(sheet).toHaveCount(0);
  await pane.getByTestId("info-end-link").click();
  await buyer.getByTestId("confirm-ok").click();
  await expect
    .poll(async () => {
      const l = (await (
        await buyer.request.get("/v1/links", { headers: H })
      ).json()) as {
        links: { state: string }[];
      };
      return l.links[0]?.state;
    })
    .toBe("ended");
  expect(dialogs, "the browser's own box was never used").toEqual([]);
});

test("contact info on a phone fits the screen", async ({ browser }) => {
  const { buyer, link, name, seller } = await linked(browser, {
    viewport: { width: 360, height: 740 },
  });
  await say(seller, link, "Hello");
  await buyer.goto(`/chat?with=${link}`);
  await buyer.getByRole("button", { name: `About ${name}` }).click();
  const info = buyer.getByTestId("chat-info");
  await expect(info).toBeVisible();
  const r = must(await info.boundingBox());
  expect(r.x).toBeGreaterThanOrEqual(0);
  expect(r.x + r.width).toBeLessThanOrEqual(360);
  expect(
    await buyer.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth + 1,
    ),
  ).toBe(false);
  await buyer.screenshot({
    path: "/tmp/claude-0/-home-user-taskier/ee15d754-4291-5a7a-bc3f-fed0af824ab6/scratchpad/info-360.png",
  });
});
