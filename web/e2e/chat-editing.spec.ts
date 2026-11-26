import { expect, test, type Browser, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Editing a sent message (docs/spec/chat-previews-and-editing.md §3.2), in
// a conversation between linked businesses: Edit from the message's menu
// puts its words in the box with the "Editing message" strip; Enter saves,
// Escape cancels; the up arrow in an empty box edits our last message; the
// bubble says "Edited", on the other side too, live; refused after its 30
// minutes, the words stay in the box. The server's rules are tested in Go.
// The page's Content-Security-Policy is bypassed only so axe can be added.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@edits.test`, name: `${tenant.split(" ")[0]} Owner`, password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

/** Two linked businesses: the seller writes, the buyer reads. */
async function linked(browser: Browser) {
  const stamp = letters();
  const seller = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
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

/** Opens a bubble's menu and presses Edit. */
async function editFromMenu(page: Page, words: string) {
  const bubble = page.getByTestId("chat-pane").locator('[data-testid="chat-message"]').filter({ hasText: words });
  await bubble.hover();
  await bubble.getByRole("button", { name: /^More for/ }).click();
  await bubble.getByTestId("chat-more").getByRole("button", { name: "Edit" }).click();
}

test("Edit from the menu, Enter saves, Escape cancels, the up arrow, and Edited on both sides", async ({ browser }) => {
  const { seller, buyer, link } = await linked(browser);
  const sent = await seller.request.post(`/v1/link-chats/${link}/messages`, { headers: H, data: { text: "Rice is 45,000 a bag" } });
  expect(sent.status()).toBe(201);
  await buyer.goto(`/chat?with=${link}`);
  await expect(buyer.getByTestId("chat-pane")).toContainText("Rice is 45,000 a bag");
  await seller.goto(`/chat?with=${link}`);
  const pane = seller.getByTestId("chat-pane");
  const box = pane.getByLabel("Message");

  // Edit from the menu: the words in the box, the strip with the old words.
  await editFromMenu(seller, "Rice is 45,000 a bag");
  const strip = seller.getByTestId("chat-editing");
  await expect(strip).toContainText("Editing message");
  await expect(strip).toContainText("Rice is 45,000 a bag");
  await expect(box).toHaveValue("Rice is 45,000 a bag");
  await expect(box).toBeFocused();
  await axeBoth(seller, "editing a message");
  await fits(seller, "editing a message");

  // Escape cancels: nothing saved.
  await box.press("Escape");
  await expect(strip).toHaveCount(0);
  await expect(box).toHaveValue("");

  // The up arrow in the empty box edits our last message; Enter saves.
  await box.press("ArrowUp");
  await expect(strip).toBeVisible();
  await box.fill("Rice is 44,000 a bag");
  await box.press("Enter");
  await expect(strip).toHaveCount(0);
  const mine = pane.locator('[data-testid="chat-message"]').filter({ hasText: "Rice is 44,000 a bag" });
  await expect(mine.getByTestId("chat-edited")).toHaveText("Edited");
  await expect(pane).not.toContainText("Rice is 45,000 a bag");
  await axeBoth(seller, "an edited message");

  // The other side sees it changed, marked Edited, without reloading.
  const theirs = buyer.getByTestId("chat-pane").locator('[data-testid="chat-message"]').filter({ hasText: "Rice is 44,000 a bag" });
  await expect(theirs.getByTestId("chat-edited")).toBeVisible({ timeout: 15_000 });
  await axeBoth(buyer, "an edited message, their side");
  await fits(buyer, "an edited message, their side");

  // Their message has no Edit.
  const bubble = buyer.getByTestId("chat-pane").locator('[data-testid="chat-message"]').filter({ hasText: "Rice is 44,000 a bag" });
  await bubble.hover();
  await bubble.getByRole("button", { name: /^More for/ }).click();
  await expect(bubble.getByTestId("chat-more")).toBeVisible();
  await expect(bubble.getByTestId("chat-more").getByRole("button", { name: "Edit" })).toHaveCount(0);
});

test("an edit refused after its 30 minutes keeps the words in the box", async ({ browser }) => {
  const { seller, link } = await linked(browser);
  await seller.request.post(`/v1/link-chats/${link}/messages`, { headers: H, data: { text: "Delivery on Monday" } });
  // The server's clock has moved past the 30 minutes: stood in for.
  await seller.route(`**/v1/link-chats/${link}/messages/*`, (route) =>
    route.request().method() === "PATCH"
      ? route.fulfill({
          status: 403,
          contentType: "application/json",
          json: { error: "This message can no longer be edited: messages can be edited for 30 minutes after they are sent.", not_editable: "too_old" },
        })
      : route.continue(),
  );
  await seller.goto(`/chat?with=${link}`);
  const pane = seller.getByTestId("chat-pane");
  const box = pane.getByLabel("Message");
  await editFromMenu(seller, "Delivery on Monday");
  await box.fill("Delivery on Tuesday");
  await seller.getByTestId("chat-save-edit").click();
  // The bubble goes back, the words stay in the box to send as new.
  await expect(seller.getByText(/can no longer be edited/).first()).toBeVisible();
  await expect(pane.locator('[data-testid="chat-message"]').filter({ hasText: "Delivery on Monday" })).toBeVisible();
  await expect(pane.locator('[data-testid="chat-message"]').filter({ hasText: "Delivery on Monday" }).getByTestId("chat-edited")).toHaveCount(0);
  await expect(box).toHaveValue("Delivery on Tuesday");
  await expect(seller.getByTestId("chat-editing")).toHaveCount(0);
});
