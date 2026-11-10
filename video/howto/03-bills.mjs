// How-to 3: a bill. Picks up where how-to 2 ends, with Grace Catering
// Supplies' invoice INV-0001 (₦83,000) in Ada's chat as her bill: Ada
// questions it, Grace settles the query with an amount off, Ada pays the
// rest from her bank and says so, and Grace sees it.
//
//   node video/howto/03-bills.mjs           # record every act, then build
//   node video/howto/03-bills.mjs build     # build from acts already recorded
//   node video/howto/03-bills.mjs settle    # record one act again, then build
//
// Wants the world how-to 2 leaves: a freshly seeded demo world, then
// how-tos 1 and 2.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "03-bills";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

async function open(who, chat) {
  const page = await person(who);
  await page.goto(BASE + "/chat");
  await later(page);
  await page.waitForTimeout(1000);
  return { page, chat: page.getByRole("link", { name: chat }).first(), pane: page.getByTestId("chat-pane") };
}

// 1 · Ada opens the bill and questions it.
async function question() {
  const { page, chat, pane } = await open("ada", /^Grace Catering/);
  await act(page, "howto03-question", "Ada · Ada's Kitchen, the buyer", async () => {
    await say("Grace's invoice for the order is in their chat. On Ada's side it is a bill to pay.", 1200);
    await chat.click();
    await pane.waitFor();
    await sleep(1500);
    await say("View invoice opens the bill: what it is for, what is left to pay, and when it is due.", 1000);
    await pane.getByText("View invoice").last().click();
    const sheet = page.getByTestId("tool-sheet");
    await sheet.getByRole("button", { name: "Question it" }).waitFor();
    await sleep(3200);
    await say("Two of the chafing dishes came dented. Before paying, Ada taps Question it.", 1000);
    await sheet.getByRole("button", { name: "Question it" }).click();
    await sleep(1000);
    const why = sheet.getByRole("textbox");
    await why.click();
    await why.pressSequentially("Two of the chafing dishes came dented.", { delay: 55 });
    await sleep(800);
    await say("Grace is told. Until it is settled, it isn't paid from Taskiem.", 1000);
    await sheet.getByRole("button", { name: "Send the question" }).click();
    await pane.getByText("Queried").last().waitFor({ timeout: 15000 });
    await sleep(1200);
    await say("The bill shows Queried, on both sides.", 2600);
  });
  await page.context().close();
}

// 2 · Grace settles it: an amount off.
async function settle() {
  const { page, chat, pane } = await open("grace", /^Ada's Kitchen/);
  await act(page, "howto03-settle", "Grace · Grace Catering Supplies, the seller", async () => {
    await say("Grace sees the question in the chat, in Ada's words.", 1000);
    await chat.click();
    await pane.waitFor();
    await sleep(1400);
    const card = pane.getByText(/queried invoice/).last();
    await card.scrollIntoViewIfNeeded();
    await sleep(2400);
    await say("Settle the query opens the invoice.", 900);
    await pane.getByRole("link", { name: /^Settle the query/ }).last().click();
    await page.waitForURL(/invoices/);
    await later(page);
    const how = page.getByText("How do you settle it?");
    await how.waitFor();
    await how.evaluate((el) => el.scrollIntoView({ behavior: "smooth", block: "start" }));
    await sleep(1600);
    await say("She can say the invoice stands as it is, or take an amount off. Two dishes at ₦17,000: ₦34,000 off.", 1000);
    const sec = page.locator("section").filter({ hasText: "How do you settle it?" }).first();
    await sec.getByLabel("Take an amount off").check();
    await sleep(800);
    const amount = sec.getByLabel("Amount off (₦)");
    await amount.click();
    await amount.pressSequentially("34000", { delay: 110 });
    await sleep(500);
    const why = sec.getByLabel(/^Why/);
    await why.click();
    await why.pressSequentially("Two chafing dishes came dented", { delay: 50 });
    await sleep(1400);
    await say("The reason goes on the invoice, and to Ada.", 900);
    await sec.getByRole("button", { name: "Settle the query" }).click();
    await page.getByText("₦49,000").first().waitFor({ timeout: 15000 });
    await page.evaluate(() => window.scrollTo({ top: 0, behavior: "smooth" }));
    await sleep(1200);
    await say("The invoice is now ₦49,000. A sent invoice is never edited: the change is a line of its own.", 4000);
  });
  await page.context().close();
}

// 3 · Ada pays the rest from her bank and says so.
async function paid() {
  const { page, chat, pane } = await open("ada", /^Grace Catering/);
  await act(page, "howto03-paid", "Ada · Ada's Kitchen, the buyer", async () => {
    await say("Ada sees Grace's answer in the chat.", 900);
    await chat.click();
    await pane.waitFor();
    await sleep(1200);
    await pane.getByText(/settled the query/).last().scrollIntoViewIfNeeded();
    await sleep(2600);
    await pane.getByText("View invoice").last().click();
    const sheet = page.getByTestId("tool-sheet");
    await sheet.getByRole("button", { name: "I paid outside Taskiem" }).waitFor();
    await sleep(1000);
    await say("The bill is ₦49,000 now, with the ₦34,000 off as a line.", 2800);
    await say("Ada pays from her own bank app, then tells Grace: I paid outside Taskiem.", 1000);
    await sheet.getByRole("button", { name: "I paid outside Taskiem" }).click();
    await sleep(900);
    const amount = sheet.getByLabel(/What you paid outside/);
    await amount.click();
    await amount.pressSequentially("49000", { delay: 110 });
    await sleep(700);
    await say("How much, and how: by transfer, in cash, or another way.", 2200);
    await sheet.getByRole("button", { name: /^Tell / }).click();
    await pane.getByText("Paid, they say").last().waitFor({ timeout: 15000 });
    await sleep(1000);
    await say("The bill says Paid, they say, until Grace's money arrives.", 3000);
  });
  await page.context().close();
}

// 4 · Grace sees it; it counts when the money does.
async function told() {
  const { page, chat, pane } = await open("grace", /^Ada's Kitchen/);
  await act(page, "howto03-told", "Grace · Grace Catering Supplies, the seller", async () => {
    await say("Grace is told Ada says she paid ₦49,000.", 1000);
    await chat.click();
    await pane.waitFor();
    await sleep(1200);
    await pane.getByText(/said it paid/).last().scrollIntoViewIfNeeded();
    await sleep(2600);
    await say("It counts as paid only when money arrives. When the transfer reaches Grace's account, Taskiem matches it to INV-0001 (or Grace does, on To match), and both sides see Paid.", 5600);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *question a bill, and pay it.*" },
  { title: "1. Question *the bill.*" },
  { act: "howto03-question" },
  { title: "2. The seller *settles it.*" },
  { act: "howto03-settle" },
  { title: "3. Pay, *and say so.*" },
  { act: "howto03-paid" },
  { act: "howto03-told" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { question, settle, paid, told };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
