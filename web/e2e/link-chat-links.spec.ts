import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Links in chats (docs/spec/chat-previews-and-editing.md §2): a web address
// in a message opens in a new tab; the first link's card shows the page's
// picture, always from Taskiem's own address; the card shows above the box
// while a link is typed, and ✕ sends the message without it. The server's
// side (fetching through the egress guard) is tested in Go: here the page
// and the composer's card are stood in for.
// The page's Content-Security-Policy is bypassed only so axe can be added;
// where the picture comes from is checked from its address.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@links.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

/** Two linked businesses; Ada (the buyer) is the page under test. */
async function linked(browser: Browser) {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const buyer = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  await seller.request.put("/v1/link-settings", { headers: H, data: { findable: true } });
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`, { headers: H })).json();
  await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } });
  const link = (await (await seller.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  await seller.request.post(`/v1/links/${link}/accept`, { headers: H });
  return { seller, buyer, link };
}

async function axeBoth(page: Page, where: string) {
  for (const scheme of ["light", "dark"] as const) {
    await page.evaluate((t) => {
      if (t === "dark") document.documentElement.dataset.theme = "dark";
      else delete document.documentElement.dataset.theme;
    }, scheme);
    await page.waitForTimeout(400);
    const vs = await axe(page);
    expect(vs, describe(`${where} (${scheme})`, vs)).toEqual([]);
  }
  await page.evaluate(() => delete document.documentElement.dataset.theme);
}

async function fits(page: Page, where: string) {
  for (const width of [390, 360]) {
    await page.setViewportSize({ width, height: 800 });
    await page.waitForTimeout(200);
    expect(await page.evaluate(() => document.documentElement.scrollWidth), `${where} at ${width}`).toBeLessThanOrEqual(width);
  }
  await page.setViewportSize({ width: 390, height: 844 });
}

// A 1200 by 630 PNG: the size of a page's usual picture.
const PICTURE = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAYAAAD0In+KAAAAEUlEQVR4nGNgYPj/n4GBgQEAEAEB/cg9C3cAAAAASUVORK5CYII=",
  "base64",
);

const card = {
  url: "https://jumia.example/phones",
  host: "jumia.example",
  site: "Jumia",
  title: "Phones at the best prices",
  description: "Shop phones online, delivered to your door.",
  image: { w: 1200, h: 630 },
};

test("a link opens in a new tab and its card's picture comes from Taskiem", async ({ browser }) => {
  const { seller, buyer, link } = await linked(browser);
  const sent = await seller.request.post(`/v1/link-chats/${link}/messages`, { headers: H, data: { text: "See https://jumia.example/phones and www.example.com/rice." } });
  expect(sent.status()).toBe(201);
  // The server made the card (no page is reachable here): stood in for.
  await buyer.route(`**/v1/link-chats/${link}/messages*`, async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    const r = await route.fetch();
    const body = await r.json();
    for (const m of body.messages ?? []) if (m.body?.includes("jumia.example")) m.preview = card;
    await route.fulfill({ response: r, json: body });
  });
  const pictures: string[] = [];
  await buyer.route(`**/v1/link-chats/${link}/previews/*`, (route) => {
    pictures.push(route.request().url());
    return route.fulfill({ contentType: "image/png", body: PICTURE });
  });
  // Nothing leaves for the page's own site but the tab the person opens.
  await buyer.context().route("https://jumia.example/**", (route) => route.fulfill({ contentType: "text/html", body: "<title>Jumia</title>Phones" }));
  await buyer.context().route("https://www.example.com/**", (route) => route.fulfill({ contentType: "text/html", body: "<title>Rice</title>Rice" }));
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const theirs = pane.locator('[data-testid="chat-message"]').filter({ hasText: "jumia.example" });
  const links = theirs.getByTestId("chat-link");
  await expect(links).toHaveCount(2);
  await expect(links.first()).toHaveAttribute("href", "https://jumia.example/phones");
  await expect(links.first()).toHaveAttribute("target", "_blank");
  await expect(links.first()).toHaveAttribute("rel", "noopener noreferrer nofollow");
  // A bare address opens as https; the full stop is the sentence's.
  await expect(links.nth(1)).toHaveAttribute("href", "https://www.example.com/rice");
  // The card, at the top of the bubble, its picture from Taskiem's own address.
  const preview = theirs.getByTestId("chat-preview");
  await expect(preview).toContainText("Phones at the best prices");
  await expect(preview).toContainText("Jumia");
  const pic = preview.getByTestId("chat-preview-picture");
  await expect(pic).toBeVisible();
  const src = await pic.evaluate((el: HTMLImageElement) => el.currentSrc || el.src);
  expect(new URL(src).origin).toBe(new URL(buyer.url()).origin);
  expect(new URL(src).pathname).toMatch(new RegExp(`^/v1/link-chats/${link}/previews/`));
  expect(pictures.length).toBeGreaterThan(0);
  await expect(preview).toHaveClass(/wide/);
  // Tapping the link opens a new tab with the page.
  const [tab] = await Promise.all([buyer.context().waitForEvent("page"), links.first().click()]);
  await tab.waitForLoadState();
  expect(tab.url()).toBe("https://jumia.example/phones");
  await tab.close();
  // Away from the bubble: its hover chevron is not part of the page at rest.
  await buyer.mouse.move(1, 1);
  await axeBoth(buyer, "a message with a link and its card");
  await fits(buyer, "a message with a link and its card");
});

test("the card shows above the box while a link is typed, and ✕ sends without it", async ({ browser }) => {
  const { buyer, link } = await linked(browser);
  const asked: { url: string; chat: string }[] = [];
  await buyer.route("**/v1/link-preview", async (route) => {
    asked.push(JSON.parse(route.request().postData() ?? "{}"));
    await route.fulfill({ json: { id: "held1", ...card, image: { id: "held1", ...card.image } } });
  });
  await buyer.route("**/v1/link-preview/held1/image*", (route) => route.fulfill({ contentType: "image/png", body: PICTURE }));
  const bodies: Record<string, unknown>[] = [];
  await buyer.route(`**/v1/link-chats/${link}/messages`, async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    bodies.push(JSON.parse(route.request().postData() ?? "{}"));
    await route.continue();
  });
  await buyer.goto(`/chat?with=${link}`);
  const pane = buyer.getByTestId("chat-pane");
  const box = pane.getByLabel("Message");
  await box.fill("Look at https://jumia.example/phones");
  const shown = pane.getByTestId("composer-preview");
  await expect(shown).toContainText("Phones at the best prices");
  expect(asked).toEqual([{ url: "https://jumia.example/phones", chat: `link:${link}` }]);
  const src = await shown.getByTestId("chat-preview-picture").evaluate((el: HTMLImageElement) => el.currentSrc || el.src);
  expect(new URL(src).pathname).toBe("/v1/link-preview/held1/image");
  await axeBoth(buyer, "the card above the box");
  await fits(buyer, "the card above the box");
  // Sent as shown: it goes with the card's id.
  await box.press("Enter");
  await expect.poll(() => bodies.length).toBe(1);
  expect(bodies[0]?.preview).toEqual({ id: "held1" });
  await expect(shown).toHaveCount(0);
  // Closed with ✕: it goes without.
  await box.fill("And https://jumia.example/phones again");
  await expect(shown).toBeVisible();
  await pane.getByRole("button", { name: "Send without the preview" }).click();
  await expect(shown).toHaveCount(0);
  await box.press("Enter");
  await expect.poll(() => bodies.length).toBe(2);
  expect(bodies[1]?.preview).toBe(false);
  // Taking the link out of the words takes the card away.
  await box.fill("https://jumia.example/phones");
  await expect(shown).toBeVisible();
  await box.fill("no link now");
  await expect(shown).toHaveCount(0);
});
