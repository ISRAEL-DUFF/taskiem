// How-to 11: invoices and matching payments. Ada sends Chinedu an
// invoice for the office lunch from the Invoices tool in the Taskiem chat;
// his transfer came in without naming it, so she matches it with Money.
//
//   node video/howto/11-invoices-and-payments.mjs          # record, then build
//   node video/howto/11-invoices-and-payments.mjs build    # build only
//   node video/howto/11-invoices-and-payments.mjs match    # one act again, then build
//
// Wants a fresh world (video/howto/world.sh); it needs nothing from the
// other how-tos.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "11-invoices-and-payments";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

// Off camera: what needs Ada now is put off till later, so the
// conversation is what shows.
async function quiet(page) {
  const off = page.getByTestId("chat-alert").getByRole("button", { name: /^Later/ });
  for (let i = 0; i < 12 && (await off.count()) > 0; i++) {
    await off.first().click();
    await page.waitForTimeout(500);
  }
}

async function taskiem(who) {
  const page = await person(who);
  await page.goto(BASE + "/chat?with=taskiem");
  await later(page);
  await page.waitForTimeout(1500);
  await quiet(page);
  // The briefing is put away too: Got it.
  await page.getByTestId("briefing-card").getByRole("button", { name: /^Got it/ }).click({ timeout: 2000 }).catch(() => {});
  await page.waitForTimeout(800);
  return page;
}

const tool = async (page, name) => {
  await page.getByTestId("tools-button").click();
  await page.getByRole("region", { name: "Tools" }).getByRole("button", { name: new RegExp("^" + name) }).click();
  const sheet = page.getByTestId("tool-sheet");
  await sheet.waitFor();
  return sheet;
};

const type = async (field, text, delay = 70) => {
  await field.scrollIntoViewIfNeeded();
  await field.click();
  await field.pressSequentially(text, { delay });
};

// 1 · An invoice, sent.
async function send() {
  const page = await taskiem("ada");
  await act(page, "howto11-send", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Chinedu's office had lunch from Ada's Kitchen. In the chat's tools: Invoices.", 600);
    const sheet = await tool(page, "Invoices");
    await sleep(2000);
    await say("What she is owed, what is late, and drafts. New invoice.", 600);
    await sheet.getByRole("button", { name: "New invoice" }).click();
    await sheet.getByLabel("Customer's name").waitFor();
    await sleep(600);
    await say("Who it is for, their phone number, what it is for and how much.", 400);
    await type(sheet.getByLabel("Customer's name"), "Chinedu Okonkwo");
    await type(sheet.getByLabel("Their phone number"), "0803 555 0177", 60);
    await type(sheet.getByLabel("What it is for"), "Office lunch, 15 plates", 50);
    await type(sheet.getByLabel("Amount (₦)"), "37500", 120);
    await sleep(1000);
    await say("Send it texts it to his phone, with a link to see it and pay. Keep as draft keeps it for later.", 2600);
    await sheet.getByRole("button", { name: "Send it" }).click();
    await sheet.waitFor({ state: "detached", timeout: 15000 });
    await sleep(200);
    await say("The chat keeps a line of it.", 3200);
  });
  await page.context().close();
}

// 2 · A payment matched to it.
async function match() {
  const page = await taskiem("ada");
  await act(page, "howto11-match", "Ada · Ada's Kitchen, the owner", async () => {
    await say("Chinedu paid by transfer, but it named no invoice. Money, in the same tools.", 600);
    const sheet = await tool(page, "Money");
    await sleep(1600);
    await say("To match shows what came in that is not tied to anything yet.", 600);
    await sheet.getByRole("group", { name: "Show" }).getByRole("button", { name: /^To match/ }).click();
    await sleep(2000);
    await sheet.getByRole("button", { name: /37,500/ }).first().click();
    await sheet.getByTestId("money-hero").waitFor();
    await sleep(1200);
    await say("₦37,500 from OKONKWO C, for lunch. Match to an invoice.", 600);
    await sheet.getByRole("button", { name: "Match to an invoice" }).click();
    const pick = sheet.getByLabel("The invoice it pays");
    await pick.waitFor();
    await page.waitForFunction(() => !document.querySelector("select")?.disabled);
    await sleep(800);
    const opt = await pick.locator("option", { hasText: "37,500" }).first().getAttribute("value");
    await pick.selectOption(opt);
    await sleep(1800);
    await say("Chinedu's invoice, ₦37,500 to pay. Match it.", 1200);
    await sheet.getByRole("button", { name: "Match it" }).click();
    await sheet.waitFor({ state: "detached", timeout: 15000 });
    await sleep(200);
    await say("The invoice is paid, and the chat says so.", 3400);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *send an invoice, and match the payment.*" },
  { title: "1. Send *an invoice.*" },
  { act: "howto11-send" },
  { title: "2. Match *the payment.*" },
  { act: "howto11-match" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { send, match };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
