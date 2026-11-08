import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Reactions, stars and message info, as WhatsApp's: a tap on a message adds
// one of six emoji that both businesses see; a star keeps a message for the
// person who starred it; Info shows when it was sent and read.
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

/** Two linked businesses, each with its page open on the conversation. */
async function linked(browser: Browser) {
  const stamp = letters();
  const seller = await (
    await browser.newContext({ viewport: { width: 1280, height: 800 } })
  ).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const buyer = await (
    await browser.newContext({ viewport: { width: 1280, height: 800 } })
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

const bubble = (page: Page, text: string) =>
  page
    .getByTestId("chat-pane")
    .locator('[data-testid="chat-message"]', { hasText: text });
const NAMES: Record<string, string> = {
  "👍": "thumbs up",
  "❤️": "heart",
  "😂": "laughing",
  "😮": "surprise",
  "😢": "sad",
  "🙏": "thanks",
};
async function react(page: Page, text: string, emoji: string) {
  await bubble(page, text).getByRole("button", { name: /^More/ }).click();
  await page
    .getByTestId("chat-more")
    .getByRole("button", { name: `React with ${NAMES[emoji]}` })
    .click();
}

test("a reaction is seen by both businesses, changed, added to by the other, and taken back", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await seller.request.post(`/v1/link-chats/${link}/messages`, {
    headers: H,
    data: { text: "Rice is back in stock" },
  });
  await buyer.goto(`/chat?with=${link}`);
  await seller.goto(`/chat?with=${link}`);
  await expect(bubble(buyer, "Rice is back")).toBeVisible();
  await expect(bubble(seller, "Rice is back")).toBeVisible();

  // The buyer taps a thumbs up on the menu's row: a chip, at once.
  await bubble(buyer, "Rice is back")
    .getByRole("button", { name: /^More/ })
    .click();
  const picker = buyer
    .getByTestId("chat-more")
    .getByRole("group", { name: "React" });
  await expect(picker.getByRole("button")).toHaveCount(7); // the six, and the + for any emoji
  await axeBoth(buyer, "the reaction row in the menu");
  await picker.getByRole("button", { name: "React with thumbs up" }).click();
  const chip = bubble(buyer, "Rice is back").getByRole("button", {
    name: /^👍 1/,
  });
  await expect(chip).toBeVisible();
  await expect(chip).toHaveAttribute("aria-pressed", "true");
  await axeBoth(buyer, "a reaction chip");

  // The seller sees it, without reloading, with the buyer's name.
  const theirs = bubble(seller, "Rice is back").getByRole("button", {
    name: /^👍 1: Ada/,
  });
  await expect(theirs).toBeVisible({ timeout: 20_000 });
  await expect(theirs).toHaveAttribute("aria-pressed", "false");

  // The seller adds their own: two emoji; the buyer changes theirs to the same: one chip, two.
  await react(seller, "Rice is back", "❤️");
  await expect(
    bubble(seller, "Rice is back").getByRole("button", { name: /^❤️ 1/ }),
  ).toBeVisible();
  await react(buyer, "Rice is back", "❤️");
  await expect(
    bubble(buyer, "Rice is back").getByRole("button", { name: /^❤️ 2/ }),
  ).toBeVisible();
  await expect(
    bubble(buyer, "Rice is back").getByRole("button", { name: /^👍/ }),
  ).toHaveCount(0); // theirs was changed, not added to

  // Tapping your own chip takes it back; the other's stays.
  await bubble(buyer, "Rice is back")
    .getByRole("button", { name: /^❤️ 2/ })
    .click();
  await expect(
    bubble(buyer, "Rice is back").getByRole("button", { name: /^❤️ 1/ }),
  ).toBeVisible();
  await expect(
    bubble(buyer, "Rice is back").getByRole("button", { name: /^❤️ 1/ }),
  ).toHaveAttribute("aria-pressed", "false");
  // The chip changes before the server has answered: wait for the server.
  await expect
    .poll(
      async () =>
        (
          await (
            await buyer.request.get(`/v1/link-chats/${link}/messages`, {
              headers: H,
            })
          ).json()
        ).messages[0].reactions,
    )
    .toEqual([{ emoji: "❤️", count: 1, by: ["Dangote"] }]);
});

test("a reaction that cannot be given is taken off again, with the reason", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await seller.request.post(`/v1/link-chats/${link}/messages`, {
    headers: H,
    data: { text: "Rice is back in stock" },
  });
  await buyer.route(`**/v1/link-chats/${link}/messages/*/reaction`, (route) =>
    route.abort("connectionfailed"),
  );
  await buyer.goto(`/chat?with=${link}`);
  await react(buyer, "Rice is back", "😂");
  await expect(
    buyer
      .getByTestId("chat-pane")
      .getByText(/no connection, so that was not done/),
  ).toBeVisible();
  await expect(
    bubble(buyer, "Rice is back").getByRole("button", { name: /^😂/ }),
  ).toHaveCount(0);
});

test("a star stays on when a copy read before it was saved arrives after", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  for (const t of ["Rice is back in stock", "The price is 45000"])
    await seller.request.post(`/v1/link-chats/${link}/messages`, {
      headers: H,
      data: { text: t },
    });
  // The live stream is held: opening the conversation marks it read, and
  // the stream's next look carries those messages as they were then.
  let stream: ((body: string) => Promise<void>) | undefined;
  await buyer.route("**/v1/chat/stream", (route) => {
    if (stream) return route.continue();
    stream = (body) => route.fulfill({ status: 200, headers: { "Content-Type": "text/event-stream" }, body });
  });
  await buyer.goto(`/chat?with=${link}`);
  await expect(bubble(buyer, "The price is 45000")).toBeVisible();
  const before = (await (await buyer.request.get(`/v1/link-chats/${link}/messages`, { headers: H })).json()) as {
    messages: { body?: string; starred?: boolean }[];
  };
  const stale = before.messages.find((m) => m.body === "The price is 45000");
  expect(stale?.starred ?? false).toBe(false);

  await bubble(buyer, "The price is 45000").getByRole("button", { name: /^More/ }).click();
  await buyer.getByTestId("chat-more").getByRole("button", { name: "Star" }).click();
  const star = bubble(buyer, "The price is 45000").getByRole("img", { name: "Starred" });
  await expect(star).toBeVisible();
  await expect
    .poll(async () => {
      const r = await buyer.request.get(`/v1/link-chats/${link}/messages`, { headers: H });
      return ((await r.json()) as { messages: { body?: string; starred?: boolean }[] }).messages.find((m) => m.body === "The price is 45000")?.starred;
    })
    .toBe(true);
  // Now the stream's copy, read before the star was saved, arrives.
  await expect.poll(() => !!stream).toBe(true);
  await stream?.(`event: ready\ndata: {}\n\nid: 1\nevent: message\ndata: ${JSON.stringify({ message: stale })}\n\n`);
  // It stays on throughout, not taken off until a later reload puts it back.
  const counts: number[] = [];
  for (let i = 0; i < 30; i++) {
    counts.push(await star.count());
    await buyer.waitForTimeout(100);
  }
  expect(counts).not.toContain(0);
});

test("a star is yours alone: marked in the thread, listed under Starred, opened at the message", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  for (const t of ["Rice is back in stock", "The price is 45000", "On its way"])
    await seller.request.post(`/v1/link-chats/${link}/messages`, {
      headers: H,
      data: { text: t },
    });
  await buyer.goto(`/chat?with=${link}`);
  await expect(bubble(buyer, "On its way")).toBeVisible();

  // Star two, from the menu.
  for (const t of ["The price is 45000", "Rice is back"]) {
    await bubble(buyer, t).getByRole("button", { name: /^More/ }).click();
    await buyer
      .getByTestId("chat-more")
      .getByRole("button", { name: "Star" })
      .click();
    await expect(
      bubble(buyer, t).getByRole("img", { name: "Starred" }),
    ).toBeVisible();
  }
  // The menu now offers Unstar for it.
  await bubble(buyer, "Rice is back")
    .getByRole("button", { name: /^More/ })
    .click();
  await expect(
    buyer.getByTestId("chat-more").getByRole("button", { name: "Unstar" }),
  ).toBeVisible();
  await buyer.keyboard.press("Escape");

  // Not the seller's.
  await seller.goto(`/chat?with=${link}`);
  await expect(bubble(seller, "Rice is back")).toBeVisible();
  await expect(seller.getByRole("img", { name: "Starred" })).toHaveCount(0);

  // The Starred view: most recent first; a row opens the conversation at it.
  await buyer.getByRole("button", { name: "Starred" }).click();
  const rows = buyer.getByTestId("starred-message");
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText("Rice is back");
  await expect(rows.nth(1)).toContainText("The price is 45000");
  await axeBoth(buyer, "the starred messages");
  await rows.nth(1).click();
  await expect(
    buyer
      .getByTestId("chat-pane")
      .locator('[data-testid="chat-message"].flash'),
  ).toContainText("The price is 45000");

  // Unstarred: gone from the view.
  await bubble(buyer, "The price is 45000")
    .getByRole("button", { name: /^More/ })
    .click();
  await buyer
    .getByTestId("chat-more")
    .getByRole("button", { name: "Unstar" })
    .click();
  await buyer.getByRole("button", { name: "Starred" }).click();
  await expect(buyer.getByTestId("starred-message")).toHaveCount(1);
});

test("Info shows when a message was sent and read, and who reacted", async ({
  browser,
}) => {
  const { seller, buyer, link } = await linked(browser);
  await buyer.goto(`/chat?with=${link}`);
  await buyer
    .getByTestId("chat-pane")
    .getByLabel("Message")
    .fill("Ten bags of rice");
  await buyer.getByTestId("chat-pane").getByLabel("Message").press("Enter");
  await expect(
    bubble(buyer, "Ten bags").getByRole("img", { name: "Delivered" }),
  ).toBeVisible();
  await bubble(buyer, "Ten bags")
    .getByRole("button", { name: /^More/ })
    .click();
  await buyer
    .getByTestId("chat-more")
    .getByRole("button", { name: "Info" })
    .click();
  const sheet = buyer.getByRole("dialog", { name: "Message info" });
  await expect(sheet.getByTestId("chat-info-times")).toContainText("Sent");
  await expect(sheet.getByTestId("chat-info-times")).toContainText("Not yet"); // not read by them
  await axeBoth(buyer, "the message info");
  await sheet.getByRole("button", { name: "Close" }).click();

  // The seller opens it: read, and reacts; the buyer's Info says so.
  await seller.goto(`/chat?with=${link}`);
  await expect(bubble(seller, "Ten bags")).toBeVisible();
  await react(seller, "Ten bags", "🙏");
  await buyer.goto(`/chat?with=${link}`);
  await bubble(buyer, "Ten bags")
    .getByRole("button", { name: /^More/ })
    .click();
  await buyer
    .getByTestId("chat-more")
    .getByRole("button", { name: "Info" })
    .click();
  const again = buyer.getByRole("dialog", { name: "Message info" });
  await expect(again.getByTestId("chat-info-times")).not.toContainText(
    "Not yet",
  );
  await expect(again).toContainText("🙏 1: Dangote");
});
