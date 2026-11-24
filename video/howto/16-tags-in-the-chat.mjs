// How-to 16: tags in the chat. In her chat with Bayo Rice Mills, Ada tags
// one of his items with #, a product with @, and his bill with /bill;
// each goes as a card both sides see. In her Taskiem chat, @ finds a
// business she does not trade with yet.
//
//   node video/howto/16-tags-in-the-chat.mjs          # record, then build
//   node video/howto/16-tags-in-the-chat.mjs build    # build only
//
// Wants a fresh world (video/howto/world.sh): Bayo Rice Mills is Ada's
// supplier, with a price list and a bill to pay.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "16-tags-in-the-chat";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});
const ADA = "Ada · Ada's Kitchen, the owner";

async function bayo() {
  const page = await person("ada");
  await page.goto(BASE + "/chat");
  await later(page);
  await page.getByRole("link", { name: /^Bayo Rice Mills/ }).first().click();
  const pane = page.getByTestId("chat-pane");
  await pane.waitFor();
  await page.waitForTimeout(1200);
  return { page, pane, box: pane.getByLabel(/^Message/).first() };
}

const picker = (page) => page.getByTestId("mention-list").first();

// 1 · # an item.
async function hash() {
  const { page, box } = await bayo();
  await act(page, "howto16-hash", ADA, async () => {
    await say("In her chat with Bayo Rice Mills. # tags one of his items: type # and a few letters.", 300);
    await box.click();
    await box.pressSequentially("Do you have #bas", { delay: 90 });
    await picker(page).waitFor({ timeout: 8000 });
    await sleep(1200);
    await say("His items, at his price for her, and the least he sells.", 2600);
    await picker(page).getByRole("button", { name: /Basmati rice/ }).first().click();
    await sleep(700);
    await box.pressSequentially(" this week?", { delay: 70 });
    await sleep(600);
    await box.press("Enter");
    await sleep(3400);
    await say("It goes as a card for the item, at his price, with Order: neither side has to guess which.", 3400);
  });
  await page.context().close();
}

// 2 · @ a product: a live card to order from.
async function at() {
  const { page, pane, box } = await bayo();
  await act(page, "howto16-at", ADA, async () => {
    await say("@ tags a thing or a business: his products and services together.", 300);
    await box.click();
    await box.pressSequentially("Need 20 more @ri", { delay: 80 });
    await picker(page).waitFor({ timeout: 8000 });
    await sleep(2400);
    await picker(page).getByRole("button", { name: /Rice, 25kg bag/ }).first().click();
    await sleep(800);
    await box.press("Enter");
    const card = pane.getByRole("article", { name: /Rice, 25kg bag/ }).last();
    await card.waitFor({ timeout: 10000 });
    await card.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(1200);
    await say("Her own stock shows on the card too: 4 bags left of the rice she needs.", 3800);
  });
  await page.context().close();
}

// 3 · / a bill, an order, an invoice.
async function slash() {
  const { page, pane, box } = await bayo();
  await act(page, "howto16-slash", ADA, async () => {
    await say("/ tags a bill, an order or an invoice. /bill shows Bayo's bills.", 300);
    await box.click();
    await box.pressSequentially("Paying this today /bill ", { delay: 80 });
    await picker(page).waitFor({ timeout: 8000 });
    await sleep(2200);
    await picker(page).getByRole("button").first().click();
    await sleep(900);
    await box.press("Enter");
    await sleep(1400);
    await pane.getByText("Paying this today").last().evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "end" }));
    await sleep(800);
    await say("The bill goes with the message: its number, amount and whether it is paid.", 4200);
  });
  await page.context().close();
}

// 4 · @ in the Taskiem chat finds a business too.
async function find() {
  const page = await person("ada");
  await page.goto(BASE + "/chat?with=taskiem");
  await later(page);
  await page.waitForTimeout(1500);
  const box = page.getByLabel(/^Message|Ask Taskiem/).first();
  await act(page, "howto16-find", ADA, async () => {
    await say("In her Taskiem chat, @ with a few letters looks among her products and businesses.", 300);
    await box.click();
    await box.pressSequentially("@print", { delay: 110 });
    await picker(page).waitFor({ timeout: 8000 });
    await sleep(2600);
    await say("Even businesses she does not trade with yet: their card, to connect.", 300);
    await picker(page).getByRole("button", { name: /Akoka Print Hub/ }).first().click();
    await page.getByRole("button", { name: /^Connect/ }).first().waitFor({ timeout: 10000 });
    await sleep(3600);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *tag things in the chat.*" },
  { title: "*#* an item." },
  { act: "howto16-hash" },
  { title: "*@* a product." },
  { act: "howto16-at" },
  { title: "*/* a bill or an order." },
  { act: "howto16-slash" },
  { title: "*@* a business." },
  { act: "howto16-find" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { hash, at, slash, find };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
