// How-to 14: deals between linked businesses. Ada sets a lower price for
// 50 or more small chops packs and her lowest price; Rivers Foods orders
// 50 and offers less for the whole order; Tunde counters; Rivers counters
// under Ada's lowest price; Tunde accepts, and it waits for Ada, who
// agrees from her Taskiem chat.
//
//   node video/howto/14-deals.mjs          # record, then build
//   node video/howto/14-deals.mjs build    # build only
//   node video/howto/14-deals.mjs agree    # one act again, then build
//
// Wants a fresh world (video/howto/world.sh): Rivers Foods is linked to
// Ada's Kitchen and orders from its price list there.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "14-deals";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
const ADA = "Ada · Ada's Kitchen, the owner";
const TUNDE = "Tunde · Ada's Kitchen, cashier";
const RIVERS = "Rivers Foods · a business that buys from Ada's Kitchen";

const center = (l) => l.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));

async function chatWith(who, name) {
  const page = await person(who);
  await page.goto(BASE + "/chat");
  await later(page);
  await page.getByRole("link", { name: new RegExp("^" + name) }).first().waitFor();
  await page.waitForTimeout(800);
  return page;
}
const dealCard = (page) => page.getByTestId("chat-pane").getByRole("article", { name: /^Order · / }).last();

async function openDeal(page, name) {
  await page.getByRole("link", { name: new RegExp("^" + name) }).first().click();
  const card = dealCard(page);
  await card.waitFor();
  await center(card);
  await sleep(1200);
  return card;
}

// 1 · Ada's quantity price and lowest price.
async function prices() {
  const page = await person("ada");
  await page.goto(BASE + "/storefront/price-lists");
  await later(page);
  await page.getByRole("button", { name: /^Items of/ }).first().waitFor();
  await page.waitForTimeout(800);
  await act(page, "howto14-prices", ADA, async () => {
    await say("Rivers Foods buys from Ada's Kitchen's price list. Ada opens its items.", 600);
    await page.getByRole("button", { name: /^Items of/ }).first().click();
    const modal = page.getByRole("dialog");
    await sleep(1000);
    const add = modal.getByRole("button", { name: "+ Lower price for more of Small chops pack" });
    await center(add);
    await sleep(800);
    await say("A lower price for more: from 50 small chops packs, ₦2,000 a pack.", 300);
    await add.click();
    await sleep(600);
    const brk = modal.getByRole("group", { name: "Small chops pack: quantity price 1" });
    await brk.getByLabel("From").pressSequentially("50", { delay: 120 });
    await brk.getByLabel(/^Price/).pressSequentially("2000", { delay: 120 });
    await sleep(1400);
    await modal.getByRole("button", { name: "Save" }).click();
    await modal.waitFor({ state: "detached" });
    await sleep(800);
    await say("And the lowest she will go, in the product, from the chat's tools.", 300);
    await page.goto(BASE + "/chat?with=taskiem");
    await page.getByTestId("taskiem-stream").waitFor();
    await sleep(800);
    await page.getByTestId("tools-button").click();
    await sleep(700);
    await page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Products/ }).click();
    const sheet = page.getByTestId("tool-sheet");
    await sleep(800);
    await sheet.getByPlaceholder(/Search/).first().pressSequentially("small", { delay: 90 });
    await sleep(600);
    await sheet.getByRole("button", { name: /Small chops pack/ }).first().click();
    await sleep(1000);
    const floor = sheet.getByLabel("Lowest price in naira");
    await center(floor);
    await sleep(800);
    await say("Lowest price: ₦1,900 a pack. Buyers never see it.", 300);
    await floor.click();
    await floor.pressSequentially("1900", { delay: 120 });
    await sleep(1400);
    await sheet.getByRole("button", { name: /^Save/ }).first().click();
    await page.getByText("Saved", { exact: true }).first().waitFor({ timeout: 15000 });
    await sleep(2400);
  });
  await page.context().close();
}

// 2 · Rivers orders 50 and makes an offer.
async function offer() {
  const page = await chatWith("rivers", "Ada's Kitchen");
  await act(page, "howto14-offer", RIVERS, async () => {
    await say("Rivers Foods orders from Ada's Kitchen in their chat: the tools, then Order.", 600);
    const pane = page.getByTestId("chat-pane");
    await page.getByRole("link", { name: /^Ada's Kitchen/ }).first().click();
    await pane.waitFor();
    await sleep(1000);
    await pane.getByRole("button", { name: "Open tools" }).click();
    await sleep(1000);
    await page.getByRole("region", { name: "Tools" }).getByRole("button", { name: /^Order/ }).first().click();
    const order = page.getByTestId("order-sheet");
    await order.waitFor();
    await sleep(800);
    await order.getByPlaceholder(/Search/).pressSequentially("small", { delay: 90 });
    await sleep(700);
    await say("Ada's Kitchen takes at least 20. Rivers wants 50: the price for 50 or more applies by itself.", 300);
    // The first + puts in the least Ada's Kitchen takes (20); then one at a time to 50.
    const more = order.getByRole("button", { name: /^One more Small chops pack/ });
    const row = more.locator("xpath=..");
    for (let i = 0; i < 60 && !/\b50\b/.test(await row.innerText()); i++) {
      await more.click();
      await sleep(i === 0 ? 900 : 90);
    }
    if (!/\b50\b/.test(await row.innerText())) throw new Error("not 50: " + (await row.innerText()));
    await sleep(1800);
    await order.getByRole("button", { name: "Review order" }).click();
    await sleep(1800);
    await say("Rivers would rather pay less: Make an offer, for the whole order.", 300);
    await order.getByRole("button", { name: "Make an offer" }).click();
    await sleep(800);
    await order.getByLabel("Your price for the whole order").pressSequentially("88k", { delay: 150 });
    await sleep(1500);
    await order.getByRole("button", { name: "Send offer" }).click();
    await dealCard(page).waitFor({ timeout: 15000 });
    await sleep(800);
    await center(dealCard(page));
    await say("The offer is a card in the chat, on both sides.", 3400);
  });
  await page.context().close();
}

// 3 · Tunde counters for Ada's Kitchen.
async function counter() {
  const page = await chatWith("tunde", "Rivers Foods");
  await act(page, "howto14-counter", TUNDE, async () => {
    await say("At Ada's Kitchen, Tunde sees the offer: ₦88,000 for the 50.", 300);
    const card = await openDeal(page, "Rivers Foods");
    await sleep(2000);
    await say("He can accept, decline or counter. He counters at ₦97,000.", 300);
    await card.getByRole("button", { name: /^Counter/ }).click();
    await sleep(800);
    await card.getByLabel(/Your price for the whole order|Your counter/).pressSequentially("97k", { delay: 150 });
    await sleep(400);
    await card.getByLabel(/A word with it/).pressSequentially("Best I can do for 50", { delay: 45 }).catch(() => {});
    await sleep(1000);
    await card.getByRole("button", { name: /^Send counter/ }).click();
    await page.getByText(/97,000/).last().waitFor({ timeout: 15000 });
    await sleep(3200);
  });
  await page.context().close();
}

// 4 · Rivers counters again, under Ada's lowest price.
async function again() {
  const page = await chatWith("rivers", "Ada's Kitchen");
  await act(page, "howto14-again", RIVERS, async () => {
    await say("Rivers counters back: ₦93,000.", 300);
    const card = await openDeal(page, "Ada's Kitchen");
    await sleep(1400);
    await card.getByRole("button", { name: /^Counter/ }).click();
    await sleep(800);
    await card.getByLabel(/Your counter|Your price for the whole order/).pressSequentially("93,000", { delay: 130 });
    await sleep(1000);
    await card.getByRole("button", { name: /^Send counter/ }).click();
    await page.getByText(/93,000/).last().waitFor({ timeout: 15000 });
    await sleep(3000);
  });
  await page.context().close();
}

// 5 · Tunde accepts; under the lowest price, it waits for Ada.
async function accept() {
  const page = await chatWith("tunde", "Rivers Foods");
  await act(page, "howto14-accept", TUNDE, async () => {
    await say("₦93,000 is under Ada's lowest price for 50. Tunde accepts it.", 300);
    const card = await openDeal(page, "Rivers Foods");
    await sleep(1800);
    await card.getByRole("button", { name: /^Accept ₦93,000/ }).click();
    await page.getByText(/waits for the owner|Needs .*OK|owner's OK/).first().waitFor({ timeout: 15000 });
    await sleep(800);
    await say("Below the lowest price, it waits for Ada's OK.", 3600);
  });
  await page.context().close();
}

// 6 · Ada agrees from her Taskiem chat.
async function agree() {
  const page = await person("ada");
  await page.goto(BASE + "/chat?with=taskiem");
  await later(page);
  await page.getByTestId("taskiem-stream").waitFor();
  const ask = page.getByTestId("chat-alert").filter({ hasText: /would agree/ }).last();
  await ask.waitFor({ timeout: 15000 });
  await center(ask);
  await page.waitForTimeout(1200);
  await act(page, "howto14-agree", ADA, async () => {
    await say("Ada's Taskiem chat asks her: Tunde would agree ₦93,000, under her lowest price. Agree, or Later.", 4200);
    await ask.getByRole("button", { name: /^(Agree|Approve)/ }).first().click();
    await page.getByText(/Agreed ₦93,000/).first().waitFor({ timeout: 15000 });
    await sleep(800);
    await say("Agreed: the deal stands at ₦93,000.", 4200);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *make a deal with a business.*" },
  { title: "1. Your *prices.*" },
  { act: "howto14-prices" },
  { title: "2. An *offer.*" },
  { act: "howto14-offer" },
  { title: "3. *Counter.*" },
  { act: "howto14-counter" },
  { act: "howto14-again" },
  { title: "4. *Agree.*" },
  { act: "howto14-accept" },
  { act: "howto14-agree" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { prices, offer, counter, again, accept, agree };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
