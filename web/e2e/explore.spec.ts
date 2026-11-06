import {
  expect,
  test,
  type Browser,
  type Page,
  type Route,
} from "@playwright/test";
import { axe, describe } from "./a11y";

// Explore: finding a business by browsing, on a screen of its own. The lists,
// the search, Connect and Withdraw run on the real server; the photos and
// logos (the test server has no photo storage) are stand-in pictures, added
// to the lists' answers, so the swipe and its dots can be seen working.
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

const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAoAAAAKCAYAAACNMs+9AAAAFUlEQVR42mP8z8BQz0AEYBxVSF+FABJADveWkH6oAAAAAElFTkSuQmCC",
  "base64",
);

async function twoBusinesses(browser: Browser, buyerOptions = {}) {
  const stamp = letters();
  const seller = await (
    await browser.newContext({ viewport: { width: 1280, height: 800 } })
  ).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const hidden = await (await browser.newContext()).newPage();
  await signUp(hidden, `Hidden Hardware ${stamp}`);
  const buyer = await (
    await browser.newContext({
      viewport: { width: 1280, height: 800 },
      ...buyerOptions,
    })
  ).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  expect(
    (
      await seller.request.put("/v1/link-settings", {
        headers: H,
        data: { findable: true },
      })
    ).ok(),
  ).toBe(true);
  const name = `Dangote Cement ${stamp}`;
  expect(
    (
      await seller.request.put("/v1/business-profile", {
        headers: H,
        data: {
          name,
          tagline: "Cement and sand, delivered",
          area: "Ikeja",
          about: "Family-run since 1998.",
        },
      })
    ).ok(),
  ).toBe(true);
  expect(
    (
      await buyer.request.put("/v1/business-profile", {
        headers: H,
        data: { name: "", tagline: "", area: "Ikeja", about: "" },
      })
    ).ok(),
  ).toBe(true);
  // Stand-in pictures: four photos and a logo for each card the server lists.
  await buyer.route("**/v1/explore/*/photos/*", (r: Route) =>
    r.fulfill({ status: 200, contentType: "image/png", body: PNG }),
  );
  await buyer.route("**/v1/explore/*/logo*", (r: Route) =>
    r.fulfill({ status: 200, contentType: "image/png", body: PNG }),
  );
  await buyer.route(/\/v1\/explore(\?.*)?$/, async (route) => {
    const res = await route.fetch();
    const j = (await res.json()) as Record<
      string,
      { photos?: number; logo?: boolean }[]
    >;
    for (const k of ["results", "near", "fresh"]) {
      for (const c of j[k] ?? []) {
        c.photos = 4;
        c.logo = true;
      }
    }
    return route.fulfill({ response: res, json: j });
  });
  return { seller, buyer, hidden, name, stamp };
}

test("Find a business opens Explore: Near you and the newest, a swipe of photos with dots, Connect and Withdraw, Message once linked; only those found", async ({
  browser,
}) => {
  const { seller, buyer, name } = await twoBusinesses(browser);
  await buyer.goto("/chat");
  await buyer.getByTestId("new-chat").click();
  const ex = buyer.getByTestId("explore");
  await expect(ex).toBeVisible();
  await expect(ex.getByTestId("explore-search")).toBeFocused();

  // The two sections; the business that is not findable is not there.
  await expect(ex.getByRole("heading", { name: "Near you" })).toBeVisible();
  await expect(
    ex.getByRole("heading", { name: "New on Taskiem" }),
  ).toBeVisible();
  const card = ex.getByTestId("explore-card").filter({ hasText: name }).first();
  await expect(card).toContainText("Cement and sand, delivered · Ikeja");
  await expect(card).toContainText("Family-run since 1998.");
  await expect(ex).not.toContainText("Hidden Hardware");

  // Photos: four slides, dots that follow, keys and dots that move.
  const track = card.getByRole("group", {
    name: /swipe or use the arrow keys/,
  });
  await expect(card.locator(".carousel-slide")).toHaveCount(4);
  await expect(card.locator(".carousel-dot")).toHaveCount(4);
  await expect(
    card.locator('.carousel-dot[aria-current="true"]'),
  ).toHaveAttribute("aria-label", "Show photo 1 of 4");
  await expect
    .poll(() =>
      card
        .locator(".carousel-slide img")
        .first()
        .evaluate((i: HTMLImageElement) => i.naturalWidth),
    )
    .toBeGreaterThan(0);
  await track.focus();
  await buyer.keyboard.press("ArrowRight");
  await expect(
    card.locator('.carousel-dot[aria-current="true"]'),
  ).toHaveAttribute("aria-label", "Show photo 2 of 4");
  await card.getByRole("button", { name: "Show photo 4 of 4" }).click();
  await expect(
    card.locator('.carousel-dot[aria-current="true"]'),
  ).toHaveAttribute("aria-label", "Show photo 4 of 4");
  await card.getByRole("button", { name: "Show photo 1 of 4" }).click();
  await expect(
    card.locator('.carousel-dot[aria-current="true"]'),
  ).toHaveAttribute("aria-label", "Show photo 1 of 4");
  await axeBoth(buyer, "Explore");

  // Connect asks at once (both ways); Withdraw takes it back.
  await card.getByRole("button", { name: new RegExp(`^Connect`) }).click();
  await expect(card.getByText("Requested")).toBeVisible();
  const asked = async () =>
    (
      (await (
        await seller.request.get("/v1/links", { headers: H })
      ).json()) as { links: { state: string; role: string }[] }
    ).links;
  await expect
    .poll(async () => (await asked()).map((l) => `${l.state}/${l.role}`))
    .toEqual(["asked_us/both"]); // as the other side sees it;
  await card.getByRole("button", { name: /^Withdraw/ }).click();
  await expect(card.getByRole("button", { name: /^Connect/ })).toBeVisible();
  await expect.poll(async () => (await asked()).length).toBe(0);

  // Connect again; they accept; it is a chat now: out of the sections, a Message in a search.
  await card.getByRole("button", { name: /^Connect/ }).click();
  await expect(card.getByText("Requested")).toBeVisible();
  await expect.poll(async () => (await asked()).length).toBe(1);
  const link = (await asked())[0] as unknown as { id: string };
  expect(
    (
      await seller.request.post(`/v1/links/${link.id}/accept`, { headers: H })
    ).ok(),
  ).toBe(true);
  await buyer.keyboard.press("Escape"); // closes Explore
  await expect(ex).toHaveCount(0);
  await expect(buyer.getByTestId("new-chat")).toBeFocused();
  await buyer.getByTestId("new-chat").click();
  await expect(
    ex.getByTestId("explore-card").filter({ hasText: name }),
  ).toHaveCount(0);
  await ex.getByTestId("explore-search").fill("cement");
  const found = ex.getByTestId("explore-card").filter({ hasText: name });
  await expect(found).toBeVisible();
  await found.getByRole("button", { name: /^Message/ }).click();
  await expect(ex).toHaveCount(0);
  await expect(buyer).toHaveURL(/\/chat\?with=/);
});

test("Explore search: nothing found says so, a phone number is the one blind row, a business that is not findable is never found; the back arrow closes it", async ({
  browser,
}) => {
  const { buyer, stamp } = await twoBusinesses(browser);
  await buyer.goto("/chat");
  await buyer.getByTestId("new-chat").click();
  const ex = buyer.getByTestId("explore");
  const box = ex.getByTestId("explore-search");
  await box.fill("zzqx");
  await expect(ex).toContainText("No business found for “zzqx”");
  await box.fill(`hidden hardware ${stamp}`);
  await expect(ex).toContainText("No business found");
  await box.fill("0803 123 4567");
  await expect(ex.getByTestId("explore-blind")).toContainText(
    "Send a link request to 0803…4567",
  );
  await box.fill("");
  await expect(
    ex.getByRole("heading", { name: "New on Taskiem" }),
  ).toBeVisible();
  await ex.getByTestId("explore-back").click();
  await expect(ex).toHaveCount(0);
  await expect(buyer.getByTestId("new-chat")).toBeFocused();
});

test("Explore on a phone fits the screen: nothing scrolls sideways, the photos swipe inside their card", async ({
  browser,
}) => {
  const { buyer, name } = await twoBusinesses(browser, {
    viewport: { width: 360, height: 740 },
  });
  await buyer.goto("/chat");
  await buyer.getByTestId("new-chat").click();
  const ex = buyer.getByTestId("explore");
  const card = ex.getByTestId("explore-card").filter({ hasText: name }).first();
  await expect(card).toBeVisible();
  const box = await card.boundingBox();
  expect(box && box.x >= 0 && box.x + box.width <= 360).toBe(true);
  expect(
    await buyer.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth + 1,
    ),
  ).toBe(false);
  await buyer.screenshot({
    path: "/tmp/claude-0/-home-user-taskier/ee15d754-4291-5a7a-bc3f-fed0af824ab6/scratchpad/explore-360.png",
  });
  await axeBoth(buyer, "Explore on a phone");
});

test("with Explore open over a conversation, the keys and Esc belong to Explore, not to the chat behind it; the phone's Back closes it", async ({
  browser,
}) => {
  const { seller, buyer, name } = await twoBusinesses(browser);
  // A chat to have open behind it: the buyer asks, the seller accepts.
  const cards = (await (
    await buyer.request.get("/v1/explore", { headers: H })
  ).json()) as {
    fresh: { business: string; name: string }[];
  };
  const sellerId = cards.fresh.find((c) => c.name === name)?.business;
  expect(
    (
      await buyer.request.post("/v1/links", {
        headers: H,
        data: { business: sellerId, role: "both" },
      })
    ).ok(),
  ).toBe(true);
  const asked = (await (
    await seller.request.get("/v1/links", { headers: H })
  ).json()) as { links: { id: string }[] };
  expect(
    (
      await seller.request.post(`/v1/links/${asked.links[0]?.id}/accept`, {
        headers: H,
      })
    ).ok(),
  ).toBe(true);
  const link = (
    (await (
      await buyer.request.get("/v1/link-chats", { headers: H })
    ).json()) as { conversations: { link: string }[] }
  ).conversations[0]?.link;
  const here = new RegExp(`with=${link}`);

  await buyer.goto(`/chat?with=${link}`);
  await expect(buyer.getByTestId("chat-pane")).toBeVisible();
  await buyer.getByTestId("new-chat").click();
  const ex = buyer.getByTestId("explore");
  await expect(ex).toBeVisible();
  await expect(buyer).toHaveURL(here); // the chat stays open behind it

  // The chat page's shortcuts do nothing behind Explore.
  await ex.getByTestId("explore-back").focus();
  for (const key of ["j", "k", "n", "/", "?"]) await buyer.keyboard.press(key);
  await expect(buyer).toHaveURL(here);
  await expect(ex).toBeVisible();
  await expect(
    buyer.getByRole("dialog", { name: /keyboard|shortcuts/i }),
  ).toHaveCount(0);

  // Esc closes Explore only: the conversation is still open.
  await buyer.keyboard.press("Escape");
  await expect(ex).toHaveCount(0);
  await expect(buyer).toHaveURL(here);
  await expect(buyer.getByTestId("chat-pane")).toBeVisible();

  // The phone's Back closes it too, and stays on the chat.
  await buyer.getByTestId("new-chat").click();
  await expect(ex).toBeVisible();
  await buyer.goBack();
  await expect(ex).toHaveCount(0);
  await expect(buyer).toHaveURL(here);
  await expect(buyer.getByTestId("chat-pane")).toBeVisible();
});

test("a request to a phone number from Explore says it was sent, and does not offer to send it again", async ({
  browser,
}) => {
  const { buyer } = await twoBusinesses(browser);
  await buyer.goto("/chat");
  await buyer.getByTestId("new-chat").click();
  const ex = buyer.getByTestId("explore");
  await ex.getByTestId("explore-search").fill("0803 123 4567");
  const blind = ex.getByTestId("explore-blind");
  await blind.getByRole("button", { name: "Send request" }).click();
  await expect(blind.getByText("Request sent")).toBeVisible();
  await expect(blind.getByRole("button", { name: "Send request" })).toHaveCount(
    0,
  );
});
