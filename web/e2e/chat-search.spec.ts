import { expect, test, type Page } from "@playwright/test";
import { axe, describe } from "./a11y";

// Searching Chat as WhatsApp does: the list narrows from the first letter;
// the chat found opens when pressed; from two letters, other businesses and
// messages are looked for too. At a phone's width, passing axe.
test.use({ bypassCSP: true, viewport: { width: 390, height: 844 } });

const H = { "X-Taskiem-Request": "1" };
const letters = () => String(Date.now()).replace(/\d/g, (d) => "abcdefghij".charAt(Number(d)));

async function signUp(page: Page, tenant: string) {
  const r = await page.request.post("/v1/signup", {
    headers: H,
    data: { tenant, email: `${letters()}-${Math.random().toString(36).slice(2, 8)}@chat.test`, name: "Owner", password: "correct horse battery" },
  });
  expect(r.status(), await r.text()).toBe(201);
}

test("the chat list narrows as the person types", async ({ browser }) => {
  const stamp = letters();
  const seller = await (await browser.newContext()).newPage();
  await signUp(seller, `Dangote Depot ${stamp}`);
  const other = await (await browser.newContext()).newPage();
  await signUp(other, `Danfo Bakery ${stamp}`);
  const buyer = await (await browser.newContext()).newPage();
  await signUp(buyer, `Ada Kitchen ${stamp}`);
  for (const p of [seller, other]) expect((await p.request.put("/v1/link-settings", { headers: H, data: { findable: true } })).status()).toBe(200);
  const found = await (await buyer.request.get(`/v1/links/search?q=${encodeURIComponent(`dangote depot ${stamp}`)}`, { headers: H })).json();
  expect((await buyer.request.post("/v1/links", { headers: H, data: { business: found.results[0].business, role: "supplier" } })).status()).toBe(201);
  const link = (await (await seller.request.get("/v1/links", { headers: H })).json()).links[0].id as string;
  expect((await seller.request.post(`/v1/links/${link}/accept`, { headers: H })).status()).toBe(200);
  expect((await seller.request.post(`/v1/link-chats/${link}/messages`, { headers: H, data: { text: "Rice is back in stock" } })).status()).toBe(201);

  await buyer.goto("/chat");
  const box = buyer.getByTestId("link-search");
  await expect(buyer.getByTestId("link-linked")).toHaveCount(1);

  // One letter: the chat that fits stays, found in the results at once; the
  // Taskiem conversation, which does not fit "d", steps aside.
  await box.pressSequentially("d");
  await expect(buyer.getByTestId("link-results")).toBeVisible();
  await expect(buyer.getByTestId("link-linked")).toHaveCount(1);
  await expect(buyer.getByTestId("link-linked")).toContainText("Dangote Depot");
  await expect(buyer.getByTestId("taskiem-chat")).toHaveCount(0);
  // A letter that fits nothing says so, and does not look for businesses yet.
  await box.fill("q");
  await expect(buyer.getByTestId("link-results")).toContainText("No chat matches");

  // Two letters: the other business that fits is offered to link with.
  await box.fill("");
  await box.pressSequentially("da");
  await expect(buyer.getByTestId("link-linked")).toContainText("Dangote Depot");
  await expect(buyer.getByTestId("link-found").filter({ hasText: `Danfo Bakery ${stamp}` })).toHaveCount(0);
  await box.pressSequentially("n");
  await expect(buyer.getByTestId("link-found").filter({ hasText: `Danfo Bakery ${stamp}` })).toBeVisible();
  await axe(buyer).then((vs) => expect(vs, describe("chat search", vs)).toEqual([]));

  // A word from the last message finds the chat and the message.
  await box.fill("rice");
  await expect(buyer.getByTestId("link-linked")).toContainText("Dangote Depot");
  await expect(buyer.getByTestId("chat-found-message")).toContainText("Rice is back in stock");

  // Pressing the chat found opens the conversation.
  await buyer.getByTestId("link-linked").click();
  await expect(buyer.getByTestId("chat-pane")).toContainText("Rice is back in stock");
});
