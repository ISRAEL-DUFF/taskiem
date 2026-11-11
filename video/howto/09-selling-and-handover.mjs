// How-to 9: selling at the counter, and handing the shift over. Tunde, a
// cashier, records a sale with the Sell tool in the Taskiem chat; Sam, the
// shift manager, hands the shift over at the end of the day, the cash
// counted against the cash sold.
//
//   node video/howto/09-selling-and-handover.mjs          # record, then build
//   node video/howto/09-selling-and-handover.mjs build    # build only
//   node video/howto/09-selling-and-handover.mjs handover # one act again, then build
//
// Wants a fresh world (video/howto/world.sh); it needs nothing from the
// other how-tos.
import { person, BASE, sleep } from "../recording/lib.mjs";
import { act, build, say } from "./howto.mjs";

const SLUG = "09-selling-and-handover";
const later = (page) => page.getByRole("button", { name: "Remind me later" }).click({ timeout: 3000 }).catch(() => {});

// Off camera: what needs them now is put off till later, so the
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
  return page;
}

const tools = async (page) => {
  await page.getByTestId("tools-button").click();
  return page.getByRole("region", { name: "Tools" });
};

// 1 · Tunde sells.
async function sell() {
  const page = await taskiem("tunde");
  await act(page, "howto09-sell", "Tunde · a cashier at Ada's Kitchen", async () => {
    await say("A customer at the counter wants two jollof rice and a zobo. Tunde opens Sell in the chat's tools.", 1000);
    await (await tools(page)).getByRole("button", { name: /^Sell/ }).click();
    const sheet = page.getByTestId("sell-sheet");
    await sheet.waitFor();
    await sleep(1600);
    await say("Everything Ada's Kitchen sells, at its price. + adds one.", 600);
    const more = (name) => sheet.getByRole("button", { name: `One more ${name}` });
    await more("Jollof rice").scrollIntoViewIfNeeded();
    await more("Jollof rice").click();
    await sleep(500);
    await more("Jollof rice").click();
    await sleep(700);
    await more("Zobo").scrollIntoViewIfNeeded();
    await more("Zobo").click();
    await sleep(1400);
    await say("The total adds up as he goes. Next.", 800);
    await sheet.getByRole("button", { name: /^Next/ }).click();
    await sheet.getByRole("heading", { name: "How was it paid?" }).waitFor();
    await sleep(1200);
    await say("How it was paid: cash, transfer or POS. The customer paid by transfer.", 600);
    await sheet.getByRole("radio", { name: "Transfer" }).click();
    await sleep(2600);
    await say("Record the sale.", 700);
    await sheet.getByRole("button", { name: "Record the sale" }).click();
    await sheet.waitFor({ state: "detached", timeout: 15000 });
    await sleep(1600);
    await say("The sale is in the chat, the stock comes down, and the money is written to today's sales.", 4200);
  });
  await page.context().close();
}

// 2 · Sam hands the shift over.
async function handover() {
  const page = await taskiem("sam");
  await act(page, "howto09-handover", "Sam · shift manager at Ada's Kitchen", async () => {
    await say("At the end of the day, Sam hands the shift over: Handover in the chat's tools.", 600);
    await (await tools(page)).getByRole("button", { name: /^Handover/ }).click();
    const sheet = page.getByTestId("handover-sheet");
    await sheet.getByTestId("handover-hero").waitFor();
    await sleep(1600);
    await say("The shift's sales, how they were paid, and the cash that should be in the till.", 3800);
    // The cash that should be there: "₦38,500 sold in cash" under the field.
    const all = await sheet.innerText();
    const cash = Number((all.match(/₦([\d,]+) sold in cash/)?.[1] ?? "0").replace(/,/g, ""));
    await say("Sam counts the cash and types what is there.", 600);
    const counted = sheet.getByLabel("Cash counted (₦)");
    await counted.scrollIntoViewIfNeeded();
    await counted.click();
    await counted.pressSequentially(String(Math.max(0, cash - 2000)), { delay: 110 });
    await sleep(1600);
    await say("Taskiem says at once if it is short or over, and by how much.", 3000);
    const note = sheet.getByLabel("A note for the next shift (optional)");
    await note.scrollIntoViewIfNeeded();
    await note.click();
    await note.pressSequentially("₦2,000 change given from the till for the generator", { delay: 40 });
    await sleep(800);
    await say("A note for the next shift, then Hand over. Ada is told, and Sam's shift ends.", 800);
    await sheet.getByRole("button", { name: "Hand over" }).click();
    await sheet.waitFor({ state: "detached", timeout: 15000 });
    await sleep(2600);
  });
  await page.context().close();
}

const PARTS = [
  { title: "How to: *sell at the counter, and hand over.*" },
  { title: "1. *Sell.*" },
  { act: "howto09-sell" },
  { title: "2. Hand *the shift over.*" },
  { act: "howto09-handover" },
];

// No argument: everything. "build": only join. Act names: record those, then join.
const ACTS = { sell, handover };
const which = process.argv.slice(2);
if (which.length === 0) for (const f of Object.values(ACTS)) await f();
else for (const n of which.filter((n) => n !== "build")) await ACTS[n]();
console.log(build(SLUG, PARTS));
process.exit(0);
