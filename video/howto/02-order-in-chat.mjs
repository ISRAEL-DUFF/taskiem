// How-to 2: an order in a business chat, from both sides. Picks up where
// how-to 1 ends, with Ada's order to Grace Catering Supplies placed: Grace
// accepts it in the chat, gets it ready, sends it and records the
// delivery; Ada follows it on the cards, says All came, and the invoice
// arrives in the chat as her bill.
//
//   node video/howto/02-order-in-chat.mjs           # record every act, then build
//   node video/howto/02-order-in-chat.mjs build     # build from acts already recorded
//   node video/howto/02-order-in-chat.mjs received  # record one act again, then build
//
// Wants the world how-to 1 leaves: a freshly seeded demo world, then
// node video/howto/01-link-and-price-list.mjs.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "02-order-in-chat";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

async function open(who, chat) {
  const page = await person(who);
  await page.goto(BASE + "/chat");
  await later(page);
  await page.waitForTimeout(1000);
  return { page, chat: page.getByRole("link", { name: chat }).first(), pane: page.getByTestId("chat-pane") };
}

// 1 · Grace accepts Ada's order in the chat.
async function accept() {
  const { page, chat, pane } = await open("grace", /^Ada's Kitchen/);
  await act(page, "howto02-accept", "Grace · Grace Catering Supplies, the seller", async () => {
    await say("Ada's Kitchen has sent Grace an order. Her chats show it as a new message.", 3000);
    await chat.click();
    await pane.waitFor();
    await sleep(1400);
    await say("The order card has what it comes to, and how far it has got.", 3000);
    await say("Grace taps Accept. If she can't fill it, Decline asks her why, and Ada is told.", 1600);
    await pane.getByRole("button", { name: /^Accept/ }).first().click();
    await pane.getByText(/You accepted order/).waitFor({ timeout: 15000 });
    await sleep(800);
    await say("Accepted. Ada's card says so too.", 3000);
  });
  await page.context().close();
}

// 2 · Ada sees it accepted.
async function seen() {
  const { page, chat, pane } = await open("ada", /^Grace Catering/);
  await act(page, "howto02-seen", "Ada · Ada's Kitchen, the buyer", async () => {
    await say("On Ada's side, each step Grace takes arrives in their chat.", 1200);
    await chat.click();
    await pane.waitFor();
    await sleep(1600);
    await say("Accepted: the bar under the order moves along. Placed, Accepted, On the way, Delivered.", 4200);
  });
  await page.context().close();
}

// 3 · Grace gets it ready, sends it, and records the delivery.
async function fill() {
  const { page, chat, pane } = await open("grace", /^Ada's Kitchen/);
  await act(page, "howto02-fill", "Grace · Grace Catering Supplies, the seller", async () => {
    await say("To move the order along, Grace opens it from the card.", 1000);
    await chat.click();
    await pane.waitFor();
    await sleep(1000);
    await pane.getByText("View order").last().click();
    await page.waitForURL(/trade-orders/);
    await sleep(1400);
    await say("Its page: what Ada ordered and at what price.", 2600);
    const steps = page.getByRole("button", { name: "It is ready", exact: true });
    await steps.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(1200);
    await say("Packed? Tap It is ready.", 900);
    await steps.click();
    await page.getByText("Ready", { exact: true }).first().waitFor();
    await sleep(1600);
    await say("On the road? Sent on its way.", 900);
    await page.getByRole("button", { name: "Sent on its way", exact: true }).click();
    await page.getByText("On its way", { exact: true }).first().waitFor();
    await sleep(1600);
    await say("Handed over? Delivered, with how many of each went.", 900);
    await page.getByRole("button", { name: "Delivered", exact: true }).click();
    const form = page.getByRole("dialog");
    await form.waitFor();
    await sleep(1800);
    await say("Ada then says All came, or Not all came. If she says nothing for a day, Grace's count stands.", 3800);
    await form.getByRole("button", { name: "Record the delivery" }).click();
    await form.waitFor({ state: "detached" });
    await sleep(1200);
    const terms = page.getByText("Raise the invoice");
    await terms.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "center" }));
    await sleep(1000);
    await say("Lower down, Ada's Kitchen's terms: the invoice is raised once the delivery is received, or when Grace accepts. Days to pay, and a credit limit.", 5200);
  });
  await page.context().close();
}

// 4 · Ada says All came; the invoice arrives.
async function received() {
  const { page, chat, pane } = await open("ada", /^Grace Catering/);
  await act(page, "howto02-received", "Ada · Ada's Kitchen, the buyer", async () => {
    await say("Back with Ada: Grace has said it is delivered.", 1000);
    await chat.click();
    await pane.waitFor();
    await sleep(1200);
    const all = pane.getByRole("button", { name: /^All came/ });
    await all.scrollIntoViewIfNeeded();
    await sleep(800);
    await say("Everything arrived, so Ada taps All came. If something is missing, she opens the order and says Not all came.", 4000);
    await all.click();
    await sleep(600);
    await say("Grace's invoice is raised by itself…", 600);
    const invoice = pane.getByText(/sent invoice/).last();
    await invoice.waitFor({ timeout: 30000 });
    await sleep(500);
    await invoice.scrollIntoViewIfNeeded();
    await sleep(800);
    await say("…and lands in the chat as Ada's bill, with Pay on it. Paying a bill is the next video.", 4600);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *an order in the chat.*" },
  { title: "1. The seller *accepts.*" },
  { act: "howto02-accept" },
  { act: "howto02-seen" },
  { title: "2. Ready, sent, *delivered.*" },
  { act: "howto02-fill" },
  { title: "3. The buyer *says it came.*" },
  { act: "howto02-received" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { accept, seen, fill, received };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
