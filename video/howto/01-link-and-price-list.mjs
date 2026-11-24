// How-to 1: link with a supplier, and the supplier shares a price list.
// Ada's Kitchen asks Grace Catering Supplies to link; Grace accepts, makes
// a price list from her stock with her own trade price, and shares it with
// Ada; Ada orders from it in their chat.
//
//   node video/howto/01-link-and-price-list.mjs          # record every act, then build
//   node video/howto/01-link-and-price-list.mjs build    # build from acts already recorded
//   node video/howto/01-link-and-price-list.mjs order    # record one act again, then build
//
// Wants a freshly seeded demo world (serve-demo.sh without --keep, then
// seed.ts): it links Ada and Grace and gives Grace a price list.
import { randomUUID } from "node:crypto";
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "01-link-and-price-list";
const H = { "X-Taskiem-Request": "1" };

// Off camera: Grace's stock (keeping stock is a how-to of its own) and her
// chat tools.
async function prepare() {
  const g = await person("grace");
  const call = async (method, url, data) => {
    const r = await g.request.fetch(BASE + url, { method, headers: { ...H, "Idempotency-Key": randomUUID() }, data });
    if (r.status() >= 300) throw new Error(`${method} ${url}: ${r.status()} ${await r.text()}`);
  };
  await call("PUT", "/v1/settings/chat-tools", { chat_tools: true });
  await call("POST", "/v1/stock/setup");
  for (const [name, quantity, unit, naira] of [
    ["Chafing dish, 9 litre", 40, "piece", 18500],
    ["Cooler, 45 litre", 25, "piece", 32000],
    ["Foil trays, pack of 50", 200, "pack", 7500],
    ["Food warmer, 3 pans", 12, "piece", 65000],
  ])
    await call("POST", "/v1/stock/items", { name, quantity, unit, price_kobo: naira * 100 });
  await g.context().close();
}

const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

// 1 · Ada finds Grace and asks to link.
async function ask() {
  const page = await person("ada");
  await page.goto(BASE + "/chat");
  await later(page);
  await page.waitForTimeout(1000);
  await act(page, "howto01-ask", "Ada · Ada's Kitchen, the buyer", async () => {
    await say("Ada runs Ada's Kitchen. She wants to buy from Grace Catering Supplies.", 3200);
    await say("In Chat, tap Find a business.", 1400);
    await page.getByTestId("new-chat").click();
    await sleep(1200);
    await say("Search for the business by name.", 600);
    await page.keyboard.type("Grace", { delay: 120 });
    await page.getByRole("button", { name: /^Connect/ }).first().waitFor();
    await sleep(2200);
    await say("Tap Connect. The request goes to Grace.", 1400);
    await page.getByRole("button", { name: /^Connect/ }).first().click();
    await sleep(2500);
    await say("The card now says Requested. Until Grace answers, Ada can withdraw it.", 3500);
  });
  await page.context().close();
}

// 2 · Grace accepts.
async function accept() {
  const page = await person("grace");
  await page.goto(BASE + "/chat");
  await later(page);
  await page.waitForTimeout(1000);
  await act(page, "howto01-accept", "Grace · Grace Catering Supplies, the seller", async () => {
    await say("Grace sees Ada's request at the top of her chats.", 3000);
    await say("She taps Accept.", 1200);
    await page.getByRole("button", { name: "Accept Ada's Kitchen" }).click();
    await page.getByRole("link", { name: /^Ada's Kitchen/ }).first().waitFor({ timeout: 15000 });
    await sleep(1200);
    await say("They are linked: Ada's Kitchen now has a chat of its own here, and Grace has one on Ada's side.", 4200);
  });
  await page.context().close();
}

// 3 · Grace makes a price list and shares it with Ada.
async function priceList() {
  const page = await person("grace");
  await page.goto(BASE + "/chat");
  await later(page);
  await page.waitForTimeout(1000);
  await act(page, "howto01-list", "Grace · Grace Catering Supplies, the seller", async () => {
    await say("A price list is what linked buyers see and order from. Grace makes one.", 3200);
    await say("Open Menu, then Storefront.", 1200);
    await page.getByRole("link", { name: "Menu" }).or(page.getByRole("button", { name: "Menu" })).first().click();
    await sleep(1400);
    await page.getByRole("link", { name: "Storefront", exact: true }).first().click();
    await later(page);
    await sleep(1600);
    await say("Tap Price lists.", 1000);
    await page.getByRole("link", { name: "Price lists" }).first().click();
    await later(page);
    await sleep(1600);
    await say("Name the list, then tap Make a price list.", 600);
    const name = page.getByPlaceholder("Trade prices");
    await name.click();
    await name.pressSequentially("Restaurants and caterers", { delay: 70 });
    await sleep(600);
    await page.getByRole("button", { name: "Make a price list" }).click();
    await page.getByTestId("price-list").first().waitFor();
    await sleep(1800);
    await say("Tap Items. Every item comes from Grace's own stock.", 1200);
    await page.getByRole("button", { name: /^Items of/ }).first().click();
    const modal = page.getByRole("dialog");
    await modal.waitFor();
    await sleep(1600);
    await say("Tick what to sell on this list. Each price starts at the stock price.", 800);
    for (const n of ["Chafing dish, 9 litre (piece)", "Cooler, 45 litre (piece)", "Foil trays, pack of 50 (pack)"]) {
      await modal.getByLabel(n).check();
      await sleep(900);
    }
    await sleep(1200);
    await say("Change a price for this list only. Here the chafing dish is ₦17,000 to trade buyers.", 800);
    const price = modal.getByRole("group", { name: "Chafing dish, 9 litre" }).getByLabel("Price (₦)");
    await price.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(600);
    await price.fill("");
    await price.pressSequentially("17000", { delay: 120 });
    await sleep(1800);
    await say("Save.", 900);
    await modal.getByRole("button", { name: "Save" }).click();
    await modal.waitFor({ state: "detached" });
    await sleep(1500);
    await say("Now tap Share and tick the businesses that get this list.", 1000);
    await page.getByRole("button", { name: /^Share/ }).first().click();
    const share = page.getByRole("dialog");
    await share.waitFor();
    await sleep(1500);
    await share.getByLabel("Ada's Kitchen").check();
    await sleep(1000);
    await say("Each buyer sees one list. Sharing this one replaces any list Ada had from Grace.", 3400);
    await share.getByRole("button", { name: "Save" }).click();
    await share.waitFor({ state: "detached" });
    await sleep(1000);
    await say("Shared with Ada's Kitchen. Change a price later and Ada sees it at once.", 3600);
  });
  await page.context().close();
}

// 4 · Ada orders from it in their chat.
async function order() {
  const page = await person("ada");
  await page.goto(BASE + "/chat");
  await later(page);
  await page.waitForTimeout(1000);
  await act(page, "howto01-order", "Ada · Ada's Kitchen, the buyer", async () => {
    await say("Back on Ada's side, open the chat with Grace.", 1200);
    await page.getByRole("link", { name: /^Grace Catering/ }).first().click();
    const pane = page.getByTestId("chat-pane");
    await pane.waitFor();
    await sleep(1500);
    await say("Open the tools and tap Order.", 1000);
    await pane.getByRole("button", { name: "Open tools" }).click();
    const tools = page.getByRole("region", { name: "Tools" });
    await tools.waitFor();
    await sleep(1500);
    await tools.getByRole("button", { name: /^Order/ }).first().click();
    const sheet = page.getByTestId("order-sheet");
    await sheet.waitFor();
    await sleep(1200);
    await say("Grace's list, at the prices Grace set for Ada: the chafing dish at ₦17,000.", 3600);
    await say("Add what you need.", 600);
    for (const [n, times] of [["Chafing dish, 9 litre", 4], ["Foil trays, pack of 50", 2]]) {
      for (let k = 0; k < times; k++) {
        await sheet.getByRole("button", { name: `One more ${n}` }).click();
        await sleep(450);
      }
      await sleep(600);
    }
    await sleep(1000);
    await say("Review the order, then place it.", 800);
    await sheet.getByRole("button", { name: "Review order" }).click();
    const place = sheet.getByRole("button", { name: /Place order|Send order/ }).first();
    await place.waitFor();
    await sleep(1600);
    await say("The first order from a new supplier asks two things. Their phone number, for Ada's record of them:", 600);
    const phone = sheet.getByLabel(/phone number/);
    await phone.click();
    await phone.pressSequentially("0803 555 0142", { delay: 90 });
    await sleep(1800);
    const which = sheet.getByText("Which of your items is each?");
    await which.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await say("…and which of Ada's own items each one is. New ones are added to her stock. Both are remembered.", 4400);
    await say("Place the order.", 900);
    const cards = await pane.getByTestId("chat-card").count();
    await place.click();
    await page.waitForFunction((n) => document.querySelectorAll('[data-testid="chat-pane"] [data-testid="chat-card"]').length > n, cards, { timeout: 15000 });
    await sleep(800);
    await pane.getByTestId("chat-card").last().scrollIntoViewIfNeeded();
    await say("The order lands in the chat, on both sides, for Grace to accept.", 4000);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *link with a supplier.*" },
  { title: "1. Ask *to link.*" },
  { act: "howto01-ask" },
  { title: "2. The supplier *accepts.*" },
  { act: "howto01-accept" },
  { title: "3. Make a price list *and share it.*" },
  { act: "howto01-list" },
  { title: "4. Order *from the list.*" },
  { act: "howto01-order" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { prepare, ask, accept, priceList, order };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
