import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// A linked business is shown in chat by the name and logo it chose (its
// profile): in the list, in the conversation's head and in its info. The
// name runs on the real server; the logo (the test server has no photo
// storage) is a stand-in picture, and a logo that cannot load gives way to
// the letter.
test.use({ bypassCSP: true });

const H = { "X-Taskiem-Request": "1" };
const letters = () =>
  String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
  "base64",
);

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

test("the chat shows a linked business by its profile name and logo; the links list keeps the registered name; a logo that will not load gives way to the letter", async ({
  browser,
}) => {
  const { seller, buyer, link, name } = await linked(browser, {
    viewport: { width: 1280, height: 800 },
  });
  const profileName = `${name} & Sons`;
  const r = await seller.request.put("/v1/business-profile", {
    headers: H,
    data: { name: profileName, tagline: "", area: "", about: "" },
  });
  expect(r.status(), await r.text()).toBe(200);

  // The logo: a stand-in picture, and the list says there is one.
  let logoStatus = 200;
  await buyer.route("**/v1/links/*/logo**", (route) =>
    logoStatus === 200
      ? route.fulfill({ status: 200, contentType: "image/png", body: PNG })
      : route.fulfill({
          status: 404,
          contentType: "application/json",
          body: "{}",
        }),
  );
  await buyer.route("**/v1/link-chats", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    const res = await route.fetch();
    const j = (await res.json()) as { conversations: { logo?: boolean }[] };
    for (const c of j.conversations) c.logo = true;
    return route.fulfill({ response: res, json: j });
  });
  await buyer.goto(`/chat?with=${link}`);
  const row = buyer.getByTestId("link-linked").filter({ hasText: profileName });
  await expect(row).toBeVisible();
  await expect(row.locator("img.chat-avatar")).toBeVisible();
  const pane = buyer.getByTestId("chat-pane");
  await expect(pane.getByRole("heading", { name: profileName })).toBeVisible();
  await expect(pane.locator(".chat-head img.chat-avatar")).toBeVisible();
  // The picture really loaded (not a broken image).
  await expect
    .poll(() =>
      pane
        .locator(".chat-head img.chat-avatar")
        .evaluate((i: HTMLImageElement) => i.naturalWidth),
    )
    .toBeGreaterThan(0);
  await pane.getByRole("button", { name: `About ${profileName}` }).click();
  await expect(buyer.locator(".info-hero img.chat-avatar")).toBeVisible();
  await axeBoth(buyer, "a conversation with a logo");

  // The registered name stays where it is the legal one.
  await buyer.goto("/settings/links");
  await expect(buyer.getByRole("main")).toContainText(name);
  await expect(buyer.getByRole("main")).not.toContainText("& Sons");

  // A logo that will not load: the letter.
  logoStatus = 404;
  await buyer.goto(`/chat?with=${link}`);
  const row2 = buyer
    .getByTestId("link-linked")
    .filter({ hasText: profileName });
  await expect(row2.locator(".chat-avatar")).toHaveText(
    profileName.charAt(0).toUpperCase(),
  );
  await expect(row2.locator("img.chat-avatar")).toHaveCount(0);
});
